// Package toolchain implements sdk.Tools: it resolves external tools through
// an ordered chain of resolver plugins, probes their versions, applies
// per-tool settings and runs processes with timeouts, output capture,
// secret masking and an environment allowlist. It is the only package that
// starts processes.
package toolchain

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// TailLines is the number of output lines kept for diagnostics.
const TailLines = 200

// DefaultTimeout bounds a tool invocation without a configured timeout.
const DefaultTimeout = 30 * time.Minute

// envAllowlist lists variables (or prefixes ending in "*") passed to tools.
var envAllowlist = []string{
	"PATH", "HOME", "USER", "USERNAME", "LOGNAME", "SHELL", "LANG", "LC_*", "TZ", "TERM",
	"TMP", "TEMP", "TMPDIR", "XDG_*",
	"JAVA_HOME", "JAVA_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "MAVEN_*", "M2_HOME", "SBT_*", "COURSIER_*", "IVY_HOME",
	"DOTNET_*", "NUGET_*", "MSBUILD*",
	"NODE_*", "NPM_CONFIG_*", "npm_config_*", "YARN_*", "PNPM_*", "COREPACK_*", "NVM_*",
	"GOPATH", "GOROOT", "GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOSUMDB", "GOFLAGS", "GOINSECURE",
	"GOTOOLCHAIN", "GOMODCACHE", "GOCACHE", "GOENV", "GOAUTH", "GOEXPERIMENT", "GOOS", "GOARCH", "CGO_ENABLED",
	"CARGO_*", "RUSTUP_*", "RUSTC", "RUSTC_WRAPPER", "RUSTFLAGS",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"CI", "GITLAB_CI", "GITHUB_ACTIONS", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
	// Windows essentials.
	"SystemRoot", "SYSTEMROOT", "ComSpec", "COMSPEC", "PATHEXT", "WINDIR", "APPDATA", "LOCALAPPDATA", "USERPROFILE",
	"ProgramData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "CommonProgramFiles", "HOMEDRIVE", "HOMEPATH",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS",
}

// allowed reports whether a variable from the parent environment may be
// passed to a tool. Platform tokens are never passed.
func allowed(name string) bool {
	if strings.HasPrefix(name, "DEEPWARD_") && strings.Contains(name, "TOKEN") {
		return false
	}
	for _, a := range envAllowlist {
		if prefix, ok := strings.CutSuffix(a, "*"); ok {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		} else if name == a {
			return true
		}
	}
	return false
}

// filterEnv returns the allowlisted subset of environ ("K=V" entries).
func filterEnv(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" && allowed(k) {
			out[k] = v
		}
	}
	return out
}

// masker replaces secret values in output.
type masker struct{ secrets []string }

func newMasker(secrets []string) masker {
	var s []string
	for _, v := range secrets {
		// Very short values would mask unrelated text.
		if len(v) >= 4 {
			s = append(s, v)
		}
	}
	// Longest first so overlapping secrets are fully masked.
	sort.Slice(s, func(i, j int) bool { return len(s[i]) > len(s[j]) })
	return masker{secrets: s}
}

func (m masker) mask(b []byte) []byte {
	for _, s := range m.secrets {
		b = bytes.ReplaceAll(b, []byte(s), []byte("***"))
	}
	return b
}

// ring keeps the last n lines written to it.
type ring struct {
	mu      sync.Mutex
	n       int
	lines   []string
	partial []byte
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data := append(append([]byte{}, r.partial...), p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		r.push(strings.TrimRight(string(data[:i]), "\r"))
		data = data[i+1:]
	}
	r.partial = append([]byte{}, data...)
	return len(p), nil
}

func (r *ring) push(line string) {
	r.lines = append(r.lines, line)
	if len(r.lines) > r.n {
		r.lines = r.lines[len(r.lines)-r.n:]
	}
}

func (r *ring) tail() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string{}, r.lines...)
	if len(r.partial) > 0 {
		out = append(out, string(r.partial))
		if len(out) > r.n {
			out = out[len(out)-r.n:]
		}
	}
	return out
}

// process describes one invocation after all settings were applied.
type process struct {
	name    string
	argv    []string
	dir     string
	env     map[string]string
	stdin   io.Reader
	timeout time.Duration
	secrets []string
}

// execute runs a process. A non-zero exit returns *sdk.ExitError together
// with the populated result.
func execute(ctx context.Context, p process) (sdk.RunResult, error) {
	if len(p.argv) == 0 {
		return sdk.RunResult{}, errors.New("empty command")
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.argv[0], p.argv[1:]...)
	cmd.Dir = p.dir
	keys := make([]string, 0, len(p.env))
	for k := range p.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmd.Env = make([]string, 0, len(keys))
	for _, k := range keys {
		cmd.Env = append(cmd.Env, k+"="+p.env[k])
	}
	cmd.Stdin = p.stdin
	tail := &ring{n: TailLines}
	var stdout bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, tail)
	cmd.Stderr = tail
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	m := newMasker(p.secrets)
	res := sdk.RunResult{Stdout: m.mask(stdout.Bytes())}
	for _, l := range tail.tail() {
		res.Tail = append(res.Tail, string(m.mask([]byte(l))))
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.ExitCode = -1
		return res, &sdk.ExitError{Tool: p.name, ExitCode: -1, Tail: append(res.Tail, "deepward: timed out after "+timeout.String())}
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, &sdk.ExitError{Tool: p.name, ExitCode: res.ExitCode, Tail: res.Tail}
		}
		res.ExitCode = -1
		return res, err
	}
	return res, nil
}
