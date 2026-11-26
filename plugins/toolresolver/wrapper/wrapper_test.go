package wrapper

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
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "services", "api"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "mvnw"), []byte("#!/bin/sh"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "mvnw.cmd"), []byte("@echo off"), 0o644)
	ws := &sdk.Workspace{Root: root}
	def := sdk.ToolDefinition{Name: "mvn", Wrappers: []string{"mvnw", "mvnw.cmd"}}
	project := &sdk.Project{Dir: "services/api"}
	c := sdktest.TestToolResolver(t, Resolver{}, sdk.ResolveRequest{Definition: def, Project: project, Workspace: ws})
	want := filepath.Join(root, "mvnw")
	if runtime.GOOS == "windows" {
		want += ".cmd"
	}
	if c == nil || c.Command[len(c.Command)-1] != want {
		t.Fatalf("parent wrapper: %+v", c)
	}
	// A wrapper in the project directory wins over the root one; a missing
	// executable bit falls back to sh.
	_ = os.WriteFile(filepath.Join(root, "services", "api", "mvnw"), []byte("#!/bin/sh"), 0o644)
	c, _ = Resolver{}.Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Wrappers: []string{"mvnw"}}, Project: project, Workspace: ws})
	if runtime.GOOS != "windows" && (c == nil || c.Command[0] != "sh") {
		t.Errorf("non-executable wrapper: %+v", c)
	}
	if c, _ := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Wrappers: []string{"sbtx"}}, Project: project, Workspace: ws}); c != nil {
		t.Error("no wrapper anywhere")
	}
	if c, _ := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{Definition: def}); c != nil {
		t.Error("no workspace")
	}
	if c, _ := (Resolver{}).Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Wrappers: []string{"mvnw"}}, Workspace: ws}); c == nil {
		t.Error("workspace root without project")
	}
	if got := ordered([]string{"a.cmd", "b", "c.bat"}); runtime.GOOS != "windows" && (len(got) != 1 || got[0] != "b") {
		t.Errorf("ordered: %v", got)
	}
}
