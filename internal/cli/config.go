package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/engine"
	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Default values.
const (
	DefaultOutputDir = "deepward-out"
	DefaultConfig    = "deepward.yaml"
)

// DefaultFormats are written when no format is configured.
var DefaultFormats = []string{"html", "json"}

// usageError marks errors that map to exit code 2.
type usageError struct{ err error }

func (u *usageError) Error() string { return u.err.Error() }
func (u *usageError) Unwrap() error { return u.err }

func usage(err error) error {
	if err == nil {
		return nil
	}
	return &usageError{err: err}
}

func usagef(format string, args ...any) error { return usage(fmt.Errorf(format, args...)) }

func isUsage(err error) bool {
	var u *usageError
	return errors.As(err, &u)
}

// loadConfig reads the configuration file for root. path may be empty (look
// for deepward.yaml / deepward.yml in root); a missing default file yields
// defaults.
func loadConfig(root, path string) (engine.Config, string, error) {
	var cfg engine.Config
	explicit := path != ""
	if !explicit {
		path = filepath.Join(root, DefaultConfig)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			alt := filepath.Join(root, "deepward.yml")
			if _, err := os.Stat(alt); err == nil {
				path = alt
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return cfg, "", nil
		}
		return cfg, path, usagef("read configuration: %w", err)
	}
	name := filepath.Base(path)
	doc, err := strictyaml.Parse(data, name)
	if err != nil {
		return cfg, path, usage(err)
	}
	if err := strictyaml.Decode(doc, &cfg, "", name); err != nil {
		return cfg, path, usage(err)
	}
	return cfg, path, nil
}

// setPluginEntry sets plugins.<plugin>.<key>.<k> = v in the raw config.
func setPluginEntry(cfg *engine.Config, plugin, key, k, v string) {
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]yaml.Node{}
	}
	node := cfg.Plugins[plugin]
	if node.Kind != yaml.MappingNode {
		node = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	var sub *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			sub = node.Content[i+1]
		}
	}
	if sub == nil || sub.Kind != yaml.MappingNode {
		sub = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sub)
	}
	for i := 0; i+1 < len(sub.Content); i += 2 {
		if sub.Content[i].Value == k {
			sub.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
			cfg.Plugins[plugin] = node
			return
		}
	}
	sub.Content = append(sub.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	cfg.Plugins[plugin] = node
}

// parsePairs parses "a=b" flag values.
func parsePairs(flag string, values []string) ([][2]string, error) {
	out := make([][2]string, 0, len(values))
	for _, v := range values {
		k, val, ok := strings.Cut(v, "=")
		if !ok || k == "" || val == "" {
			return nil, usagef("--%s expects <name>=<path>, got %q", flag, v)
		}
		out = append(out, [2]string{k, val})
	}
	return out, nil
}

var secretKey = regexp.MustCompile(`(?i)(token|password|passwd|secret|apikey|api_key|credential)`)

// maskedConfigYAML renders the effective configuration with secrets masked.
func maskedConfigYAML(cfg engine.Config) (string, error) {
	var node yaml.Node
	if err := node.Encode(cfg); err != nil {
		return "", err
	}
	maskNode(&node, "")
	pruneEmpty(&node)
	b, err := yaml.Marshal(&node)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func maskNode(n *yaml.Node, key string) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			maskNode(n.Content[i+1], n.Content[i].Value)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			maskNode(c, key)
		}
	case yaml.ScalarNode:
		if n.Value != "" && secretKey.MatchString(key) && !strings.HasPrefix(n.Value, "${env:") {
			n.Value = "***"
			n.Style = 0
		}
	}
}

// pruneEmpty removes keys whose values are empty, so the printout stays readable.
func pruneEmpty(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.MappingNode:
		var kept []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !pruneEmpty(n.Content[i+1]) {
				kept = append(kept, n.Content[i], n.Content[i+1])
			}
		}
		n.Content = kept
		return len(kept) == 0
	case yaml.SequenceNode:
		var kept []*yaml.Node
		for _, c := range n.Content {
			if !pruneEmpty(c) {
				kept = append(kept, c)
			}
		}
		n.Content = kept
		return len(kept) == 0
	case yaml.ScalarNode:
		return n.Value == "" || n.Value == "0" || n.Value == "false" || n.Value == "0s" || n.Tag == "!!null"
	}
	return false
}

// ExpandEnvRefs replaces ${env:NAME} references using getenv.
func ExpandEnvRefs(s string, getenv func(string) string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return getenv(envRef.FindStringSubmatch(m)[1])
	})
}

var envRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// sortedKeys returns map keys sorted.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func validFormats(formats []string, reporters []string) error {
	for _, f := range formats {
		ok := false
		for _, r := range reporters {
			if r == f {
				ok = true
			}
		}
		if !ok {
			return usagef("unknown format %q (available: %s)", f, strings.Join(reporters, ", "))
		}
	}
	return nil
}

func severityFlag(v string) (sdk.Severity, error) {
	if v == "" {
		return "", nil
	}
	s, err := sdk.ParseSeverity(v)
	if err != nil {
		return "", usagef("--fail-on: %w", err)
	}
	return s, nil
}
