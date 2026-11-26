package domain

import (
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// CVSS3Score computes the CVSS v3.x base score of a vector such as
// "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H".
func CVSS3Score(vector string) (float64, error) {
	parts := strings.Split(vector, "/")
	if len(parts) < 9 || !strings.HasPrefix(parts[0], "CVSS:3") {
		return 0, errors.New("not a CVSS v3 vector")
	}
	m := map[string]string{}
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			m[k] = v
		}
	}
	weights := map[string]map[string]float64{
		"AV": {"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2},
		"AC": {"L": 0.77, "H": 0.44},
		"UI": {"N": 0.85, "R": 0.62},
		"C":  {"H": 0.56, "L": 0.22, "N": 0},
		"I":  {"H": 0.56, "L": 0.22, "N": 0},
		"A":  {"H": 0.56, "L": 0.22, "N": 0},
	}
	val := map[string]float64{}
	for k, table := range weights {
		w, ok := table[m[k]]
		if !ok {
			return 0, errors.New("invalid CVSS v3 metric " + k)
		}
		val[k] = w
	}
	scope := m["S"]
	if scope != "U" && scope != "C" {
		return 0, errors.New("invalid CVSS v3 metric S")
	}
	pr := map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	if scope == "C" {
		pr = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.5}
	}
	prv, ok := pr[m["PR"]]
	if !ok {
		return 0, errors.New("invalid CVSS v3 metric PR")
	}
	iss := 1 - (1-val["C"])*(1-val["I"])*(1-val["A"])
	var impact float64
	if scope == "U" {
		impact = 6.42 * iss
	} else {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	exploit := 8.22 * val["AV"] * val["AC"] * prv * val["UI"]
	if impact <= 0 {
		return 0, nil
	}
	if scope == "U" {
		return roundUp(math.Min(impact+exploit, 10)), nil
	}
	return roundUp(math.Min(1.08*(impact+exploit), 10)), nil
}

// roundUp implements the CVSS v3.1 Roundup function.
func roundUp(x float64) float64 {
	i := int64(math.Round(x * 100000))
	if i%10000 == 0 {
		return float64(i) / 100000
	}
	return (math.Floor(float64(i)/10000) + 1) / 10
}

// Rating is the outcome of the severity policy.
type Rating struct {
	Severity sdk.Severity
	Score    float64
	Vector   string
	Version  string // "3" or "4"
}

// Rate applies the severity policy: CVSS v3/v4 base scores first, then the
// sources' textual ratings; when sources disagree the highest wins.
func Rate(advs []deps.Advisory) Rating {
	best := Rating{Severity: sdk.SeverityUnknown}
	found := false
	consider := func(r Rating) {
		if !found || r.Severity.Rank() > best.Severity.Rank() || (r.Severity == best.Severity && r.Score > best.Score) {
			best, found = r, true
		}
	}
	scored := false
	for _, a := range advs {
		for _, s := range a.Severity {
			score := s.Value
			version := "3"
			switch {
			case strings.HasPrefix(s.Score, "CVSS:4") || s.Type == "CVSS_V4":
				version = "4"
			case score == 0:
				if v, err := CVSS3Score(s.Score); err == nil {
					score = v
				}
			}
			if score > 0 {
				scored = true
				consider(Rating{Severity: sdk.SeverityFromCVSS(score), Score: score, Vector: s.Score, Version: version})
			}
		}
	}
	if !scored {
		for _, a := range advs {
			if sev, err := sdk.ParseSeverity(a.DatabaseSeverity); err == nil && a.DatabaseSeverity != "" {
				consider(Rating{Severity: sev})
			}
		}
	}
	return best
}

// Group is a set of advisories describing the same vulnerability.
type Group struct {
	Advisories []deps.Advisory
	// IDs are all identifiers and aliases, sorted.
	IDs []string
}

// Primary returns the preferred rule ID: a CVE, then a GHSA, then the first ID.
func (g Group) Primary() string {
	for _, prefix := range []string{"CVE-", "GHSA-"} {
		for _, id := range g.IDs {
			if strings.HasPrefix(id, prefix) {
				return id
			}
		}
	}
	return g.IDs[0]
}

// Aliases returns the IDs except the primary one.
func (g Group) Aliases() []string {
	p := g.Primary()
	var out []string
	for _, id := range g.IDs {
		if id != p {
			out = append(out, id)
		}
	}
	return out
}

// Sources returns the advisory source IDs, sorted and unique.
func (g Group) Sources() []string {
	set := map[string]bool{}
	for _, a := range g.Advisories {
		set[a.Source] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Deduplicate merges advisories that share an ID or alias.
func Deduplicate(advs []deps.Advisory) []Group {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			parent[x] = x
			return x
		}
		r := find(parent[x])
		parent[x] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			if ra < rb {
				parent[rb] = ra
			} else {
				parent[ra] = rb
			}
		}
	}
	for _, a := range advs {
		find(a.ID)
		for _, al := range a.Aliases {
			union(a.ID, al)
		}
	}
	groups := map[string]*Group{}
	var roots []string
	for _, a := range advs {
		r := find(a.ID)
		g, ok := groups[r]
		if !ok {
			g = &Group{}
			groups[r] = g
			roots = append(roots, r)
		}
		g.Advisories = append(g.Advisories, a)
	}
	sort.Strings(roots)
	out := make([]Group, 0, len(roots))
	for _, r := range roots {
		g := groups[r]
		set := map[string]bool{}
		for _, a := range g.Advisories {
			set[a.ID] = true
			for _, al := range a.Aliases {
				set[al] = true
			}
		}
		for id := range set {
			g.IDs = append(g.IDs, id)
		}
		sort.Strings(g.IDs)
		out = append(out, *g)
	}
	return out
}
