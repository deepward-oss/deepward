package archtest

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const m = Module + "/"

func TestCheckDetectsEveryRule(t *testing.T) {
	pkgs := []Package{
		{ImportPath: m + "internal/engine", Imports: []string{m + "pkg/sdk", m + "plugins/ecosystem/maven"}},
		{ImportPath: m + "pkg/sdk", Imports: []string{m + "internal/engine"}},
		{ImportPath: m + "pkg/sdk/deps", Imports: []string{m + "plugins/ecosystem/maven"}},
		{ImportPath: m + "plugins/ecosystem/maven/app", Imports: []string{
			m + "plugins/ecosystem/maven/domain", // same plugin: allowed
			m + "plugins/analyzer/depvuln",       // other plugin: forbidden
			m + "internal/textedit",              // allowed helper
			m + "internal/engine",                // forbidden
			"os/exec",                            // forbidden
		}},
		{ImportPath: m + "plugins/analyzer/depvuln/domain", Imports: []string{"net/http", "strings"}},
		{ImportPath: m + "internal/toolchain", Imports: []string{"os/exec"}},
		{ImportPath: "example.com/other", Imports: []string{"os/exec"}},
		{ImportPath: m + "plugins/all", Imports: []string{m + "plugins/ecosystem/maven"}},
	}
	got := Check(pkgs)
	want := map[string]bool{
		"engine-is-plugin-agnostic":    false,
		"sdk-is-self-contained":        false,
		"plugins-are-isolated":         false,
		"plugins-use-only-sdk":         false,
		"processes-only-via-toolchain": false,
		"domain-has-no-io":             false,
	}
	for _, v := range got {
		want[v.Rule] = true
		if v.To == m+"plugins/ecosystem/maven/domain" || v.To == m+"internal/textedit" {
			t.Errorf("allowed edge reported: %s", v)
		}
		if v.From == m+"internal/toolchain" || v.From == "example.com/other" {
			t.Errorf("allowed package reported: %s", v)
		}
	}
	for rule, seen := range want {
		if !seen {
			t.Errorf("rule %s not triggered; got %v", rule, got)
		}
	}
	if len(got) != 7 {
		t.Errorf("expected 7 violations, got %d: %v", len(got), got)
	}
}

func TestPluginRoot(t *testing.T) {
	if got := pluginRoot("plugins/ecosystem"); got != "plugins/ecosystem" {
		t.Errorf("short path: %s", got)
	}
	if got := pluginRoot("plugins/a/b/c/d"); got != "plugins/a/b" {
		t.Errorf("deep path: %s", got)
	}
}

// TestRepositoryRespectsBoundaries runs the rules over the real module.
func TestRepositoryRespectsBoundaries(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, Module+"/...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []Package
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pkgs = append(pkgs, Package{ImportPath: fields[0], Imports: fields[1:]})
	}
	for _, v := range Check(pkgs) {
		t.Error(v)
	}
}
