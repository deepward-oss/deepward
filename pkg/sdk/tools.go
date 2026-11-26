package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ToolRequirement declares an external tool a plugin needs.
type ToolRequirement struct {
	// Name is the logical tool name (e.g. "mvn", "sbt", "dotnet", "semgrep").
	Name string `json:"name"`
	// Constraint is an optional version constraint such as ">=1.4" or
	// ">=3.6, <4". Empty accepts any version.
	Constraint string `json:"constraint,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
}

// ToolDefinition describes how to find and probe a tool.
type ToolDefinition struct {
	Name string
	// Executables are candidate file names on PATH (e.g. "mvn", "mvn.cmd").
	Executables []string
	// Wrappers are project-local wrapper scripts (relative to the project or
	// repository root), e.g. "mvnw".
	Wrappers []string
	// VersionArgs are passed to the tool to print its version.
	VersionArgs []string
	// VersionPattern is a regular expression whose first group is the version.
	VersionPattern string
	// JVMOptsEnv is the variable that receives tools.<name>.jvmOpts
	// (e.g. MAVEN_OPTS); default JAVA_OPTS.
	JVMOptsEnv string
}

// Tools gives plugins runnable tools. It is the only way a plugin may start a
// process.
type Tools interface {
	// Require resolves a tool for a project (nil for workspace-level use).
	Require(ctx context.Context, req ToolRequirement, project *Project) (Tool, error)
}

// Tool is a resolved, runnable tool.
type Tool interface {
	Name() string
	Version() string
	// Resolver is the ID of the resolver that found the tool.
	Resolver() string
	// Command is the invocation prefix (e.g. ["./mvnw"]).
	Command() []string
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

// RunSpec describes one invocation.
type RunSpec struct {
	Args []string
	// Dir is the working directory (absolute). Empty means the workspace root.
	Dir string
	// Env adds variables to the tool's environment.
	Env map[string]string
	// Secrets are values to mask in captured output in addition to the ones
	// the toolchain already knows.
	Secrets []string
	Stdin   io.Reader
	// Timeout overrides the tool's configured timeout.
	Timeout time.Duration
}

// RunResult is the outcome of a finished process.
type RunResult struct {
	ExitCode int
	// Stdout is the full standard output (masked).
	Stdout []byte
	// Tail holds the last lines of combined output (masked), for diagnostics.
	Tail []string
}

// Tool resolution errors.
var (
	ErrToolNotFound        = errors.New("tool-not-found")
	ErrToolVersionMismatch = errors.New("tool-version-mismatch")
)

// ToolError explains a failed resolution.
type ToolError struct {
	Tool   string
	Reason error // ErrToolNotFound or ErrToolVersionMismatch
	Detail string
}

func (e *ToolError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%v: %s", e.Reason, e.Tool)
	}
	return fmt.Sprintf("%v: %s", e.Reason, e.Detail)
}

// Unwrap supports errors.Is with the sentinel reasons.
func (e *ToolError) Unwrap() error { return e.Reason }

// ExitError is returned by Tool.Run when the process exits non-zero; the
// RunResult is still populated.
type ExitError struct {
	Tool     string
	ExitCode int
	Tail     []string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with code %d", e.Tool, e.ExitCode)
}

// ToolSettings are the per-tool configuration values (tools.<name> in
// deepward.yaml). They are passed to resolvers.
type ToolSettings struct {
	Path      string   `yaml:"path" json:"path,omitempty"`
	Resolvers []string `yaml:"resolvers" json:"resolvers,omitempty"`
	Image     string   `yaml:"image" json:"image,omitempty"`
	// ContainerUser selects the user of the container resolver: "" or
	// "host" (the host UID/GID, default), "image" (the image's user) or an
	// explicit "uid:gid".
	ContainerUser string            `yaml:"containerUser" json:"containerUser,omitempty"`
	Download      *DownloadSettings `yaml:"download" json:"download,omitempty"`
	Args          []string          `yaml:"args" json:"args,omitempty"`
	Env           map[string]string `yaml:"env" json:"env,omitempty"`
	JavaHome      string            `yaml:"javaHome" json:"javaHome,omitempty"`
	JVMOpts       string            `yaml:"jvmOpts" json:"jvmOpts,omitempty"`
	Workdir       string            `yaml:"workdir" json:"workdir,omitempty"`
	Timeout       Duration          `yaml:"timeout" json:"timeout,omitempty"`
	// Executables and VersionArgs/VersionPattern define user tools.
	Executables    []string `yaml:"executables" json:"executables,omitempty"`
	VersionArgs    []string `yaml:"versionArgs" json:"versionArgs,omitempty"`
	VersionPattern string   `yaml:"versionPattern" json:"versionPattern,omitempty"`
}

// DownloadSettings configure the download resolver.
type DownloadSettings struct {
	URL    string `yaml:"url" json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	// Binary is the path of the executable inside the archive (or the file
	// name for a bare binary).
	Binary string `yaml:"binary" json:"binary,omitempty"`
}

// ToolResolver locates a tool. It returns (nil, nil) when it does not apply.
type ToolResolver interface {
	Plugin
	Resolve(ctx context.Context, req ResolveRequest) (*ToolCandidate, error)
}

// ResolveRequest is passed to resolvers.
type ResolveRequest struct {
	Definition ToolDefinition
	Settings   ToolSettings
	Project    *Project
	Workspace  *Workspace
	// CacheDir is a directory the resolver may use (e.g. for downloads).
	CacheDir string
}

// ToolCandidate is a resolved invocation prefix. Command may contain the
// placeholders {{dir}} (the working directory of each run), {{dirmount}}
// (expanded to "-v DIR:DIR" when that directory is outside the workspace,
// otherwise to nothing) and {{env}} (expanded to "-e NAME" pairs for the
// tool-specific variables), which container-based resolvers use.
type ToolCandidate struct {
	Command []string
	Env     map[string]string
	// Source describes where the tool came from (path, image, URL).
	Source string
	// MapPath translates host paths for tools running elsewhere (containers);
	// nil means identity.
	MapPath func(string) string `json:"-"`
}
