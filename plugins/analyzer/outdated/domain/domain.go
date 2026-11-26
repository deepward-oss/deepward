// Package domain classifies available updates and applies update rules.
package domain

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// Rule configures updates for packages matching a purl pattern.
type Rule struct {
	// Match is a purl glob without version, e.g. "pkg:maven/org.springframework*/*".
	Match string `yaml:"match" json:"match"`
	// Ignore drops the package from reports and change proposals.
	Ignore bool `yaml:"ignore" json:"ignore,omitempty"`
	// MaxUpdate caps the update type: patch, minor or major.
	MaxUpdate deps.UpdateType `yaml:"maxUpdate" json:"maxUpdate,omitempty"`
	// Allow restricts versions to a range such as ">=2.0, <3".
	Allow string `yaml:"allow" json:"allow,omitempty"`
	// AllowPrerelease proposes prerelease versions.
	AllowPrerelease bool `yaml:"allowPrerelease" json:"allowPrerelease,omitempty"`
}

// Matches reports whether the rule applies to a package (purl without version).
func (r Rule) Matches(pkg string) bool {
	if r.Match == "" {
		return false
	}
	if strings.EqualFold(r.Match, pkg) {
		return true
	}
	ok, _ := path.Match(strings.ToLower(r.Match), strings.ToLower(pkg))
	return ok
}

// Policy is the merged effect of all matching rules.
type Policy struct {
	Ignore          bool
	MaxUpdate       deps.UpdateType
	Allow           []Range
	AllowPrerelease bool
}

// PolicyFor merges the rules matching pkg (later rules refine earlier ones).
func PolicyFor(rules []Rule, pkg string) (Policy, error) {
	var p Policy
	for _, r := range rules {
		if !r.Matches(pkg) {
			continue
		}
		p.Ignore = p.Ignore || r.Ignore
		if r.MaxUpdate != deps.UpdateNone {
			if p.MaxUpdate == deps.UpdateNone || r.MaxUpdate.Rank() < p.MaxUpdate.Rank() {
				p.MaxUpdate = r.MaxUpdate
			}
		}
		if r.Allow != "" {
			rs, err := ParseRanges(r.Allow)
			if err != nil {
				return Policy{}, err
			}
			p.Allow = append(p.Allow, rs...)
		}
		p.AllowPrerelease = p.AllowPrerelease || r.AllowPrerelease
	}
	return p, nil
}

// Range is one version comparison.
type Range struct {
	Op string
	V  string
}

var rangePattern = regexp.MustCompile(`^(>=|<=|!=|=|>|<)\s*(\S+)$`)

// ParseRanges parses "op version, op version".
func ParseRanges(s string) ([]Range, error) {
	var out []Range
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		m := rangePattern.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Errorf("invalid version range %q (example: \">=2.0, <3\")", s)
		}
		out = append(out, Range{Op: m[1], V: m[2]})
	}
	return out, nil
}

func allowed(rs []Range, v deps.Version) bool {
	for _, r := range rs {
		rv, err := deps.ParseVersion(v.Ecosystem(), r.V)
		if err != nil {
			return false
		}
		c := v.Compare(rv)
		ok := false
		switch r.Op {
		case ">=":
			ok = c >= 0
		case ">":
			ok = c > 0
		case "<=":
			ok = c <= 0
		case "<":
			ok = c < 0
		case "!=":
			ok = c != 0
		default:
			ok = c == 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// Updates are the newest versions per update line.
type Updates struct {
	Patch, Minor, Major, Latest string
	// Type is the highest available update type.
	Type deps.UpdateType
}

// Any reports whether an update exists.
func (u Updates) Any() bool { return u.Latest != "" }

// Classify computes the available updates from published versions.
func Classify(current deps.Version, published []string, p Policy) Updates {
	cM, cm, cpatch, _ := current.Segments()
	var best struct{ patch, minor, major, latest *deps.Version }
	consider := func(slot **deps.Version, v deps.Version) {
		if *slot == nil || (*slot).Less(v) {
			vv := v
			*slot = &vv
		}
	}
	for _, raw := range published {
		v, err := deps.ParseVersion(current.Ecosystem(), raw)
		if err != nil || !current.Less(v) {
			continue
		}
		if v.IsPrerelease() && !p.AllowPrerelease {
			// A prerelease is only proposed for the same release line when the
			// current version is itself a prerelease.
			vM, vm, vp, _ := v.Segments()
			if !current.IsPrerelease() || vM != cM || vm != cm || vp != cpatch {
				continue
			}
		}
		if len(p.Allow) > 0 && !allowed(p.Allow, v) {
			continue
		}
		t := deps.ClassifyUpdate(current, v)
		if p.MaxUpdate != deps.UpdateNone && t.Rank() > p.MaxUpdate.Rank() {
			continue
		}
		switch t {
		case deps.UpdatePatch:
			consider(&best.patch, v)
		case deps.UpdateMinor:
			consider(&best.minor, v)
		case deps.UpdateMajor:
			consider(&best.major, v)
		}
		consider(&best.latest, v)
	}
	var u Updates
	str := func(v *deps.Version) string {
		if v == nil {
			return ""
		}
		return v.String()
	}
	u.Patch, u.Minor, u.Major, u.Latest = str(best.patch), str(best.minor), str(best.major), str(best.latest)
	switch {
	case u.Major != "":
		u.Type = deps.UpdateMajor
	case u.Minor != "":
		u.Type = deps.UpdateMinor
	case u.Patch != "":
		u.Type = deps.UpdatePatch
	}
	return u
}
