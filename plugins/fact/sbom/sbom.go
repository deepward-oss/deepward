// Package sbom is the sbom-import fact producer: it reads a pre-built
// CycloneDX SBOM as a project's dependency graph instead of running the
// build tool.
package sbom

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/deepward-oss/deepward/pkg/sdk"
	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

// ID is the plugin ID.
const ID = "sbom-import"

// Config is plugins.sbom-import (also filled by --sbom <dir>=<path>).
type Config struct {
	// Files maps a project directory to an SBOM path (relative to the
	// repository root).
	Files map[string]string `yaml:"files" json:"files"`
}

// Producer imports CycloneDX SBOMs.
type Producer struct{ cfg Config }

func init() { sdk.Register(New()) }

// New returns the producer.
func New() *Producer { return &Producer{} }

// Info implements sdk.Plugin.
func (p *Producer) Info() sdk.Info {
	return sdk.Info{ID: ID, Kind: sdk.KindFactProducer, Version: sdk.BuiltinVersion,
		Description: "Uses a pre-built CycloneDX SBOM (1.4+) as a project's dependency graph (--sbom <dir>=<path>)"}
}

// ConfigTarget implements sdk.Configurable.
func (p *Producer) ConfigTarget() any { return &p.cfg }

// Produces implements sdk.FactProducer.
func (p *Producer) Produces() []string { return []string{deps.FactDependencyGraph} }

func (p *Producer) file(pr sdk.Project) (string, bool) {
	f, ok := p.cfg.Files[path.Clean(pr.Dir)]
	return f, ok && f != ""
}

// AppliesTo implements sdk.FactProducer.
func (p *Producer) AppliesTo(pr sdk.Project) bool {
	_, ok := p.file(pr)
	return ok
}

// Produce implements sdk.FactProducer.
func (p *Producer) Produce(ctx context.Context, req sdk.FactRequest) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, ok := p.file(req.Project)
	if !ok {
		return nil, fmt.Errorf("no SBOM configured for %s", req.Project.ID)
	}
	name := rel
	if !filepath.IsAbs(name) && req.Workspace != nil {
		name = filepath.Join(req.Workspace.Root, filepath.FromSlash(rel))
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read SBOM: %w", err)
	}
	defer f.Close()
	g, err := deps.ParseCycloneDX(f, "cyclonedx:"+filepath.ToSlash(rel))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return g, nil
}
