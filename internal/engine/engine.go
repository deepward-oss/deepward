package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/internal/strictyaml"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Options configure an engine.
type Options struct {
	Registry  *sdk.Registry
	Config    Config
	Workspace *sdk.Workspace
	// Tools is the toolchain; nil means no tools are available.
	Tools  sdk.Tools
	Clock  sdk.Clock
	Logger *slog.Logger
	// CacheDir is the root cache directory; plugins get subdirectories.
	CacheDir string
	NoCache  bool
	Tool     sdk.ToolInfo
	Run      sdk.RunInfo
	// ConfigFile is used in error messages.
	ConfigFile string
}

// Engine runs plugins.
type Engine struct {
	opts    Options
	cfg     Config
	ws      *sdk.Workspace
	enabled map[string]sdk.Plugin
	all     []PluginStatus
	clock   sdk.Clock
	log     *slog.Logger
	tools   sdk.Tools
}

// PluginStatus is a registered plugin and whether it is enabled.
type PluginStatus struct {
	Info    sdk.Info `json:"info"`
	Enabled bool     `json:"enabled"`
}

type enabledFlag struct {
	Enabled *bool `yaml:"enabled"`
}

// New binds configuration to plugins and initializes them.
func New(ctx context.Context, opts Options) (*Engine, error) {
	if opts.Registry == nil {
		opts.Registry = sdk.NewRegistry()
	}
	if opts.Clock == nil {
		opts.Clock = sdk.SystemClock{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Tools == nil {
		opts.Tools = noTools{}
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, err
	}
	e := &Engine{opts: opts, cfg: opts.Config, ws: opts.Workspace, enabled: map[string]sdk.Plugin{},
		clock: opts.Clock, log: opts.Logger, tools: opts.Tools}
	if e.ws != nil && len(e.cfg.Exclude) > 0 {
		e.ws.Exclude = append(e.ws.Exclude, e.cfg.Exclude...)
	}
	var errs []error
	for id := range e.cfg.Plugins {
		if _, ok := opts.Registry.Get(id); !ok {
			errs = append(errs, fmt.Errorf("plugins.%s: unknown plugin %q (see `deepward plugins list`)", id, id))
		}
	}
	for id := range e.cfg.Ecosystems {
		p, ok := opts.Registry.Get(id)
		if !ok || p.Info().Kind != sdk.KindEcosystem {
			errs = append(errs, fmt.Errorf("ecosystems.%s: unknown ecosystem plugin %q", id, id))
		}
	}
	for _, p := range opts.Registry.All() {
		info := p.Info()
		enabled, err := e.bind(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		e.all = append(e.all, PluginStatus{Info: info, Enabled: enabled})
		if enabled {
			e.enabled[info.ID] = p
		}
	}
	for id, eco := range e.cfg.Ecosystems {
		p := e.enabled[id]
		specs := map[string]bool{}
		if ap, ok := p.(sdk.AddonProvider); ok {
			for _, s := range ap.Addons() {
				specs[s.Name] = true
			}
		}
		for name, a := range eco.Addons {
			if !specs[name] && a.Kind == "" {
				errs = append(errs, fmt.Errorf("ecosystems.%s.addons.%s.kind is required for a new add-on", id, name))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for _, id := range e.enabledIDs() {
		p := e.enabled[id]
		init, ok := p.(sdk.Initializer)
		if !ok {
			continue
		}
		err := guard(ctx, e.pluginTimeout(), func(ctx context.Context) error { return init.Init(ctx, e.host(id)) })
		if err != nil {
			return nil, fmt.Errorf("plugin %s: init: %w", id, err)
		}
	}
	return e, nil
}

// bind decodes plugins.<id> into the plugin's configuration.
func (e *Engine) bind(p sdk.Plugin) (bool, error) {
	id := p.Info().ID
	node, ok := e.cfg.Plugins[id]
	if !ok {
		return !p.Info().DisabledByDefault, nil
	}
	path := "plugins." + id
	enabled := !p.Info().DisabledByDefault
	// Split off the generic "enabled" key.
	rest := node
	if node.Kind == yaml.MappingNode {
		rest.Content = nil
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "enabled" {
				var f enabledFlag
				pair := yaml.Node{Kind: yaml.MappingNode, Content: node.Content[i : i+2]}
				if err := strictyaml.Decode(&pair, &f, path, e.opts.ConfigFile); err != nil {
					return false, err
				}
				if f.Enabled != nil {
					enabled = *f.Enabled
				}
				continue
			}
			rest.Content = append(rest.Content, node.Content[i], node.Content[i+1])
		}
	} else if node.Kind != 0 && node.Tag != "!!null" {
		return false, &strictyaml.Error{File: e.opts.ConfigFile, Line: node.Line, Path: path, Msg: "expected a mapping"}
	}
	c, configurable := p.(sdk.Configurable)
	if !configurable {
		if len(rest.Content) > 0 {
			return false, &strictyaml.Error{File: e.opts.ConfigFile, Line: rest.Content[0].Line, Path: path + "." + rest.Content[0].Value,
				Msg: fmt.Sprintf("plugin %s has no settings besides \"enabled\"", id)}
		}
		return enabled, nil
	}
	target := c.ConfigTarget()
	if len(rest.Content) > 0 {
		if err := strictyaml.Decode(&rest, target, path, e.opts.ConfigFile); err != nil {
			return false, err
		}
	}
	if v, ok := target.(sdk.Validator); ok {
		if err := v.Validate(); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}
	return enabled, nil
}

func (e *Engine) pluginTimeout() time.Duration { return e.cfg.PluginTimeout.Std() }

func (e *Engine) enabledIDs() []string {
	ids := make([]string, 0, len(e.enabled))
	for id := range e.enabled {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Plugin returns an enabled plugin by ID (nil if unknown or disabled).
func (e *Engine) Plugin(id string) sdk.Plugin { return e.enabled[id] }

// Plugins lists every registered plugin with its enabled state.
func (e *Engine) Plugins() []PluginStatus { return e.all }

// Enabled returns the enabled plugins of a kind, sorted by ID.
func (e *Engine) Enabled(kind sdk.Kind) []sdk.Plugin {
	var out []sdk.Plugin
	for _, id := range e.enabledIDs() {
		if p := e.enabled[id]; p.Info().Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// Config returns the bound configuration.
func (e *Engine) Config() Config { return e.cfg }

// Workspace returns the scan root.
func (e *Engine) Workspace() *sdk.Workspace { return e.ws }

// Reporter returns the enabled reporter for a format.
func (e *Engine) Reporter(format string) (sdk.Reporter, bool) {
	for _, p := range e.Enabled(sdk.KindReporter) {
		if r, ok := p.(sdk.Reporter); ok && r.Format() == format {
			return r, true
		}
	}
	return nil, false
}

// Render renders a result with the reporter for format.
func (e *Engine) Render(ctx context.Context, res *sdk.ScanResult, format string, w io.Writer) error {
	r, ok := e.Reporter(format)
	if !ok {
		return fmt.Errorf("no enabled reporter for format %q", format)
	}
	return guard(ctx, e.pluginTimeout(), func(ctx context.Context) error { return r.Render(ctx, res, w) })
}

func (e *Engine) host(id string) *host { return &host{e: e, id: id} }

type host struct {
	e  *Engine
	id string
}

func (h *host) Plugins(kind sdk.Kind) []sdk.Plugin { return h.e.Enabled(kind) }
func (h *host) Tools() sdk.Tools                   { return h.e.tools }
func (h *host) Logger() *slog.Logger               { return h.e.log.With("plugin", h.id) }
func (h *host) Clock() sdk.Clock                   { return h.e.clock }

func (h *host) Settings() sdk.RunSettings {
	s := sdk.RunSettings{Offline: h.e.cfg.Offline, NoCache: h.e.opts.NoCache, CacheTTL: h.e.cfg.Cache.TTL.Std()}
	if h.e.opts.CacheDir != "" {
		s.CacheDir = filepath.Join(h.e.opts.CacheDir, h.id)
	}
	return s
}

// Addons returns the plugin's add-ons: declared defaults overlaid with
// ecosystems.<id>.addons, plus add-ons that exist only in configuration.
func (h *host) Addons() []sdk.Addon {
	p := h.e.enabled[h.id]
	cfg := h.e.cfg.Ecosystems[h.id].Addons
	var out []sdk.Addon
	seen := map[string]bool{}
	if ap, ok := p.(sdk.AddonProvider); ok {
		for _, s := range ap.Addons() {
			a := s.Default
			a.Name, a.Kind = s.Name, s.Kind
			if o, ok := cfg[s.Name]; ok {
				a = overlayAddon(a, o)
			}
			seen[s.Name] = true
			out = append(out, a)
		}
	}
	names := make([]string, 0, len(cfg))
	for n := range cfg {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		a := cfg[n]
		a.Name = n
		out = append(out, a)
	}
	return out
}

func overlayAddon(a, o sdk.Addon) sdk.Addon {
	if o.Coordinates != "" {
		a.Coordinates = o.Coordinates
	}
	if o.Version != "" {
		a.Version = o.Version
	}
	if o.Goal != "" {
		a.Goal = o.Goal
	}
	if o.File != "" {
		a.File = o.File
		a.Builtin = false
	}
	return a
}

type noTools struct{}

func (noTools) Require(_ context.Context, req sdk.ToolRequirement, _ *sdk.Project) (sdk.Tool, error) {
	return nil, &sdk.ToolError{Tool: req.Name, Reason: sdk.ErrToolNotFound, Detail: req.Name + " (no toolchain configured)"}
}

// noProgressKey marks a context whose scans log no progress (the nested
// verification scans of the fix pipeline, which log their own).
type noProgressKey struct{}

func withoutProgress(ctx context.Context) context.Context {
	return context.WithValue(ctx, noProgressKey{}, true)
}

// progress logs what the engine is doing at info level, unless log.progress
// is false or ctx belongs to a nested scan.
func (e *Engine) progress(ctx context.Context, msg string, args ...any) {
	if !e.cfg.Log.ProgressEnabled() || ctx.Value(noProgressKey{}) != nil {
		return
	}
	e.log.InfoContext(ctx, msg, args...)
}

// since returns the time elapsed since start, rounded for logs.
func (e *Engine) since(start time.Time) time.Duration {
	return e.clock.Now().Sub(start).Round(10 * time.Millisecond)
}
