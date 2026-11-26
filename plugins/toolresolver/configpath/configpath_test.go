package configpath

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestResolver(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "tools", "dotnet")
	_ = os.MkdirAll(filepath.Dir(bin), 0o755)
	_ = os.WriteFile(bin, []byte("x"), 0o755)
	ws := &sdk.Workspace{Root: root}
	c := sdktest.TestToolResolver(t, Resolver{}, sdk.ResolveRequest{Settings: sdk.ToolSettings{Path: "tools/dotnet"}, Workspace: ws})
	if c == nil || c.Command[0] != bin {
		t.Fatalf("relative path: %+v", c)
	}
	c, _ = Resolver{}.Resolve(context.Background(), sdk.ResolveRequest{Settings: sdk.ToolSettings{Path: bin}})
	if c == nil || c.Source != bin {
		t.Error("absolute path")
	}
	if c, err := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{}); c != nil || err != nil {
		t.Error("no path configured")
	}
	if _, err := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{Settings: sdk.ToolSettings{Path: filepath.Join(root, "missing")}}); err == nil {
		t.Error("missing file")
	}
	if _, err := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{Settings: sdk.ToolSettings{Path: root}}); err == nil {
		t.Error("directory")
	}
}
