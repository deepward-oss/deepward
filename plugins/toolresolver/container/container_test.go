package container

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/sdktest"
)

func TestResolver(t *testing.T) {
	uid, gid := 1000, 1000
	getenv := func(k string) string { return map[string]string{"DEEPWARD_CONTAINER_RUNTIME": "podman"}[k] }
	r := &Resolver{Getenv: getenv, UID: &uid, GID: &gid}
	ws := &sdk.Workspace{Root: "/work/repo"}
	image := "sbtscala/scala-sbt:eclipse-temurin-17.0.10_7_1.10.1_2.13.14"
	req := sdk.ResolveRequest{Definition: sdk.ToolDefinition{Name: "sbt", Executables: []string{"sbt", "sbt.bat"}},
		Settings: sdk.ToolSettings{Image: image}, Workspace: ws}
	c := sdktest.TestToolResolver(t, r, req)
	root := filepath.Clean("/work/repo")
	want := "podman run --rm -i --user 1000:1000 -v " + root + ":" + root + " {{dirmount}} -w {{dir}} {{env}} " + image + " sbt"
	if runtime.GOOS == "windows" {
		want = strings.Replace(want, " --user 1000:1000", "", 1)
	}
	if got := strings.Join(c.Command, " "); got != want || c.Source != "podman:"+image {
		t.Errorf("command:\n got %s\nwant %s", got, want)
	}
	noUser := -1
	r2 := &Resolver{Getenv: getenv, UID: &noUser, GID: &noUser}
	c, _ = r2.Resolve(context.Background(), sdk.ResolveRequest{Definition: sdk.ToolDefinition{Name: "x"}, Settings: req.Settings, Workspace: ws})
	if strings.Contains(strings.Join(c.Command, " "), "--user") || c.Command[len(c.Command)-1] != "x" {
		t.Errorf("no user / name fallback: %v", c.Command)
	}
	imageUser := req
	imageUser.Settings.ContainerUser = "image"
	c, _ = r.Resolve(context.Background(), imageUser)
	if strings.Contains(strings.Join(c.Command, " "), "--user") {
		t.Errorf("image user: %v", c.Command)
	}
	explicit := req
	explicit.Settings.ContainerUser = "1654:1654"
	c, _ = r.Resolve(context.Background(), explicit)
	if !strings.Contains(strings.Join(c.Command, " "), "--user 1654:1654") {
		t.Errorf("explicit user: %v", c.Command)
	}
	if c, _ := r.Resolve(context.Background(), sdk.ResolveRequest{}); c != nil {
		t.Error("no image")
	}
	if _, err := r.Resolve(context.Background(), sdk.ResolveRequest{Settings: req.Settings}); err == nil {
		t.Error("no workspace")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "docker"), []byte("x"), 0o755)
	found := &Resolver{Getenv: func(k string) string {
		if k == "PATH" {
			return dir
		}
		return ""
	}}
	if rt, err := found.runtime(); err != nil || rt != filepath.Join(dir, "docker") {
		t.Errorf("docker on PATH: %s %v", rt, err)
	}
	none := &Resolver{Getenv: func(string) string { return "" }}
	if _, err := none.Resolve(context.Background(), req); err == nil {
		t.Error("no runtime")
	}
	def := &Resolver{}
	if def.Info().ID != "container" {
		t.Error("id")
	}
	_, _ = def.runtime()
}
