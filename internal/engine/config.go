// Package engine is the run engine: plugin registry and configuration
// binding, the fact store, the scan and fix pipelines and the quality gate.
// It knows only the contracts in pkg/sdk, never concrete plugins.
package engine

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Config is the engine-relevant part of deepward.yaml.
type Config struct {
	Exclude []string     `yaml:"exclude"`
	FailOn  sdk.Severity `yaml:"failOn"`
	Ignore  []IgnoreRule `yaml:"ignore"`
	// Plugins holds raw plugins.<id> sections; they are decoded into each
	// plugin's own configuration struct.
	Plugins map[string]yaml.Node `yaml:"plugins"`
	// Tools holds per-tool settings (used by the toolchain).
	Tools map[string]sdk.ToolSettings `yaml:"tools"`
	// Ecosystems holds per-ecosystem add-ons and setup steps.
	Ecosystems map[string]EcosystemConfig `yaml:"ecosystems"`
	// Projects holds per-project settings keyed by project directory.
	Projects map[string]ProjectConfig `yaml:"projects"`
	// Integrations holds raw config-defined integrations (see internal/integrations).
	Integrations map[string]yaml.Node `yaml:"integrations"`
	Fix          FixConfig            `yaml:"fix"`
	Platform     PlatformSettings     `yaml:"platform"`
	Output       OutputConfig         `yaml:"output"`
	Cache        CacheConfig          `yaml:"cache"`
	Offline      bool                 `yaml:"offline"`
	// PluginTimeout bounds every plugin call (default 30m).
	PluginTimeout sdk.Duration `yaml:"pluginTimeout"`
	// Concurrency is the number of analyzer invocations run in parallel.
	Concurrency int `yaml:"concurrency"`
	// AllowPartial makes project failures non-fatal for the exit code.
	AllowPartial bool `yaml:"allowPartial"`
}

// IgnoreRule suppresses findings.
type IgnoreRule struct {
	// ID is a rule ID or alias (e.g. CVE-2022-1471).
	ID string `yaml:"id"`
	// PURL optionally restricts the rule to packages matching this purl or
	// glob pattern (without version), e.g. "pkg:maven/org.yaml/*".
	PURL string `yaml:"purl"`
	// Analyzer optionally restricts the rule to one analyzer.
	Analyzer string    `yaml:"analyzer"`
	Reason   string    `yaml:"reason"`
	Expires  *sdk.Date `yaml:"expires"`
}

// EcosystemConfig configures one ecosystem plugin.
type EcosystemConfig struct {
	Addons map[string]sdk.Addon `yaml:"addons"`
	Setup  []SetupStep          `yaml:"setup"`
}

// ProjectConfig configures one project (keyed by its directory).
type ProjectConfig struct {
	Setup []SetupStep `yaml:"setup"`
}

// SetupStep is a command run before facts are produced for a project.
type SetupStep struct {
	Tool string            `yaml:"tool"`
	Args []string          `yaml:"args"`
	Env  map[string]string `yaml:"env"`
	// Dir is relative to the project directory (default: the project directory).
	Dir string `yaml:"dir"`
}

// FixConfig configures the fix pipeline.
type FixConfig struct {
	// Branch is the branch name template; see DefaultBranchTemplate.
	Branch string `yaml:"branch"`
	// Grouping selects how fixes are split into change requests when no
	// group rule matches: package (default), type or project.
	Grouping       string      `yaml:"grouping" enum:"package,type,project"`
	Limit          *int        `yaml:"limit"`
	Labels         []string    `yaml:"labels"`
	SecurityLabels []string    `yaml:"securityLabels"`
	Groups         []GroupRule `yaml:"groups"`
	// MaxCandidates bounds the ancestor upgrade trial per finding.
	MaxCandidates int `yaml:"maxCandidates"`
	// TargetBranch overrides the detected default branch.
	TargetBranch string `yaml:"targetBranch"`
}

// GroupRule groups updates into one change request.
type GroupRule struct {
	Name string `yaml:"name"`
	// Match is a purl glob pattern (without version).
	Match []string `yaml:"match"`
}

// PlatformSettings selects the hosting platform.
type PlatformSettings struct {
	Type   string `yaml:"type"`
	APIURL string `yaml:"apiUrl"`
	Repo   string `yaml:"repo"`
}

// OutputConfig selects report formats and directory.
type OutputConfig struct {
	Dir     string   `yaml:"dir"`
	Formats []string `yaml:"formats"`
}

// CacheConfig configures the on-disk cache.
type CacheConfig struct {
	Dir string       `yaml:"dir"`
	TTL sdk.Duration `yaml:"ttl"`
}

// Validate checks engine-level settings that do not depend on plugins.
func (c *Config) Validate() error {
	var errs []error
	if c.FailOn != "" {
		if _, err := sdk.ParseSeverity(string(c.FailOn)); err != nil {
			errs = append(errs, fmt.Errorf("failOn: %w", err))
		}
	}
	for i, r := range c.Ignore {
		if r.ID == "" {
			errs = append(errs, fmt.Errorf("ignore[%d].id is required", i))
		}
		if r.Reason == "" {
			errs = append(errs, fmt.Errorf("ignore[%d].reason is required", i))
		}
	}
	for name, eco := range c.Ecosystems {
		for i, s := range eco.Setup {
			if s.Tool == "" {
				errs = append(errs, fmt.Errorf("ecosystems.%s.setup[%d].tool is required", name, i))
			}
		}
	}
	for dir, p := range c.Projects {
		for i, s := range p.Setup {
			if s.Tool == "" {
				errs = append(errs, fmt.Errorf("projects.%s.setup[%d].tool is required", dir, i))
			}
		}
	}
	if c.Concurrency < 0 {
		errs = append(errs, errors.New("concurrency must be >= 0"))
	}
	if c.Fix.Limit != nil && *c.Fix.Limit < 0 {
		errs = append(errs, errors.New("fix.limit must be >= 0"))
	}
	for i, g := range c.Fix.Groups {
		if g.Name == "" || len(g.Match) == 0 {
			errs = append(errs, fmt.Errorf("fix.groups[%d] needs a name and at least one match pattern", i))
		}
	}
	if c.Fix.Branch != "" {
		if err := ValidateBranchTemplate(c.Fix.Branch); err != nil {
			errs = append(errs, fmt.Errorf("fix.branch: %w", err))
		}
	}
	switch c.Fix.Grouping {
	case "", GroupingPackage, GroupingType, GroupingProject:
	default:
		errs = append(errs, fmt.Errorf("fix.grouping: %q is not one of %s, %s, %s", c.Fix.Grouping, GroupingPackage, GroupingType, GroupingProject))
	}
	return errors.Join(errs...)
}
