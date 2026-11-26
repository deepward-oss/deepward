package domain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func read(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func manifests(t *testing.T, variant string, dirs ...string) map[string]*Manifest {
	t.Helper()
	out := map[string]*Manifest{}
	for _, d := range dirs {
		m, err := ParseManifest(d+"/package.json", read(t, variant, d, "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		out[d] = m
	}
	return out
}

// summary renders a graph as sorted "id scope direct" lines plus edges.
func summary(t *testing.T, tree *Tree) string {
	t.Helper()
	g, err := tree.Graph("project:npm:.", tree.Format)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, n := range g.Packages() {
		d := ""
		if n.Direct {
			d = " direct"
		}
		lines = append(lines, n.ID+" "+n.Scope+d)
	}
	for _, e := range g.Edges {
		if e.From != g.Root {
			lines = append(lines, e.From+" -> "+e.To)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

const workspaceGraph = `pkg:npm/%40sindresorhus/is@4.0.1 runtime direct
pkg:npm/axios@0.21.4 -> pkg:npm/follow-redirects@1.16.3
pkg:npm/axios@0.21.4 runtime direct
pkg:npm/follow-redirects@1.16.3 runtime
pkg:npm/lodash@4.17.20 runtime direct
pkg:npm/minimist@1.2.5 development direct
pkg:npm/ms@2.1.3 runtime direct`

func TestRecordedLockfiles(t *testing.T) {
	cases := map[string]func(t *testing.T) (*Tree, error){
		"npm": func(t *testing.T) (*Tree, error) {
			return ParseNPMLock(read(t, "npm", "package-lock.json"), nil)
		},
		"yarn1": func(t *testing.T) (*Tree, error) {
			return ParseYarnLock(read(t, "yarn1", "yarn.lock"), manifests(t, "yarn1", ".", "packages/lib"))
		},
		"berry": func(t *testing.T) (*Tree, error) {
			return ParseYarnLock(read(t, "berry", "yarn.lock"), manifests(t, "berry", ".", "packages/lib"))
		},
		"pnpm9": func(t *testing.T) (*Tree, error) { return ParsePNPMLock(read(t, "pnpm9", "pnpm-lock.yaml")) },
		"pnpm8": func(t *testing.T) (*Tree, error) { return ParsePNPMLock(read(t, "pnpm8", "pnpm-lock.yaml")) },
		"pnpm7": func(t *testing.T) (*Tree, error) { return ParsePNPMLock(read(t, "pnpm7", "pnpm-lock.yaml")) },
	}
	for name, parse := range cases {
		t.Run(name, func(t *testing.T) {
			tree, err := parse(t)
			if err != nil {
				t.Fatal(err)
			}
			if got := summary(t, tree); got != workspaceGraph {
				t.Errorf("graph:\n%s\nwant:\n%s", got, workspaceGraph)
			}
			if len(tree.Importers) != 2 || tree.Importers[1].Dir != "packages/lib" {
				t.Errorf("importers: %+v", tree.Importers)
			}
			if got := tree.Instances("lodash"); len(got) != 1 || got[0] != "4.17.20" {
				t.Errorf("instances: %v", got)
			}
		})
	}
}

func TestNPMLockV1(t *testing.T) {
	root := manifests(t, "npm6", ".")["."]
	tree, err := ParseNPMLock(read(t, "npm6", "package-lock.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	want := `pkg:npm/axios@0.21.4 -> pkg:npm/follow-redirects@1.16.3
pkg:npm/axios@0.21.4 runtime direct
pkg:npm/follow-redirects@1.16.3 runtime
pkg:npm/lodash@4.17.20 runtime direct
pkg:npm/minimist@1.2.5 development direct`
	if got := summary(t, tree); got != want || tree.Format != "package-lock.json v1" {
		t.Errorf("graph:\n%s", got)
	}
}

func TestNPMNestedAndLinks(t *testing.T) {
	lock := `{"lockfileVersion": 3, "packages": {
  "": {"name": "app", "dependencies": {"a": "^1", "b": "^2", "local": "file:../local", "alias": "npm:real@^1"}, "devDependencies": {"a": "^1"}},
  "node_modules/a": {"version": "1.0.0", "dependencies": {"b": "^1", "missing-optional": "^1"}},
  "node_modules/a/node_modules/b": {"version": "1.4.0", "dependencies": {"c": "^1"}},
  "node_modules/b": {"version": "2.0.0", "dependencies": {"c": "^1"}},
  "node_modules/c": {"version": "1.1.0"},
  "node_modules/local": {"resolved": "../local", "link": true},
  "node_modules/broken": {"resolved": "nowhere", "link": true},
  "node_modules/alias": {"name": "real", "version": "1.2.0"},
  "node_modules/tar": {"version": "file:vendor/tar.tgz"}
}}`
	tree, err := ParseNPMLock([]byte(lock), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `pkg:npm/a@1.0.0 -> pkg:npm/b@1.4.0
pkg:npm/a@1.0.0 runtime direct
pkg:npm/b@1.4.0 -> pkg:npm/c@1.1.0
pkg:npm/b@1.4.0 runtime
pkg:npm/b@2.0.0 -> pkg:npm/c@1.1.0
pkg:npm/b@2.0.0 runtime direct
pkg:npm/c@1.1.0 runtime
pkg:npm/real@1.2.0 runtime direct`
	if got := summary(t, tree); got != want {
		t.Errorf("graph:\n%s", got)
	}
	if got := tree.Instances("b"); strings.Join(got, ",") != "1.4.0,2.0.0" {
		t.Errorf("instances: %v", got)
	}
	if _, ok := resolveNPM(map[string]*npmEntry{"node_modules/x": {Link: true, Resolved: "y"}}, "", "x"); ok {
		t.Error("dangling link")
	}
}

func TestLockErrors(t *testing.T) {
	bad := map[string]func() error{
		"npm json":    func() error { _, err := ParseNPMLock([]byte("{"), nil); return err },
		"npm version": func() error { _, err := ParseNPMLock([]byte(`{"lockfileVersion": 7}`), nil); return err },
		"npm v1 deps": func() error {
			_, err := ParseNPMLock([]byte(`{"lockfileVersion": 1, "dependencies": []}`), nil)
			return err
		},
		"npm v1 entry": func() error {
			_, err := ParseNPMLock([]byte(`{"lockfileVersion": 1, "dependencies": {"a": []}}`), nil)
			return err
		},
		"npm v1 nested": func() error {
			_, err := ParseNPMLock([]byte(`{"lockfileVersion": 1, "dependencies": {"a": {"dependencies": []}}}`), nil)
			return err
		},
		"pnpm yaml":    func() error { _, err := ParsePNPMLock([]byte("[")); return err },
		"pnpm version": func() error { _, err := ParsePNPMLock([]byte("lockfileVersion: '4.0'")); return err },
		"pnpm no ver":  func() error { _, err := ParsePNPMLock([]byte("lockfileVersion: abc")); return err },
		"yarn entry":   func() error { _, err := ParseYarnLock([]byte("foo\n"), nil); return err },
		"yarn orphan":  func() error { _, err := ParseYarnLock([]byte("  version \"1\"\n"), nil); return err },
		"berry yaml": func() error {
			_, err := ParseYarnLock([]byte("__metadata:\n  version: 8\n\"a@npm:1\": [\n"), nil)
			return err
		},
		"berry entry": func() error {
			_, err := ParseYarnLock([]byte("__metadata:\n  version: 8\n\"a@npm:1\": [1]\n"), nil)
			return err
		},
		"yarn long line": func() error {
			_, err := ParseYarnLock([]byte("a@1:\n  version \""+strings.Repeat("x", 17*1024*1024)+"\"\n"), nil)
			return err
		},
	}
	for name, fn := range bad {
		if err := fn(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := ParseNPMLockVersionMessage(); !strings.Contains(err, "unsupported lockfileVersion 7") {
		t.Errorf("message: %s", err)
	}
}

func ParseNPMLockVersionMessage() string {
	_, err := ParseNPMLock([]byte(`{"lockfileVersion": 7}`), nil)
	return err.Error()
}

func TestYarnV1Details(t *testing.T) {
	lock := `# yarn lockfile v1

"string-width-cjs@npm:string-width@^4.2.0", string-width@^4.2.0:
  version "4.2.3"
  dependencies:
    ansi-regex "^5.0.1"
  optionalDependencies:
    "@scope/opt" "1.0.0"

ansi-regex@^5.0.1:
  version "5.0.1"
  resolved "https://example/x.tgz"

"@scope/opt@1.0.0":
  version "1.0.0"
  peerDependencies:
    react "*"

"lib@file:./lib":
  version "0.0.1"
  dependencies:
    ansi-regex "^5.0.1"
`
	ms := map[string]*Manifest{".": {Path: "package.json", Name: "app", Deps: map[DepKind]map[string]string{
		KindProd: {"string-width-cjs": "npm:string-width@^4.2.0", "lib": "file:./lib", "ws-pkg": "^1.0.0", "unknown": "^9"},
	}}, "packages/ws": {Path: "packages/ws/package.json", Name: "ws-pkg", Deps: map[DepKind]map[string]string{}}}
	tree, err := ParseYarnLock([]byte(lock), ms)
	if err != nil {
		t.Fatal(err)
	}
	want := `pkg:npm/%40scope/opt@1.0.0 runtime
pkg:npm/ansi-regex@5.0.1 runtime
pkg:npm/string-width@4.2.3 -> pkg:npm/%40scope/opt@1.0.0
pkg:npm/string-width@4.2.3 -> pkg:npm/ansi-regex@5.0.1
pkg:npm/string-width@4.2.3 runtime direct`
	if got := summary(t, tree); got != want {
		t.Errorf("graph:\n%s", got)
	}
	if n, r := SplitDescriptor("plain"); n != "plain" || r != "" {
		t.Error("descriptor without range")
	}
	if _, ok := aliasTarget("npm:@scope"); ok {
		t.Error("alias without range")
	}
	if f := yarnFields(`a "b c"  d`); strings.Join(f, "|") != "a|b c|d" {
		t.Errorf("fields: %v", f)
	}
}

func TestBerryDetails(t *testing.T) {
	lock := `__metadata:
  version: 6

"app@workspace:.":
  version: 0.0.0-use.local
  resolution: "app@workspace:."
  dependencies:
    left: ^1.0.0
    patched: "patch:patched@npm%3A1.0.0#~builtin<compat/patched>"
    other: "workspace:packages/other"
  languageName: unknown
  linkType: soft

"left@npm:^1.0.0":
  version: 1.0.1
  resolution: "left@npm:1.0.1"
  languageName: node
  linkType: hard

"patched@patch:patched@npm%3A1.0.0#~builtin<compat/patched>":
  version: 1.0.0
  resolution: "patched@patch:patched@npm%3A1.0.0#~builtin<compat/patched>::version=1.0.0"
  languageName: node
  linkType: hard

"other@workspace:packages/other":
  version: 0.0.0-use.local
  resolution: "other@workspace:packages/other"
  languageName: unknown
  linkType: soft

"noresolution@npm:2":
  version: 2.0.0
`
	tree, err := ParseYarnLock([]byte(lock), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `pkg:npm/left@1.0.1 runtime direct
pkg:npm/patched@1.0.0 runtime direct`
	if got := summary(t, tree); got != want || !strings.Contains(tree.Format, "v6") {
		t.Errorf("graph:\n%s\n%s", got, tree.Format)
	}
}

func TestPNPMDetails(t *testing.T) {
	v9 := `lockfileVersion: '9.0'
importers:
  .:
    dependencies:
      react-dom:
        specifier: ^18
        version: 18.2.0(react@18.2.0)
      alias:
        specifier: npm:real@^1
        version: real@1.0.0
      local:
        specifier: link:../local
        version: link:../local
packages:
  react-dom@18.2.0:
    resolution: {integrity: x}
  react@18.2.0:
    resolution: {integrity: y}
  real@1.0.0:
    resolution: {integrity: z}
  tarball@file:x.tgz:
    name: tarball
    version: 3.0.0
    resolution: {tarball: x.tgz}
snapshots:
  react-dom@18.2.0(react@18.2.0):
    dependencies:
      react: 18.2.0
      tarball: file:x.tgz
  react@18.2.0: {}
  real@1.0.0: {}
`
	tree, err := ParsePNPMLock([]byte(v9))
	if err != nil {
		t.Fatal(err)
	}
	want := `pkg:npm/react-dom@18.2.0 -> pkg:npm/react@18.2.0
pkg:npm/react-dom@18.2.0 runtime direct
pkg:npm/react@18.2.0 runtime
pkg:npm/real@1.0.0 runtime direct`
	if got := summary(t, tree); got != want {
		t.Errorf("v9:\n%s", got)
	}
	v6 := `lockfileVersion: '6.0'
dependencies:
  '@types/a':
    specifier: ^1
    version: 1.0.0
  b:
    specifier: npm:c@^2
    version: /c@2.0.0
devDependencies:
  '@types/a':
    specifier: ^1
    version: 1.0.0
packages:
  /@types/a@1.0.0:
    resolution: {integrity: x}
  /c@2.0.0:
    resolution: {integrity: x}
    dependencies:
      '@types/a': 1.0.0
`
	tree, err = ParsePNPMLock([]byte(v6))
	if err != nil {
		t.Fatal(err)
	}
	want = `pkg:npm/%40types/a@1.0.0 runtime direct
pkg:npm/c@2.0.0 -> pkg:npm/%40types/a@1.0.0
pkg:npm/c@2.0.0 runtime direct`
	if got := summary(t, tree); got != want {
		t.Errorf("v6:\n%s", got)
	}
	v5 := `lockfileVersion: 5.3
specifiers:
  react-dom: ^18
dependencies:
  react-dom: 18.2.0_react@18.2.0
packages:
  /react-dom/18.2.0_react@18.2.0:
    resolution: {integrity: x}
    dependencies:
      react: 18.2.0
      '@scope/x': 1.0.0_@babel+core@7.0.0
  /react/18.2.0:
    resolution: {integrity: x}
  /@scope/x/1.0.0_@babel+core@7.0.0:
    resolution: {integrity: x}
  /weird:
    resolution: {integrity: x}
`
	tree, err = ParsePNPMLock([]byte(v5))
	if err != nil {
		t.Fatal(err)
	}
	want = `pkg:npm/%40scope/x@1.0.0 runtime
pkg:npm/react-dom@18.2.0 -> pkg:npm/%40scope/x@1.0.0
pkg:npm/react-dom@18.2.0 -> pkg:npm/react@18.2.0
pkg:npm/react-dom@18.2.0 runtime direct
pkg:npm/react@18.2.0 runtime`
	if got := summary(t, tree); got != want {
		t.Errorf("v5:\n%s", got)
	}
}
