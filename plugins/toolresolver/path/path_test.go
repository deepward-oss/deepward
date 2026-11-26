package path

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestResolver(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(a, "mvn"), []byte("x"), 0o644) // not executable
	_ = os.WriteFile(filepath.Join(b, "mvn"), []byte("x"), 0o755)
	_ = os.MkdirAll(filepath.Join(a, "sbt"), 0o755) // a directory is not a tool
	sep := string(filepath.ListSeparator)
	r := &Resolver{Getenv: func(string) string { return sep + a + sep + b }}
	c := sdktest.TestToolResolver(t, r, sdk.ResolveRequest{Definition: sdk.ToolDefinition{Name: "mvn", Executables: []string{"mvn"}}})
	want := filepath.Join(b, "mvn")
	if runtime.GOOS == "windows" {
		want = filepath.Join(a, "mvn")
	}
	if c == nil || c.Command[0] != want {
		t.Fatalf("candidate: %+v", c)
	}
	if c, _ := r.Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Executables: []string{"sbt"}}}); c != nil {
		t.Error("directories are not tools")
	}
	real := &Resolver{}
	if real.Info().ID != "path" {
		t.Error("id")
	}
	if c, _ := real.Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Executables: []string{"definitely-not-a-tool-xyz"}}}); c != nil {
		t.Error("real PATH lookup of a missing tool")
	}
}
