// Package domain is the pure logic of the dependency-vulnerabilities
// analyzer: OSV range matching, severity policy, deduplication and fixed
// version selection.
package domain

import (
	"sort"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// SameName reports whether an advisory package name refers to pkg in eco
// (NuGet IDs are case-insensitive).
func SameName(eco deps.Ecosystem, a, b string) bool {
	if eco == deps.NuGet {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// AffectedEntries returns the affected entries of adv for a package.
func AffectedEntries(adv deps.Advisory, eco deps.Ecosystem, name string) []deps.Affected {
	var out []deps.Affected
	for _, a := range adv.Affected {
		if strings.EqualFold(string(a.Ecosystem), string(eco)) && SameName(eco, a.Name, name) {
			out = append(out, a)
		}
	}
	return out
}

// Affects reports whether version v of a package is affected by adv.
func Affects(adv deps.Advisory, eco deps.Ecosystem, name string, v deps.Version) bool {
	for _, a := range AffectedEntries(adv, eco, name) {
		if affectedBy(a, eco, v) {
			return true
		}
	}
	return false
}

func affectedBy(a deps.Affected, eco deps.Ecosystem, v deps.Version) bool {
	for _, explicit := range a.Versions {
		if ev, err := deps.ParseVersion(eco, explicit); err == nil && ev.Compare(v) == 0 {
			return true
		}
	}
	for _, r := range a.Ranges {
		if r.Type != "ECOSYSTEM" && r.Type != "SEMVER" {
			continue
		}
		if inRange(r, eco, v) {
			return true
		}
	}
	return false
}

type event struct {
	kind string
	v    deps.Version
	zero bool
}

// inRange implements the OSV evaluation algorithm: events are sorted by
// version and applied in order.
func inRange(r deps.Range, eco deps.Ecosystem, v deps.Version) bool {
	var events []event
	for _, e := range r.Events {
		var kind, raw string
		switch {
		case e.Introduced != "":
			kind, raw = "introduced", e.Introduced
		case e.Fixed != "":
			kind, raw = "fixed", e.Fixed
		case e.LastAffected != "":
			kind, raw = "last_affected", e.LastAffected
		case e.Limit != "":
			kind, raw = "limit", e.Limit
		default:
			continue
		}
		if raw == "0" && kind == "introduced" {
			events = append(events, event{kind: kind, zero: true})
			continue
		}
		pv, err := deps.ParseVersion(eco, raw)
		if err != nil {
			continue
		}
		events = append(events, event{kind: kind, v: pv})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].zero != events[j].zero {
			return events[i].zero
		}
		return events[i].v.Less(events[j].v)
	})
	affected := false
	for _, e := range events {
		switch e.kind {
		case "introduced":
			if e.zero || v.Compare(e.v) >= 0 {
				affected = true
			}
		case "fixed":
			if v.Compare(e.v) >= 0 {
				affected = false
			}
		case "last_affected":
			if v.Compare(e.v) > 0 {
				affected = false
			}
		case "limit":
			if v.Compare(e.v) >= 0 {
				affected = false
			}
		}
	}
	return affected
}

// FixedVersion returns the lowest version above current that is affected by
// none of advs, chosen among the advisories' "fixed" events and the extra
// candidates (e.g. published versions). ok is false when there is none.
func FixedVersion(advs []deps.Advisory, eco deps.Ecosystem, name string, current deps.Version, extra []string) (deps.Version, bool) {
	var cands []deps.Version
	add := func(raw string) {
		if v, err := deps.ParseVersion(eco, raw); err == nil && current.Less(v) {
			cands = append(cands, v)
		}
	}
	for _, adv := range advs {
		for _, a := range AffectedEntries(adv, eco, name) {
			for _, r := range a.Ranges {
				for _, e := range r.Events {
					if e.Fixed != "" {
						add(e.Fixed)
					}
				}
			}
		}
	}
	for _, e := range extra {
		add(e)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Less(cands[j]) })
	for _, c := range cands {
		if !current.IsPrerelease() && c.IsPrerelease() {
			continue
		}
		clean := true
		for _, adv := range advs {
			if Affects(adv, eco, name, c) {
				clean = false
				break
			}
		}
		if clean {
			return c, true
		}
	}
	return deps.Version{}, false
}
