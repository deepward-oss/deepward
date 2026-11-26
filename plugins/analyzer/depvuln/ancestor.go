package depvuln

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/plugins/analyzer/depvuln/domain"
)

// maxProbedAncestors bounds the direct ancestors checked per package.
const maxProbedAncestors = 3

// maxProbes bounds the ancestor versions whose dependencies are read while
// looking for the oldest one that brings a fix.
const maxProbes = 24

// outcome is what checking the newer versions of a direct ancestor found.
type outcome int

const (
	// outcomeUnknown: the dependencies of newer versions could not be read
	// (the ecosystem cannot list them, or a POM along the chain is missing).
	outcomeUnknown outcome = iota
	// outcomeFound: a newer ancestor version brings a version of the
	// package that none of its advisories affect.
	outcomeFound
	// outcomeNoFix: even the newest ancestor version brings an affected version.
	outcomeNoFix
	// outcomeNoNewer: the ancestor is already at its newest release.
	outcomeNoNewer
	// outcomeUnavailable: the ancestor's versions are not in any configured registry.
	outcomeUnavailable
)

// probe is the result of checking one direct ancestor.
type probe struct {
	outcome  outcome
	ancestor deps.Node
	// to is the oldest ancestor version that brings a fixed package
	// (outcomeFound), or the newest one (outcomeNoFix).
	to string
	// brings is the package version that `to` brings.
	brings string
	err    error
}

// upgradeAncestor checks the closest direct ancestors of a vulnerable
// transitive package and returns the first one with a newer version that
// brings a version no advisory affects; otherwise the result for the closest
// ancestor, which explains why there is none.
func upgradeAncestor(ctx context.Context, project sdk.Project, g *deps.Graph, registry deps.VersionRegistry, n deps.Node,
	ancestors []string, matched []deps.Advisory) probe {
	var first *probe
	for i, id := range ancestors {
		if i == maxProbedAncestors {
			break
		}
		anc, ok := g.Node(id)
		if !ok {
			continue
		}
		p := probeAncestor(ctx, project, g, registry, anc, n, matched)
		if p.outcome == outcomeFound {
			return p
		}
		if first == nil {
			first = &p
		}
	}
	if first == nil {
		return probe{}
	}
	return *first
}

func probeAncestor(ctx context.Context, project sdk.Project, g *deps.Graph, registry deps.VersionRegistry, anc, n deps.Node, matched []deps.Advisory) probe {
	res := probe{ancestor: anc}
	lister, ok := registry.(deps.DependencyLister)
	if !ok {
		return res
	}
	var chain []deps.Node
	for _, id := range g.Chain(anc.ID, n.ID) {
		if node, ok := g.Node(id); ok {
			chain = append(chain, node)
		}
	}
	if len(chain) < 2 {
		return res
	}
	cur, err := deps.ParseVersion(anc.Ecosystem, anc.Version)
	if err != nil {
		return res
	}
	published, err := registry.Versions(ctx, project, withoutVersion(anc.ID))
	if err != nil {
		res.outcome, res.err = outcomeUnavailable, err
		return res
	}
	var newer []deps.Version
	for _, raw := range published {
		if v, err := deps.ParseVersion(anc.Ecosystem, raw); err == nil && cur.Less(v) && !v.IsPrerelease() {
			newer = append(newer, v)
		}
	}
	if len(newer) == 0 {
		res.outcome = outcomeNoNewer
		return res
	}
	sort.Slice(newer, func(i, j int) bool { return newer[i].Less(newer[j]) })
	// brings follows the chain from the ancestor at version v down to the
	// package and returns the package version it asks for ("" if unknown).
	brings := func(v string) string {
		ver := v
		for i := 1; i < len(chain); i++ {
			ds, err := lister.Dependencies(ctx, project, withoutVersion(chain[i-1].ID), ver)
			if err != nil {
				return ""
			}
			if ver = ds[chain[i].Name]; ver == "" {
				return ""
			}
		}
		return ver
	}
	fixed := func(v string) bool {
		pv, err := deps.ParseVersion(n.Ecosystem, v)
		if err != nil {
			return false
		}
		for _, adv := range matched {
			if domain.Affects(adv, n.Ecosystem, n.Name, pv) {
				return false
			}
		}
		return true
	}
	// The newest release decides whether a fix exists at all; the oldest
	// fixing release is then the least disruptive upgrade.
	latest := newer[len(newer)-1].String()
	b := brings(latest)
	switch {
	case b == "":
		return res
	case !fixed(b):
		res.outcome, res.to, res.brings = outcomeNoFix, latest, b
		return res
	}
	res.outcome, res.to, res.brings = outcomeFound, latest, b
	for i, v := range newer[:len(newer)-1] {
		if i == maxProbes || ctx.Err() != nil {
			break
		}
		if b := brings(v.String()); b != "" && fixed(b) {
			res.to, res.brings = v.String(), b
			break
		}
	}
	return res
}

// probeRecommendation explains the probe in the recommended action.
func probeRecommendation(p probe, n deps.Node, fixed deps.Version, ecosystem string) string {
	anc := p.ancestor
	pin := fmt.Sprintf("pin %s to %s", n.Name, fixed)
	switch p.outcome {
	case outcomeFound:
		return fmt.Sprintf("upgrade %s from %s to %s: it brings %s %s, which fixes these advisories (or %s)",
			anc.Name, anc.Version, p.to, n.Name, p.brings, pin)
	case outcomeNoFix:
		return fmt.Sprintf("%s: no released version of %s brings a fixed %s yet (the newest, %s, brings %s)",
			capitalize(pin), anc.Name, n.Name, p.to, p.brings)
	case outcomeNoNewer:
		return fmt.Sprintf("%s: %s %s is the newest release of %s and brings the affected version",
			capitalize(pin), anc.Name, anc.Version, anc.Name)
	case outcomeUnavailable:
		why := fmt.Sprintf("the repositories could not be read (%v)", p.err)
		if errors.Is(p.err, deps.ErrPackageNotFound) {
			why = fmt.Sprintf("no configured repository has it; add the repository that publishes it under plugins.%s.repositories", ecosystem)
		}
		return fmt.Sprintf("%s: deepward could not check newer versions of %s because %s", capitalize(pin), anc.Name, why)
	}
	return fmt.Sprintf("upgrade %s to a version that brings %s ≥ %s (deepward fix tries newer versions and verifies them), or %s",
		anc.Name, n.Name, fixed, pin)
}

func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
