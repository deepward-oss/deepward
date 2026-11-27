package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

// DefaultLimit is the default number of concurrently open bot change requests.
const DefaultLimit = 5

// markerPrefix starts the hidden metadata comment in change-request bodies.
const markerPrefix = "<!-- deepward:meta "

// Marker is the metadata deepward keeps in its change requests.
type Marker struct {
	Project string   `json:"project"`
	Group   string   `json:"group"`
	Items   []string `json:"items"`
	Paused  bool     `json:"paused,omitempty"`
}

var markerRe = regexp.MustCompile(`<!-- deepward:meta (\{.*?\}) -->`)

// ParseMarker extracts the metadata of a change-request body.
func ParseMarker(body string) (Marker, bool) {
	m := markerRe.FindStringSubmatch(body)
	if m == nil {
		return Marker{}, false
	}
	var mk Marker
	if json.Unmarshal([]byte(m[1]), &mk) != nil {
		return Marker{}, false
	}
	return mk, true
}

func advisoryLink(f sdk.Finding) string {
	for _, r := range f.References {
		if r.URL != "" {
			return fmt.Sprintf("[%s](%s)", f.Rule, r.URL)
		}
	}
	return f.Rule
}

// Body renders the change-request description.
func Body(cs ChangeSet) string {
	var b strings.Builder
	if cs.Unverified {
		b.WriteString("> [!WARNING]\n> **Not verified.** Deepward prepared this change, but after rebuilding the project with it, " +
			"some targeted findings are still reported. It is a candidate, not a confirmed fix: review the dependency tree before merging, " +
			"and do not merge it automatically.\n\n")
		writeUnverified(&b, cs)
	} else {
		b.WriteString("This change was prepared and verified by **Deepward**: the project was rebuilt with the change and the targeted findings are gone.\n\n")
	}
	if cs.NotBuilt {
		b.WriteString("> **Not built:** the affected packages are resolved dependencies of the project but are not compiled into it, " +
			"so the vulnerable code does not ship. The update keeps the dependency files clean for other scanners and audits; it is not urgent.\n\n")
	}
	withModules := false
	for _, it := range cs.Items {
		withModules = withModules || len(itemModules(it)) > 0
	}
	if withModules {
		b.WriteString("| Package | Change | Type | Modules |\n|---|---|---|---|\n")
	} else {
		b.WriteString("| Package | Change | Type |\n|---|---|---|\n")
	}
	for _, it := range cs.Items {
		modules := ""
		if withModules {
			modules = " " + strings.Join(itemModules(it), ", ") + " |"
		}
		kind := it.UpdateType
		switch {
		case it.Pin:
			kind = "pin (transitive)"
		case it.Upgrade && kind != "":
			kind = "security fix, " + kind + " update"
		case it.Upgrade:
			kind = "security fix and update"
		case it.Kind == sdk.KindVulnerability:
			kind = "security fix"
		case kind == "":
			kind = string(it.Kind)
		}
		if it.Text {
			fmt.Fprintf(&b, "| %s | text edit | %s |%s\n", it.Package, kind, modules)
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s → **%s** | %s |%s\n", packageName(it.Package), it.From, it.To, kind, modules)
	}
	var advisories []string
	seen := map[string]bool{}
	for _, it := range cs.Items {
		for _, f := range it.Findings {
			if f.Kind != sdk.KindVulnerability || seen[f.Rule] {
				continue
			}
			seen[f.Rule] = true
			line := fmt.Sprintf("- %s (%s)", advisoryLink(f), f.Severity.Normalize())
			if f.NotBuilt {
				line = fmt.Sprintf("- %s (%s, not built)", advisoryLink(f), f.Severity.Normalize())
			}
			if f.Title != "" {
				line += ": " + f.Title
			}
			advisories = append(advisories, line)
		}
	}
	if len(advisories) > 0 {
		sort.Strings(advisories)
		heading := "Fixed vulnerabilities"
		if cs.Unverified {
			heading = "Targeted vulnerabilities"
		}
		b.WriteString("\n### " + heading + "\n\n")
		b.WriteString(strings.Join(advisories, "\n"))
		b.WriteString("\n")
	}
	var notes []string
	for _, it := range cs.Items {
		for _, f := range it.Findings {
			if u, ok := f.Properties["releaseNotes"].(string); ok && u != "" {
				notes = append(notes, fmt.Sprintf("- `%s`: %s", packageName(it.Package), u))
				break
			}
		}
	}
	if len(notes) > 0 {
		b.WriteString("\n### Release notes\n\n" + strings.Join(notes, "\n") + "\n")
	}
	var tasks []string
	for _, it := range cs.Items {
		for _, t := range it.Tasks {
			if !contains(tasks, t) {
				tasks = append(tasks, t)
			}
		}
	}
	if len(tasks) > 0 {
		b.WriteString("\n### Post tasks\n\nRun after the change, their output is part of this request:\n\n")
		for _, t := range tasks {
			b.WriteString("- `" + t + "`\n")
		}
	}
	b.WriteString("\n<sub>Project `" + cs.Project.ID + "` · group `" + cs.Group + "`. Deepward updates this request on every run; push your own commits to pause it.</sub>\n\n")
	items := make([]string, 0, len(cs.Items))
	for _, it := range cs.Items {
		items = append(items, it.Package+"@"+it.To)
	}
	meta, _ := json.Marshal(Marker{Project: cs.Project.ID, Group: cs.Group, Items: items})
	b.WriteString(markerPrefix + string(meta) + " -->\n")
	return b.String()
}

// itemModules lists, sorted, the modules that the findings of an item affect.
func itemModules(it FixItem) []string {
	var out []string
	for _, f := range it.Findings {
		if p := f.PrimaryPackage(); p != nil {
			for _, m := range p.Modules {
				if !contains(out, m.Name) {
					out = append(out, m.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// writeUnverified lists why each unverified item failed verification and
// how to inspect it.
func writeUnverified(b *strings.Builder, cs ChangeSet) {
	b.WriteString("### Why it is not verified\n\n")
	for _, it := range cs.Items {
		if !it.Unverified {
			continue
		}
		name := it.Package
		if !it.Text {
			name = "`" + packageName(it.Package) + "` " + it.From + " → " + it.To
		}
		fmt.Fprintf(b, "- %s: %s.", name, it.VerifyFailure)
		if h := inspectHint(cs.Project, it); h != "" {
			fmt.Fprintf(b, " Inspect with %s.", h)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nCommon causes: a pin or upgrade does not reach every path to the vulnerable package, the advisory is about a " +
		"different artifact or version range, or another dependency brings the package again.\n\n")
}

// inspectHint suggests a command that shows where the vulnerable package of
// an item comes from.
func inspectHint(p sdk.Project, it FixItem) string {
	var u purl.PURL
	for _, f := range it.Findings {
		if pkg := f.PrimaryPackage(); pkg != nil {
			if parsed, err := purl.Parse(pkg.PURL); err == nil {
				u = parsed
				break
			}
		}
	}
	if u.Name == "" {
		return ""
	}
	name := u.Coordinates()
	switch p.Ecosystem {
	case "maven":
		return "`mvn dependency:tree -Dverbose -Dincludes=" + name + "`"
	case "sbt":
		return "`sbt \"whatDependsOn " + strings.ReplaceAll(name, ":", " ") + "\"`"
	case "npm":
		return "`npm ls " + name + "`"
	case "go":
		return "`go mod why -m " + name + "`"
	case "cargo":
		return "`cargo tree -i " + name + "`"
	case "nuget":
		return "`dotnet list package --include-transitive`"
	}
	return ""
}

// PublishResult reports what happened on the platform.
type PublishResult struct {
	Created  []sdk.ChangeRequest `json:"created"`
	Updated  []sdk.ChangeRequest `json:"updated"`
	Closed   []sdk.ChangeRequest `json:"closed"`
	Paused   []sdk.ChangeRequest `json:"paused"`
	Deferred []ChangeSet         `json:"deferred"`
}

// Publish synchronizes change requests with the plan: it updates existing
// requests (force-pushing their branch), creates new ones within the limit
// (critical and high fixes bypass it), pauses requests that contain
// commits by other authors and closes requests that are no longer needed.
func (e *Engine) Publish(ctx context.Context, client sdk.PlatformClient, plan *FixPlan, targetBranch string) (*PublishResult, error) {
	prefix := branchPrefix(e.branchTemplate())
	limit := DefaultLimit
	if e.cfg.Fix.Limit != nil {
		limit = *e.cfg.Fix.Limit
	}
	open, err := client.ListChangeRequests(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list change requests: %w", err)
	}
	byBranch := map[string]sdk.ChangeRequest{}
	bot := 0
	for _, cr := range open {
		if _, ok := ParseMarker(cr.Body); ok {
			byBranch[cr.SourceBranch] = cr
			bot++
		}
	}
	res := &PublishResult{}
	planned := map[string]bool{}
	// carriedBy maps a project and package@version to the title of the
	// planned request that contains that change.
	carriedBy := map[string]string{}
	for _, cs := range plan.ChangeSets {
		planned[cs.Branch] = true
		for _, it := range cs.Items {
			carriedBy[cs.Project.ID+"\x00"+it.Package+"@"+it.To] = cs.Title
		}
		spec := sdk.ChangeRequestSpec{Title: cs.Title, Body: Body(cs), SourceBranch: cs.Branch, TargetBranch: targetBranch, Labels: cs.Labels}
		if existing, ok := byBranch[cs.Branch]; ok {
			commits, err := client.BranchCommits(ctx, cs.Branch, targetBranch)
			if err != nil {
				return res, fmt.Errorf("branch %s: %w", cs.Branch, err)
			}
			if human(commits) {
				mk, _ := ParseMarker(existing.Body)
				if !mk.Paused {
					if err := client.Comment(ctx, existing.ID, "Deepward paused automatic updates of this request because the branch contains commits by other authors. Close the request or remove those commits to resume."); err != nil {
						return res, err
					}
					mk.Paused = true
					meta, _ := json.Marshal(mk)
					body := markerRe.ReplaceAllString(existing.Body, markerPrefix+string(meta)+" -->")
					if _, err := client.UpdateChangeRequest(ctx, existing.ID, sdk.ChangeRequestSpec{Title: existing.Title, Body: body,
						SourceBranch: existing.SourceBranch, TargetBranch: existing.TargetBranch, Labels: existing.Labels}); err != nil {
						return res, err
					}
				}
				res.Paused = append(res.Paused, existing)
				continue
			}
			if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: cs.Branch, BaseBranch: targetBranch, Message: commitMessage(cs), Files: cs.Files, Force: true}); err != nil {
				return res, fmt.Errorf("commit %s: %w", cs.Branch, err)
			}
			cr, err := client.UpdateChangeRequest(ctx, existing.ID, spec)
			if err != nil {
				return res, err
			}
			res.Updated = append(res.Updated, cr)
			continue
		}
		if bot >= limit && !cs.Urgent {
			res.Deferred = append(res.Deferred, cs)
			continue
		}
		if _, err := client.Commit(ctx, sdk.CommitRequest{Branch: cs.Branch, BaseBranch: targetBranch, Message: commitMessage(cs), Files: cs.Files, Force: true}); err != nil {
			return res, fmt.Errorf("commit %s: %w", cs.Branch, err)
		}
		cr, err := client.CreateChangeRequest(ctx, spec)
		if err != nil {
			return res, err
		}
		bot++
		res.Created = append(res.Created, cr)
	}
	scanned := map[string]bool{}
	if plan.Result != nil {
		for _, p := range plan.Result.Projects {
			if p.Status == sdk.ProjectOK {
				scanned[p.ID] = true
			}
		}
	}
	blocked := map[string]bool{}
	for _, m := range plan.Manual {
		blocked[m.Project+"\x00"+m.Group] = true
	}
	for _, f := range plan.Failed {
		blocked[f.Project+"\x00"+f.Group] = true
	}
	branches := make([]string, 0, len(byBranch))
	for b := range byBranch {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	for _, b := range branches {
		cr := byBranch[b]
		mk, _ := ParseMarker(cr.Body)
		// Requests of groups that fix.kinds or fix.minSeverity leave out are
		// not managed in this run: they stay as they are.
		comment := ""
		if !planned[b] && !scanned[mk.Project] {
			// A project that became part of a larger one (for example a .NET
			// project file that is now a module of its solution).
			if into := mergedInto(plan.Result, mk.Project); into != "" {
				comment = "Deepward closed this request: project `" + mk.Project + "` is now part of project `" + into + "`, and its changes are proposed in that project's requests."
			}
		}
		if comment == "" {
			if planned[b] || !scanned[mk.Project] || blocked[mk.Project+"\x00"+mk.Group] || plan.excluded[mk.Project+"\x00"+mk.Group] {
				continue
			}
			comment = closeComment(mk, carriedBy)
		}
		if err := client.Comment(ctx, cr.ID, comment); err != nil {
			return res, err
		}
		if err := client.CloseChangeRequest(ctx, cr.ID); err != nil {
			return res, err
		}
		res.Closed = append(res.Closed, cr)
	}
	return res, nil
}

// mergedInto returns the scanned project that now contains the directory of
// a project that is no longer detected: a project of the same ecosystem
// whose directory is at or above it and that lists a manifest inside it.
// It returns "" otherwise, so requests of projects that failed, were
// excluded or were removed stay open.
func mergedInto(res *sdk.ScanResult, projectID string) string {
	if res == nil {
		return ""
	}
	eco, dir, ok := strings.Cut(projectID, ":")
	if !ok || dir == "." || dir == "" {
		return ""
	}
	for _, p := range res.Projects {
		if p.Status != sdk.ProjectOK || p.Ecosystem != eco || p.ID == projectID {
			continue
		}
		if p.Dir != "." && dir != p.Dir && !strings.HasPrefix(dir, p.Dir+"/") {
			continue
		}
		for _, m := range p.Manifests {
			if strings.HasPrefix(m, dir+"/") {
				return p.ID
			}
		}
	}
	return ""
}

// closeComment explains why a request is closed: its changes moved to other
// requests (after a grouping change, or merged into a security fix), or they
// are no longer needed.
func closeComment(mk Marker, carriedBy map[string]string) string {
	var titles []string
	seen := map[string]bool{}
	for _, item := range mk.Items {
		t, ok := carriedBy[mk.Project+"\x00"+item]
		if !ok {
			titles = nil
			break
		}
		if !seen[t] {
			seen[t] = true
			titles = append(titles, `"`+t+`"`)
		}
	}
	if len(titles) > 0 {
		return "Deepward closed this request: its changes are now part of " + strings.Join(titles, ", ") + "."
	}
	return "Deepward closed this request: the update is no longer needed (the target branch already has it, or the finding is gone)."
}

func human(commits []sdk.CommitInfo) bool {
	for _, c := range commits {
		if !c.IsBot {
			return true
		}
	}
	return false
}

func commitMessage(cs ChangeSet) string {
	var b strings.Builder
	if cs.CustomTitle {
		b.WriteString(cs.Title)
	} else {
		b.WriteString(strings.TrimPrefix(cs.Title, "Deepward: "))
	}
	b.WriteString("\n\n")
	for _, it := range cs.Items {
		if it.Text {
			fmt.Fprintf(&b, "- %s\n", it.Package)
		} else {
			fmt.Fprintf(&b, "- %s %s -> %s\n", packageName(it.Package), it.From, it.To)
		}
	}
	b.WriteString("\nGenerated by Deepward.\n")
	return b.String()
}
