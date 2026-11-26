package domain

import (
	"os"
	"sort"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../testdata/cargoapp/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecordedWorkspace(t *testing.T) {
	lock, err := ParseLock(fixture(t, "Cargo.lock"))
	if err != nil || lock.Version != 4 {
		t.Fatalf("lock: %v", err)
	}
	app, err := ParseManifest("app/Cargo.toml", fixture(t, "app/Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	util, _ := ParseManifest("util/Cargo.toml", fixture(t, "util/Cargo.toml"))
	root, _ := ParseManifest("Cargo.toml", fixture(t, "Cargo.toml"))
	if !root.IsWorkspace || root.HasPackageSection || strings.Join(root.Members, ",") != "app,util" || root.WorkspaceDeps["itoa"].Req != "1" {
		t.Errorf("root: %+v", root)
	}
	if !util.Deps[0].Workspace || util.Deps[0].Package != "itoa" {
		t.Errorf("util: %+v", util.Deps)
	}
	g, err := lock.Graph("project:cargo:.", "Cargo.lock", map[string]map[string]Kind{"app": app.Kinds(), "util": util.Kinds()})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, n := range g.Packages() {
		lines = append(lines, n.ID+" "+n.Scope+map[bool]string{true: " direct"}[n.Direct])
	}
	sort.Strings(lines)
	want := `pkg:cargo/aho-corasick@0.7.20 compile
pkg:cargo/hex@0.4.3 development direct
pkg:cargo/itoa@1.0.19 compile direct
pkg:cargo/memchr@2.8.3 compile
pkg:cargo/regex-syntax@0.6.29 compile
pkg:cargo/regex@1.5.4 compile direct
pkg:cargo/smallvec@1.6.0 compile direct
pkg:cargo/version_check@0.9.5 build direct`
	if got := strings.Join(lines, "\n"); got != want {
		t.Errorf("graph:\n%s", got)
	}
	if got := lock.Instances("regex"); len(got) != 1 || got[0] != "1.5.4" {
		t.Errorf("instances: %v", got)
	}
	if paths := g.Paths("pkg:cargo/memchr@2.8.3", 0); len(paths) != 2 {
		t.Errorf("memchr paths: %v", paths)
	}
}

func TestLockDetails(t *testing.T) {
	lock := `version = 3
[[package]]
name = "app"
version = "0.1.0"
dependencies = ["syn 2.0.48", "syn 1.0.109 (registry+https://github.com/rust-lang/crates.io-index)", "forked", "", "ghost"]

[[package]]
name = "syn"
version = "1.0.109"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "syn"
version = "2.0.48"
source = "sparse+https://index.crates.io/"
dependencies = ["proc-macro2"]

[[package]]
name = "proc-macro2"
version = "1.0.78"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "forked"
version = "0.3.0"
source = "git+https://github.com/me/forked#abc"
dependencies = ["proc-macro2"]
`
	l, err := ParseLock([]byte(lock))
	if err != nil {
		t.Fatal(err)
	}
	g, err := l.Graph("r", "s", nil)
	if err != nil {
		t.Fatal(err)
	}
	var edges []string
	for _, e := range g.Edges {
		edges = append(edges, e.From+" -> "+e.To)
	}
	want := "pkg:cargo/syn@2.0.48 -> pkg:cargo/proc-macro2@1.0.78|r -> pkg:cargo/proc-macro2@1.0.78|r -> pkg:cargo/syn@1.0.109|r -> pkg:cargo/syn@2.0.48"
	if got := strings.Join(edges, "|"); got != want {
		t.Errorf("edges: %s", got)
	}
	if l.resolve("syn 1.0.109 (other)") != -1 {
		t.Error("source must match")
	}
	if _, err := ParseLock([]byte("[[package")); err == nil {
		t.Error("broken lock")
	}
}

func TestManifestVariants(t *testing.T) {
	m, err := ParseManifest("Cargo.toml", []byte(`
[package]
name = "x"

[workspace]
exclude = ["examples/", "fuzz"]

[dependencies]
serde = { version = "1.0", features = ["derive"] }
rand_alias = { package = "rand", version = "0.8" }
local = { path = "../local" }
fork = { git = "https://example/fork" }

[dev-dependencies]
serde = "1.0.100"

[build_dependencies]
cc = "1"

[target.'cfg(windows)'.dependencies]
winapi = "0.3"

[target.'cfg(unix)'.dev-dependencies]
nix = "0.27"

[target.bad]
x = 1
`))
	if err != nil {
		t.Fatal(err)
	}
	k := m.Kinds()
	if k["serde"] != KindNormal || k["rand"] != KindNormal || k["cc"] != KindBuild || k["winapi"] != KindNormal || k["nix"] != KindDev {
		t.Errorf("kinds: %v", k)
	}
	if d := m.Declarations("rand"); len(d) != 1 || d[0].Key != "rand_alias" || d[0].Req != "0.8" {
		t.Errorf("rename: %+v", d)
	}
	if d := m.Declarations("local"); !d[0].Path || m.Declarations("fork")[0].Git != true {
		t.Error("path/git")
	}
	if !m.Excluded("examples/a") || !m.Excluded("fuzz") || m.Excluded("src") {
		t.Error("exclude")
	}
	if _, err := ParseManifest("Cargo.toml", []byte("[package")); err == nil {
		t.Error("broken manifest")
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		req, v string
		want   bool
	}{
		{"1.5", "1.5.4", true}, {"1.5", "1.9.0", true}, {"1.5", "2.0.0", false}, {"1.5", "1.4.9", false},
		{"^0.3", "0.3.9", true}, {"^0.3", "0.4.0", false}, {"^0.0.3", "0.0.3", true}, {"^0.0.3", "0.0.4", false},
		{"^0.0", "0.0.9", true}, {"^0.0", "0.1.0", false}, {"0", "0.9.0", true}, {"^1.2.3", "1.9.9", true},
		{"~1.2.3", "1.2.9", true}, {"~1.2.3", "1.3.0", false}, {"~1", "1.9.0", true}, {"~1", "2.0.0", false},
		{"=1.6.0", "1.6.0", true}, {"=1.6.0", "1.6.1", false}, {"=1.6", "1.6.9", true}, {"=1", "1.9.0", true},
		{">=1, <2", "1.5.0", true}, {">=1, <2", "2.0.0", false}, {">1.2", "1.2.9", false}, {">1.2", "1.3.0", true},
		{"<=1.2", "1.2.9", true}, {"<=1.2", "1.3.0", false}, {"*", "9.9.9", true}, {"1.*", "1.9.0", true},
		{"1.*", "2.0.0", false}, {"1.2.x", "1.2.7", true}, {"1.2.*", "1.3.0", false}, {">1.2.3", "1.2.4", true},
		{"=1.0.0-rc.1", "1.0.0-rc.1", true}, {">1", "2.0.0", true},
	}
	for _, c := range cases {
		got, err := Satisfies(c.req, c.v)
		if err != nil || got != c.want {
			t.Errorf("%q %s = %v %v", c.req, c.v, got, err)
		}
	}
	if _, err := Satisfies("garbage", "1.0.0"); err == nil {
		t.Error("bad requirement")
	}
	if _, err := Satisfies("1", ""); err == nil {
		t.Error("bad version")
	}
}

func TestRewriteReq(t *testing.T) {
	ok := map[string]string{"1.5": "1.6.1", "^1.5": "^1.6.1", "~1.5.0": "~1.6.1", "=1.6.0": "=1.6.1", "	1": "1.6.1"}
	for in, want := range ok {
		if got, err := RewriteReq(in, "1.6.1"); err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	for _, bad := range []string{">=1, <2", "*", "1.*", ">=1.0", "<2", "nonsense"} {
		if _, err := RewriteReq(bad, "1.6.1"); err == nil {
			t.Errorf("%q must not be rewritten", bad)
		}
	}
}

func TestFindRequirements(t *testing.T) {
	src := `[package]
name = "x"
version = "0.1.0"

[dependencies]
regex = "1.5"
serde = { version = "1.0", features = ["derive"] }
"quoted" = '2.0'
dotted.version = "3"
plain = { path = "../plain" }
# regex = "0.1"
broken = "unterminated

[dependencies.tokio]
features = ["full"]
version = "1.36"

[target.'cfg(windows)'.dependencies]
regex = "1.5"

[workspace.dependencies]
itoa = { workspace = false, version="1" }

[[bin]]
name = "regex"

[profile.release]
regex = "not a dep"
`
	find := func(key string) string {
		var out []string
		for _, s := range FindRequirements([]byte(src), key) {
			if src[s.Start:s.End] != s.Value {
				t.Errorf("%s: span mismatch", key)
			}
			out = append(out, s.Value)
		}
		return strings.Join(out, ",")
	}
	for key, want := range map[string]string{"regex": "1.5,1.5", "serde": "1.0", "quoted": "2.0", "dotted": "3", "plain": "", "tokio": "1.36", "itoa": "1", "broken": "", "name": ""} {
		if got := find(key); got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
	if h, ok := headerName(`[target."x86.64".dependencies]`); !ok || h != `target."x86.64".dependencies` {
		t.Errorf("header: %s", h)
	}
	if _, ok := headerName("[unterminated"); ok {
		t.Error("unterminated header")
	}
}
