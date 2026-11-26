package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const profile = `mode: set
github.com/x/pkg/sdk/a.go:1.1,2.2 10 1
github.com/x/pkg/sdk/a.go:3.1,4.2 1 0
github.com/x/plugins/p/adapters/b.go:1.1,2.2 9 1
github.com/x/plugins/p/adapters/b.go:3.1,4.2 1 0
github.com/x/cmd/deepward/main.go:1.1,2.2 50 0
`

func TestEvaluate(t *testing.T) {
	r, err := Evaluate(strings.NewReader(profile), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Packages) != 2 {
		t.Fatalf("cmd must be excluded, got %+v", r.Packages)
	}
	sdk, adapters := r.Packages[0], r.Packages[1] // sorted: pkg/sdk < plugins/...
	if sdk.Rule != "domain/app" || sdk.OK() {
		t.Errorf("sdk at 90.9%% must fail the 95%% gate: %+v", sdk)
	}
	if adapters.Rule != "adapters" || !adapters.OK() {
		t.Errorf("adapters at 90%% must pass the 85%% gate: %+v", adapters)
	}
	if r.OK() {
		t.Error("report must fail")
	}
	var buf bytes.Buffer
	r.Write(&buf)
	if !strings.Contains(buf.String(), "FAIL") || !strings.Contains(buf.String(), "overall") {
		t.Errorf("unexpected output:\n%s", buf.String())
	}
}

func TestEvaluateDuplicateBlocksTakeMax(t *testing.T) {
	p := "mode: count\na/pkg/sdk/x.go:1.1,2.2 4 0\na/pkg/sdk/x.go:1.1,2.2 4 3\n"
	r, err := Evaluate(strings.NewReader(p), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.Overall != 100 {
		t.Fatalf("got %+v", r)
	}
}

func TestEvaluateEmptyProfilePasses(t *testing.T) {
	r, err := Evaluate(strings.NewReader("mode: set\n"), DefaultPolicy())
	if err != nil || !r.OK() {
		t.Fatalf("empty profile must pass: %+v %v", r, err)
	}
}

func TestEvaluateMalformed(t *testing.T) {
	for _, p := range []string{"mode: set\nbad line\n", "mode: set\na.go:1.1,2.2 x 1\n"} {
		if _, err := Evaluate(strings.NewReader(p), DefaultPolicy()); err == nil {
			t.Errorf("expected error for %q", p)
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.out")
	bad := filepath.Join(dir, "bad.out")
	_ = os.WriteFile(good, []byte("mode: set\na/pkg/sdk/x.go:1.1,2.2 4 1\n"), 0o600)
	_ = os.WriteFile(bad, []byte(profile), 0o600)
	var out bytes.Buffer
	if code := run([]string{"-profile", good}, &out); code != 0 {
		t.Errorf("good: code %d\n%s", code, out.String())
	}
	if code := run([]string{"-profile", bad}, &out); code != 1 {
		t.Errorf("bad: code %d", code)
	}
	if code := run([]string{"-profile", filepath.Join(dir, "missing")}, &out); code != 2 {
		t.Errorf("missing: code %d", code)
	}
	if code := run([]string{"-unknown"}, &out); code != 2 {
		t.Errorf("bad flag: code %d", code)
	}
	_ = os.WriteFile(bad, []byte("mode: set\nbroken\n"), 0o600)
	if code := run([]string{"-profile", bad}, &out); code != 2 {
		t.Errorf("malformed: code %d", code)
	}
}
