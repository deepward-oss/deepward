// Package depstest provides fakes and a conformance check for the
// dependency SDK contracts (advisory sources and version registries).
package depstest

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

// FakeSource returns fixed advisories for matching package names.
type FakeSource struct {
	ID         string
	License    string
	Advisories []deps.Advisory
	Err        error
	Offline    bool
	HasData    bool
	Fresh      *time.Time
	Calls      atomic.Int64
}

// Info implements sdk.Plugin.
func (s *FakeSource) Info() sdk.Info {
	return sdk.Info{ID: s.ID, Kind: deps.KindAdvisorySource, Version: "0.0.0-test", Description: "fake advisory source"}
}

// DataLicense implements deps.AdvisorySource.
func (s *FakeSource) DataLicense() deps.DataLicense {
	return deps.DataLicense{SPDX: s.License, Attribution: "fake data"}
}

// Freshness implements deps.AdvisorySource.
func (s *FakeSource) Freshness() *time.Time { return s.Fresh }

// Query implements deps.AdvisorySource.
func (s *FakeSource) Query(ctx context.Context, qs []deps.PackageQuery) ([]deps.Advisory, error) {
	s.Calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Err != nil {
		return nil, s.Err
	}
	names := map[string]bool{}
	for _, q := range qs {
		names[strings.ToLower(q.Name)] = true
	}
	var out []deps.Advisory
	for _, a := range s.Advisories {
		for _, af := range a.Affected {
			if names[strings.ToLower(af.Name)] {
				if a.Source == "" {
					a.Source = s.ID
				}
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}

// fakeOffline adds deps.OfflineSource.
type fakeOffline struct{ *FakeSource }

// Available implements deps.OfflineSource.
func (f fakeOffline) Available() bool { return f.HasData }

// AsOffline wraps the source so it implements deps.OfflineSource.
func (s *FakeSource) AsOffline() deps.AdvisorySource { return fakeOffline{s} }

// FakeRegistry serves version lists keyed by purl (without version).
type FakeRegistry struct {
	Versions map[string][]string
	Err      error
}

// List implements the lookup used by FakeEcosystem.
func (r *FakeRegistry) List(_ context.Context, _ sdk.Project, pkg string) ([]string, error) {
	if r.Err != nil {
		return nil, r.Err
	}
	v, ok := r.Versions[pkg]
	if !ok {
		return nil, deps.ErrPackageNotFound
	}
	return v, nil
}

// Ecosystem is a fake ecosystem that produces a fixed graph and implements
// deps.VersionRegistry.
type Ecosystem struct {
	sdktest.FakeEcosystem
	Registry *FakeRegistry
}

// Versions implements deps.VersionRegistry.
func (e *Ecosystem) Versions(ctx context.Context, p sdk.Project, pkg string) ([]string, error) {
	if e.Registry == nil {
		return nil, deps.ErrPackageNotFound
	}
	return e.Registry.List(ctx, p, pkg)
}

// ListingEcosystem is an Ecosystem that also implements deps.DependencyLister.
type ListingEcosystem struct {
	Ecosystem
	// Deps maps "purl@version" (purl without version) to the dependencies
	// that version declares.
	Deps map[string]map[string]string
	// Calls counts Dependencies calls.
	Calls atomic.Int64
}

// Dependencies implements deps.DependencyLister.
func (e *ListingEcosystem) Dependencies(ctx context.Context, _ sdk.Project, pkg, version string) (map[string]string, error) {
	e.Calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, ok := e.Deps[pkg+"@"+version]
	if !ok {
		return nil, deps.ErrPackageNotFound
	}
	return d, nil
}

// TestAdvisorySource checks an advisory source with a query that should
// return results.
func TestAdvisorySource(t *testing.T, s deps.AdvisorySource, queries []deps.PackageQuery) []deps.Advisory {
	t.Helper()
	sdktest.TestPlugin(t, s)
	if s.Info().Kind != deps.KindAdvisorySource {
		t.Errorf("kind = %s", s.Info().Kind)
	}
	if s.DataLicense().SPDX == "" {
		t.Error("advisory sources must declare a data license")
	}
	ctx := context.Background()
	first, err := s.Query(ctx, queries)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	second, _ := s.Query(ctx, queries)
	sortAdv := func(a []deps.Advisory) { sort.Slice(a, func(i, j int) bool { return a[i].ID < a[j].ID }) }
	sortAdv(first)
	sortAdv(second)
	if !reflect.DeepEqual(first, second) {
		t.Error("Query is not deterministic")
	}
	for _, a := range first {
		if a.ID == "" || a.Source == "" {
			t.Errorf("advisory without ID or source: %+v", a)
		}
	}
	if res, err := s.Query(ctx, nil); err == nil && len(res) != 0 {
		t.Error("empty query must return no advisories")
	}
	return first
}
