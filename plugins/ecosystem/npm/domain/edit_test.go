package domain

import (
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	m, err := ParseManifest("package.json", []byte(`{"name":"a","workspaces":{"packages":["apps/*"]},"packageManager":"yarn@4.1.0",
		"dependencies":{"x":"^1","y":"1"},"devDependencies":{"x":"^1","z":"2"},"optionalDependencies":{"o":"3"},"peerDependencies":{"p":"4"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Workspaces, ",") != "apps/*" || m.Dir() != "." {
		t.Errorf("workspaces: %v", m.Workspaces)
	}
	if mg, ok := ManagerFromField(m.PackageManager); !ok || mg != Yarn {
		t.Error("manager")
	}
	if _, ok := ManagerFromField("bun@1"); ok {
		t.Error("bun is not supported")
	}
	d := m.DirectDeps()
	if d["x"] || !d["z"] || d["o"] || len(d) != 4 {
		t.Errorf("direct: %v", d)
	}
	if got := m.Declared("x"); len(got) != 2 || got[0] != KindProd || got[1] != KindDev {
		t.Errorf("declared: %v", got)
	}
	list, _ := ParseManifest("p/package.json", []byte(`{"workspaces":["a","b"]}`))
	if len(list.Workspaces) != 2 || list.Dir() != "p" {
		t.Error("list workspaces")
	}
	for _, bad := range []string{`{`, `{"workspaces": 3}`} {
		if _, err := ParseManifest("package.json", []byte(bad)); err == nil {
			t.Errorf("%s must fail", bad)
		}
	}
}

func TestMatchWorkspace(t *testing.T) {
	cases := []struct {
		patterns []string
		dir      string
		want     bool
	}{
		{[]string{"packages/*"}, "packages/lib", true},
		{[]string{"packages/*"}, "packages/lib/deep", false},
		{[]string{"apps/**"}, "apps/a/b", true},
		{[]string{"**/x"}, "x", true},
		{[]string{"./tools/cli/"}, "tools/cli", true},
		{[]string{"packages/*", "!packages/private"}, "packages/private", false},
		{[]string{"", "!"}, "a", false},
		{[]string{"a/**/c"}, "a/b", false},
		{nil, "a", false},
	}
	for _, c := range cases {
		if got := MatchWorkspace(c.patterns, c.dir); got != c.want {
			t.Errorf("%v %s = %v", c.patterns, c.dir, got)
		}
	}
}

func TestRewriteRange(t *testing.T) {
	ok := map[string]string{
		"^0.21.0":              "^0.21.2",
		"~0.21.0":              "~0.21.2",
		"0.21.0":               "0.21.2",
		"=0.21.0":              "=0.21.2",
		"v0.21.0":              "0.21.2",
		" ^0.21 ":              "^0.21.2",
		"1.0.0-beta.1":         "0.21.2",
		"npm:axios@^0.21.0":    "npm:axios@^0.21.2",
		"npm:@scope/ax@~0.21":  "npm:@scope/ax@~0.21.2",
		"1.2.3+build.5":        "0.21.2",
		"^ 0.21.0":             "^0.21.2",
		"~0.21.0-rc.1+meta.12": "~0.21.2",
	}
	for in, want := range ok {
		got, err := RewriteRange(in, "0.21.2")
		if err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"*", "latest", "^1 || ^2", "1.0.0 - 2.0.0", ">=1.0.0", "1.x", "workspace:*", "file:../x", "github:a/b", "npm:axios", "npm:x@*"} {
		if _, err := RewriteRange(bad, "1.0.0"); err == nil {
			t.Errorf("%q must not be rewritten", bad)
		}
	}
}

func TestJSONEdits(t *testing.T) {
	src := "{\n    \"name\": \"app\",\n    \"dependencies\": {\n        \"axios\": \"^0.21.0\",\n        \"a\\\"b\": \"1\"\n    },\n    \"x\": [1, {\"y\": \"]\"}, [2]],\n    \"n\": null\n}\n"
	start, end, v, ok := StringValue([]byte(src), "dependencies", "axios")
	if !ok || v != "^0.21.0" || src[start:end] != `"^0.21.0"` {
		t.Fatalf("locate: %v %q", ok, v)
	}
	if _, _, v, ok := StringValue([]byte(src), "dependencies", `a"b`); !ok || v != "1" {
		t.Error("escaped key")
	}
	for _, path := range [][]string{{"missing"}, {"name", "x"}, {"dependencies"}, {"n"}} {
		if _, _, _, ok := StringValue([]byte(src), path...); ok {
			t.Errorf("%v must not be found", path)
		}
	}
	if _, _, _, ok := StringValue([]byte("{"), "a"); ok {
		t.Error("broken json")
	}
	apply := func(data string, objPath []string, key, value string) string {
		t.Helper()
		e, err := SetMember([]byte(data), objPath, key, value)
		if err != nil {
			t.Fatal(err)
		}
		return string(ApplyJSONEdits([]byte(data), []JSONEdit{e}))
	}
	// Existing member is replaced in place.
	got := apply(src, []string{"dependencies"}, "axios", "^0.21.2")
	if !strings.Contains(got, `"axios": "^0.21.2",`) || len(got) != len(src) {
		t.Errorf("replace:\n%s", got)
	}
	// New nested objects use the file's indentation.
	got = apply(src, []string{"pnpm", "overrides"}, "follow-redirects@1.14.7", "1.14.8")
	want := "    \"n\": null,\n    \"pnpm\": {\n        \"overrides\": {\n            \"follow-redirects@1.14.7\": \"1.14.8\"\n        }\n    }\n}\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("nested insert:\n%s", got)
	}
	// Insert into an existing object.
	got = apply(src, []string{"dependencies"}, "lodash", "4.17.21")
	if !strings.Contains(got, "\"a\\\"b\": \"1\",\n        \"lodash\": \"4.17.21\"\n    }") {
		t.Errorf("member insert:\n%s", got)
	}
	// Empty objects, the root object and tabs.
	got = apply("{\n\t\"overrides\": {}\n}", []string{"overrides"}, "a", "1")
	if got != "{\n\t\"overrides\": {\n\t\t\"a\": \"1\"\n\t}\n}" {
		t.Errorf("empty object:\n%q", got)
	}
	got = apply("{}", nil, "a", "<1&>")
	if got != "{\n  \"a\": \"<1&>\"\n}" {
		t.Errorf("empty root:\n%q", got)
	}
	got = apply("\ufeff{\"a\":1}", []string{"b"}, "c", "d")
	if !strings.Contains(got, `"b": {`) {
		t.Errorf("bom: %q", got)
	}
	if _, err := SetMember([]byte(src), []string{"name"}, "a", "b"); err == nil {
		t.Error("not an object")
	}
	for _, bad := range []string{"", "[]", "{\"a\" 1}", "{1:2}", "{\"a\":1 \"b\":2}", "{\"a\":\"x", "{\"a\":[1,2}", "{\"a\": }", "{\"a\":\"\\q\"}", "{\"a\":"} {
		if _, err := SetMember([]byte(bad), nil, "k", "v"); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}
