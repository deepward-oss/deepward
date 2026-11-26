package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateSchema = flag.Bool("update", false, "rewrite the committed JSON Schema")

func TestCommittedSchemaIsUpToDate(t *testing.T) {
	got, err := ConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", filepath.FromSlash(SchemaPath))
	if *updateSchema {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/cli -run Schema -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("schema/deepward.schema.json is out of date; run: go test ./internal/cli -run Schema -update")
	}
	var s map[string]any
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	for _, key := range []string{"failOn", "ignore", "plugins", "tools", "ecosystems", "integrations", "fix", "output"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema lacks %s", key)
		}
	}
	if props["failOn"].(map[string]any)["enum"] == nil || props["pluginTimeout"].(map[string]any)["pattern"] == nil {
		t.Error("typed leaves")
	}
}
