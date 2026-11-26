package sdk

import (
	"sort"
	"time"
)

// SchemaVersion is the version of the ScanResult JSON schema.
const SchemaVersion = 1

// ScanResult is the canonical, serializable result of a run. Every reporter
// is a pure function of it.
type ScanResult struct {
	SchemaVersion int             `json:"schemaVersion"`
	Tool          ToolInfo        `json:"tool"`
	Run           RunInfo         `json:"run"`
	Projects      []ProjectResult `json:"projects"`
	Findings      []Finding       `json:"findings"`
	PluginRuns    []PluginRun     `json:"pluginRuns"`
	Warnings      []Warning       `json:"warnings"`
	DataSources   []DataSource    `json:"dataSources"`
	Summary       Summary         `json:"summary"`
	Gate          Gate            `json:"gate"`
}

// ToolInfo identifies the deepward build.
type ToolInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	SDKVersion string `json:"sdkVersion"`
}

// RunInfo describes the run context.
type RunInfo struct {
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	Root         string    `json:"root"`
	RepoURL      string    `json:"repoUrl,omitempty"`
	Commit       string    `json:"commit,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	TargetBranch string    `json:"targetBranch,omitempty"`
	CI           string    `json:"ci,omitempty"`
	Offline      bool      `json:"offline,omitempty"`
}

// ProjectStatus is the outcome of analyzing one project.
type ProjectStatus string

// Project statuses.
const (
	ProjectOK      ProjectStatus = "ok"
	ProjectSkipped ProjectStatus = "skipped"
	ProjectFailed  ProjectStatus = "failed"
)

// ProjectResult is a project and its outcome.
type ProjectResult struct {
	Project
	Status ProjectStatus `json:"status"`
	Reason string        `json:"reason,omitempty"`
	// Output is the tail of tool output for failed projects.
	Output []string `json:"output,omitempty"`
	// Facts holds reportable facts (e.g. "dependency-graph").
	Facts map[string]any `json:"facts,omitempty"`
}

// PluginRunStatus is the outcome of one plugin invocation.
type PluginRunStatus string

// Plugin run statuses.
const (
	RunOK      PluginRunStatus = "ok"
	RunSkipped PluginRunStatus = "skipped"
	RunFailed  PluginRunStatus = "failed"
)

// PluginRun records one plugin invocation.
type PluginRun struct {
	Plugin   string          `json:"plugin"`
	Kind     Kind            `json:"kind"`
	Project  string          `json:"project,omitempty"`
	Status   PluginRunStatus `json:"status"`
	Reason   string          `json:"reason,omitempty"`
	Findings int             `json:"findings"`
}

// Warning is a non-fatal problem.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Plugin  string `json:"plugin,omitempty"`
	Project string `json:"project,omitempty"`
}

// DataSource credits a data provider (e.g. a vulnerability database).
type DataSource struct {
	Name        string `json:"name"`
	License     string `json:"license"`
	Attribution string `json:"attribution,omitempty"`
	URL         string `json:"url,omitempty"`
	// Freshness is when the data was last updated, if known.
	Freshness *time.Time `json:"freshness,omitempty"`
}

// WarningReporter is implemented by plugins that collect non-fatal problems
// during a run (e.g. an unreachable registry). The engine reads them after
// all analyzers finished.
type WarningReporter interface {
	Warnings() []Warning
}

// DataSourceReporter is implemented by plugins whose data must be credited.
type DataSourceReporter interface {
	DataSources() []DataSource
}

// Summary aggregates active (unsuppressed) findings.
type Summary struct {
	Total        int                              `json:"total"`
	Suppressed   int                              `json:"suppressed"`
	BySeverity   map[Severity]int                 `json:"bySeverity"`
	ByKind       map[FindingKind]int              `json:"byKind"`
	ByAnalyzer   map[string]int                   `json:"byAnalyzer"`
	ByProject    map[string]int                   `json:"byProject"`
	Direct       int                              `json:"direct"`
	Transitive   int                              `json:"transitive"`
	KindSeverity map[FindingKind]map[Severity]int `json:"kindSeverity"`
	// NotBuilt counts the active findings marked NotBuilt (also included in
	// the totals above); NotBuiltBySeverity splits them by severity.
	NotBuilt           int              `json:"notBuilt"`
	NotBuiltBySeverity map[Severity]int `json:"notBuiltBySeverity,omitempty"`
}

// BuiltBySeverity returns the active findings by severity without the ones
// marked NotBuilt.
func (s Summary) BuiltBySeverity() map[Severity]int {
	out := make(map[Severity]int, len(s.BySeverity))
	for sev, n := range s.BySeverity {
		if n -= s.NotBuiltBySeverity[sev]; n > 0 {
			out[sev] = n
		}
	}
	return out
}

// Gate is the quality-gate outcome.
type Gate struct {
	Threshold Severity `json:"threshold,omitempty"`
	Failed    bool     `json:"failed"`
	// Count is the number of active findings at or above the threshold.
	Count int `json:"count"`
}

// Active returns the findings without a suppression.
func (r *ScanResult) Active() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Suppression == nil {
			out = append(out, f)
		}
	}
	return out
}

// ComputeSummary fills Summary from Findings.
func (r *ScanResult) ComputeSummary() {
	s := Summary{
		BySeverity:   map[Severity]int{},
		ByKind:       map[FindingKind]int{},
		ByAnalyzer:   map[string]int{},
		ByProject:    map[string]int{},
		KindSeverity: map[FindingKind]map[Severity]int{},
	}
	for _, f := range r.Findings {
		if f.Suppression != nil {
			s.Suppressed++
			continue
		}
		s.Total++
		sev := f.Severity.Normalize()
		s.BySeverity[sev]++
		s.ByKind[f.Kind]++
		s.ByAnalyzer[f.Analyzer]++
		if f.Project != "" {
			s.ByProject[f.Project]++
		}
		if s.KindSeverity[f.Kind] == nil {
			s.KindSeverity[f.Kind] = map[Severity]int{}
		}
		s.KindSeverity[f.Kind][sev]++
		if f.NotBuilt {
			if s.NotBuiltBySeverity == nil {
				s.NotBuiltBySeverity = map[Severity]int{}
			}
			s.NotBuilt++
			s.NotBuiltBySeverity[sev]++
		}
		if p := f.PrimaryPackage(); p != nil {
			if p.Direct {
				s.Direct++
			} else {
				s.Transitive++
			}
		}
	}
	r.Summary = s
}

// Normalize sorts every collection so the JSON encoding is deterministic.
func (r *ScanResult) Normalize() {
	sort.SliceStable(r.Projects, func(i, j int) bool { return r.Projects[i].ID < r.Projects[j].ID })
	for i := range r.Findings {
		f := &r.Findings[i]
		f.Severity = f.Severity.Normalize()
		if f.Fingerprint == "" {
			f.Fingerprint = f.ComputeFingerprint()
		}
		sort.Strings(f.Aliases)
	}
	SortFindings(r.Findings)
	sort.SliceStable(r.PluginRuns, func(i, j int) bool {
		a, b := r.PluginRuns[i], r.PluginRuns[j]
		if a.Plugin != b.Plugin {
			return a.Plugin < b.Plugin
		}
		return a.Project < b.Project
	})
	sort.SliceStable(r.Warnings, func(i, j int) bool {
		a, b := r.Warnings[i], r.Warnings[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Message < b.Message
	})
	sort.SliceStable(r.DataSources, func(i, j int) bool { return r.DataSources[i].Name < r.DataSources[j].Name })
	if r.Projects == nil {
		r.Projects = []ProjectResult{}
	}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	if r.PluginRuns == nil {
		r.PluginRuns = []PluginRun{}
	}
	if r.Warnings == nil {
		r.Warnings = []Warning{}
	}
	if r.DataSources == nil {
		r.DataSources = []DataSource{}
	}
	r.ComputeSummary()
}
