// Package container runs tools inside a container image (docker or podman).
package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Resolver is the container tool resolver. It is used only for tools that
// configure tools.<name>.image.
type Resolver struct {
	// Getenv overrides os.Getenv (for tests).
	Getenv func(string) string
	// UID/GID override the host user (for tests); -1 disables --user.
	UID, GID *int
}

func init() { sdk.Register(&Resolver{}) }

// Info implements sdk.Plugin.
func (*Resolver) Info() sdk.Info {
	return sdk.Info{ID: "container", Kind: sdk.KindToolResolver, Version: sdk.BuiltinVersion,
		Description: "Runs tools inside tools.<name>.image with the workspace mounted at the same path (opt-in)"}
}

// Resolve implements sdk.ToolResolver.
func (r *Resolver) Resolve(_ context.Context, req sdk.ResolveRequest) (*sdk.ToolCandidate, error) {
	if req.Settings.Image == "" {
		return nil, nil
	}
	if req.Workspace == nil {
		return nil, errors.New("no workspace to mount")
	}
	rt, err := r.runtime()
	if err != nil {
		return nil, err
	}
	root := filepath.Clean(req.Workspace.Root)
	cmd := []string{rt, "run", "--rm", "-i"}
	uid, gid := os.Getuid(), os.Getgid()
	if r.UID != nil {
		uid = *r.UID
	}
	if r.GID != nil {
		gid = *r.GID
	}
	switch u := req.Settings.ContainerUser; u {
	case "", "host":
		if uid >= 0 && gid >= 0 && runtime.GOOS != "windows" {
			cmd = append(cmd, "--user", fmt.Sprintf("%d:%d", uid, gid))
		}
	case "image":
	default:
		cmd = append(cmd, "--user", u)
	}
	exe := req.Definition.Name
	if len(req.Definition.Executables) > 0 {
		exe = req.Definition.Executables[0]
	}
	cmd = append(cmd, "-v", root+":"+root, "{{dirmount}}", "-w", "{{dir}}", "{{env}}", req.Settings.Image, exe)
	return &sdk.ToolCandidate{Command: cmd, Source: rt + ":" + req.Settings.Image}, nil
}

func (r *Resolver) runtime() (string, error) {
	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if rt := getenv("DEEPWARD_CONTAINER_RUNTIME"); rt != "" {
		return rt, nil
	}
	for _, name := range []string{"docker", "podman"} {
		for _, dir := range filepath.SplitList(getenv("PATH")) {
			for _, ext := range []string{"", ".exe"} {
				if fi, err := os.Stat(filepath.Join(dir, name+ext)); err == nil && !fi.IsDir() {
					return filepath.Join(dir, name+ext), nil
				}
			}
		}
	}
	return "", errors.New("neither docker nor podman found on PATH")
}
