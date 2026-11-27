// Package sdk is the public plugin API of deepward. The engine knows only
// these contracts; every ecosystem, analyzer, reporter, hosting platform, tool
// resolver and output format is a plugin implementing one of them.
//
// The SDK is v0: contracts may still change. New capabilities are added as
// optional interfaces rather than by changing existing methods.
package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"
)

// SDKVersion is the version of the plugin contracts.
const SDKVersion = "0.3.0"

// BuiltinVersion is the Info.Version of plugins shipped with Deepward. They
// are versioned with Deepward itself, and tools show the Deepward version
// for them. Third-party plugins set their own version.
const BuiltinVersion = "builtin"

// Kind identifies a plugin contract.
type Kind string

// Plugin kinds orchestrated by the engine. SDK subpackages may define more
// kinds (for example deps.KindAdvisorySource) that are only consumed by other
// plugins.
const (
	KindEcosystem    Kind = "ecosystem"
	KindAnalyzer     Kind = "analyzer"
	KindFactProducer Kind = "fact-producer"
	KindReporter     Kind = "reporter"
	KindPlatform     Kind = "platform"
	KindToolResolver Kind = "tool-resolver"
	KindOutputFormat Kind = "output-format"
)

// Info describes a plugin.
type Info struct {
	ID          string `json:"id"`
	Kind        Kind   `json:"kind"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Integration is true for plugins defined in configuration rather than code.
	Integration bool `json:"integration,omitempty"`
	// DisabledByDefault plugins run only when plugins.<id>.enabled is true.
	DisabledByDefault bool `json:"disabledByDefault,omitempty"`
}

// Plugin is implemented by every plugin.
type Plugin interface {
	Info() Info
}

// Configurable is implemented by plugins that accept settings under
// plugins.<id> in deepward.yaml. ConfigTarget returns a pointer to a struct
// (with yaml tags) pre-filled with defaults; the engine decodes the section
// into it with unknown-key checking. If the struct implements Validator, the
// engine calls Validate afterwards.
type Configurable interface {
	ConfigTarget() any
}

// Validator validates a decoded configuration struct.
type Validator interface {
	Validate() error
}

// Initializer is implemented by plugins that need run services. Init is
// called once after configuration and before any other method.
type Initializer interface {
	Init(ctx context.Context, host Host) error
}

// Host gives a plugin access to run-scoped services. Each plugin gets its own
// Host instance.
type Host interface {
	// Plugins returns the enabled plugins of a kind, sorted by ID.
	Plugins(kind Kind) []Plugin
	Tools() Tools
	Logger() *slog.Logger
	Clock() Clock
	Settings() RunSettings
	// Addons returns the plugin's add-ons: its declared defaults overlaid with
	// configuration (see AddonProvider).
	Addons() []Addon
}

// RunSettings are global options that plugins must respect.
type RunSettings struct {
	Offline  bool
	NoCache  bool
	CacheDir string // per-plugin cache directory, created on demand by the plugin
	// CacheTTL is the configured metadata cache TTL (0 means the plugin default).
	CacheTTL time.Duration
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidID reports whether id is kebab-case.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Registry holds plugin registrations.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{plugins: map[string]Plugin{}} }

// Register adds a plugin. IDs must be unique and kebab-case.
func (r *Registry) Register(p Plugin) error {
	info := p.Info()
	if !ValidID(info.ID) {
		return fmt.Errorf("plugin ID %q is not kebab-case", info.ID)
	}
	if info.Kind == "" {
		return fmt.Errorf("plugin %q has no kind", info.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.plugins[info.ID]; dup {
		return fmt.Errorf("plugin ID %q registered twice", info.ID)
	}
	r.plugins[info.ID] = p
	return nil
}

// Get returns a plugin by ID.
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[id]
	return p, ok
}

// All returns all plugins sorted by kind, then ID.
func (r *Registry) All() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Info(), out[j].Info()
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	return out
}

// ByKind returns the plugins of a kind sorted by ID.
func (r *Registry) ByKind(kind Kind) []Plugin {
	var out []Plugin
	for _, p := range r.All() {
		if p.Info().Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

var defaultRegistry = NewRegistry()

// Register adds a plugin to the default registry; it panics on invalid or
// duplicate IDs because it is meant to be called from init().
func Register(p Plugin) {
	if err := defaultRegistry.Register(p); err != nil {
		panic(err)
	}
}

// DefaultRegistry returns the registry populated by Register.
func DefaultRegistry() *Registry { return defaultRegistry }
