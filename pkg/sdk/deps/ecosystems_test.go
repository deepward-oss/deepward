package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk/purl"
)

func TestNewEcosystems(t *testing.T) {
	for eco, typ := range map[Ecosystem]string{NPM: "npm", Go: "golang", CratesIO: "cargo"} {
		if eco.PURLType() != typ || !eco.SemVer() {
			t.Errorf("%s: %s", eco, eco.PURLType())
		}
		if got, err := EcosystemOf(typ); err != nil || got != eco {
			t.Errorf("EcosystemOf(%s) = %v %v", typ, got, err)
		}
	}
	if Maven.SemVer() || len(Ecosystems) != 5 {
		t.Error("Maven is not SemVer")
	}
}

func TestSemVerEcosystemVersions(t *testing.T) {
	g := MustParseVersion(Go, "0.3.8")
	if g.String() != "v0.3.8" || g.Compare(MustParseVersion(Go, "v0.3.8")) != 0 || !MustParseVersion(Go, "v0.3.7").Less(g) {
		t.Errorf("go canonical: %s", g)
	}
	pseudo := MustParseVersion(Go, "v0.0.0-20180917221912-90fa682c2a6e")
	if !pseudo.IsPrerelease() || !pseudo.Less(MustParseVersion(Go, "v0.1.0")) {
		t.Error("pseudo-version")
	}
	if MustParseVersion(Go, "v2.0.0+incompatible").IsPrerelease() {
		t.Error("+incompatible is build metadata")
	}
	if !MustParseVersion(NPM, "1.0.0-beta.2").IsPrerelease() || MustParseVersion(NPM, "1.0.0").IsPrerelease() {
		t.Error("npm prerelease")
	}
	if !MustParseVersion(CratesIO, "1.5.4").Less(MustParseVersion(CratesIO, "1.5.10")) {
		t.Error("crates order")
	}
	if ClassifyUpdate(MustParseVersion(Go, "v1.2.3"), MustParseVersion(Go, "v1.3.0")) != UpdateMinor {
		t.Error("go classify")
	}
}

func TestNames(t *testing.T) {
	cases := []struct {
		eco        Ecosystem
		name, purl string
	}{
		{NPM, "@sindresorhus/is", "pkg:npm/%40sindresorhus/is@4.0.1"},
		{NPM, "lodash", "pkg:npm/lodash@4.0.1"},
		{Go, "golang.org/x/text", "pkg:golang/golang.org/x/text@4.0.1"},
		{Go, "example", "pkg:golang/example@4.0.1"},
		{CratesIO, "regex", "pkg:cargo/regex@4.0.1"},
		{Maven, "g.x:a", "pkg:maven/g.x/a@4.0.1"},
		{Maven, "plain", "pkg:maven/plain@4.0.1"},
		{NuGet, "Newtonsoft.Json", "pkg:nuget/newtonsoft.json@4.0.1"},
	}
	for _, c := range cases {
		p := NewPURL(c.eco, c.name, "4.0.1")
		if p.String() != c.purl {
			t.Errorf("%s: %s", c.name, p)
		}
		back := PackageName(purl.MustParse(c.purl))
		if c.eco != NuGet && back != c.name {
			t.Errorf("PackageName(%s) = %s", c.purl, back)
		}
	}
	if AdvisoryVersion(Go, "v1.2.3") != "1.2.3" || AdvisoryVersion(NPM, "v1") != "v1" {
		t.Error("advisory version")
	}
}

func TestRegenerate(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"a":1}`), 0o644)
	_ = os.WriteFile(filepath.Join(root, "same.txt"), []byte("x"), 0o644)
	edits, err := Regenerate(context.Background(), root, []string{"package.json", "same.txt", "new.lock", "package.json"}, func(context.Context) error {
		if err := WriteFile(root, "package.json", []byte(`{"a":2}`)); err != nil {
			return err
		}
		return WriteFile(root, "new.lock", []byte("created"))
	})
	if err != nil || len(edits) != 1 || edits[0].Replacement != `{"a":2}` || edits[0].Expected != `{"a":1}` || edits[0].End != 7 {
		t.Fatalf("edits: %+v %v", edits, err)
	}
	if b, _ := ReadFile(root, "package.json"); string(b) != `{"a":1}` {
		t.Errorf("not restored: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "new.lock")); !os.IsNotExist(err) {
		t.Error("created file must be removed")
	}
	boom := errors.New("boom")
	if _, err := Regenerate(context.Background(), root, []string{"package.json"}, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Error("callback error")
	}
	if _, err := Regenerate(context.Background(), root, []string{"package.json"}, func(context.Context) error {
		return os.Remove(filepath.Join(root, "package.json"))
	}); err == nil {
		t.Error("deleted file")
	}
	if b, _ := ReadFile(root, "package.json"); string(b) != `{"a":1}` {
		t.Error("deleted file restored")
	}
	_ = os.Mkdir(filepath.Join(root, "dir"), 0o755)
	if _, err := Regenerate(context.Background(), root, []string{"dir"}, func(context.Context) error { return nil }); err == nil {
		t.Error("unreadable snapshot")
	}
}

// TestPathsScale builds a layered graph with 2^20 root-to-leaf chains and
// checks that limited path search and ancestor lookup stay fast.
func TestPathsScale(t *testing.T) {
	const layers, width = 20, 2
	root := "project:x"
	nodes := []Node{{ID: root}}
	var edges []Edge
	id := func(l, i int) string { return fmt.Sprintf("pkg:npm/n%d-%d@1.0.0", l, i) }
	for l := 0; l < layers; l++ {
		for i := 0; i < width; i++ {
			nodes = append(nodes, Node{ID: id(l, i), Ecosystem: NPM, Name: fmt.Sprintf("n%d-%d", l, i), Version: "1.0.0"})
			if l == 0 {
				edges = append(edges, Edge{root, id(l, i)})
				continue
			}
			for j := 0; j < width; j++ {
				edges = append(edges, Edge{id(l-1, j), id(l, i)})
			}
		}
	}
	leaf := "pkg:npm/leaf@1.0.0"
	nodes = append(nodes, Node{ID: leaf, Ecosystem: NPM, Name: "leaf", Version: "1.0.0"})
	for j := 0; j < width; j++ {
		edges = append(edges, Edge{id(layers-1, j), leaf})
	}
	g, err := NewGraph(root, "test", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	paths := g.Paths(leaf, 5)
	if len(paths) != 5 || len(paths[0]) != layers+1 {
		t.Fatalf("paths: %d", len(paths))
	}
	if got := g.DirectAncestors(leaf); !reflect.DeepEqual(got, []string{id(0, 0), id(0, 1)}) {
		t.Errorf("ancestors: %v", got)
	}
	if all := g.Paths(leaf, 0); len(all) != maxPaths {
		t.Errorf("unlimited paths are capped: %d", len(all))
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("path search too slow: %s", time.Since(start))
	}
	orphan, _ := NewGraph(root, "test", []Node{{ID: root}, {ID: leaf, Version: "1"}}, nil)
	if orphan.Paths(leaf, 0) != nil || orphan.DirectAncestors(leaf) != nil || orphan.DirectAncestors(root) != nil {
		t.Error("unreachable node")
	}
	lazy := &Graph{Root: root, Nodes: []Node{{ID: root}, {ID: leaf, Version: "1"}}, Edges: []Edge{{root, leaf}}}
	if !reflect.DeepEqual(lazy.DirectAncestors(leaf), []string{leaf}) {
		t.Error("lazy ancestors")
	}
}
