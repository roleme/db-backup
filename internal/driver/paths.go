package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/proc"
)

type paths struct {
	cfg   *config.Config
	run   proc.Runner
	dirs  map[string]string
	order []string
}

func newPaths(cfg *config.Config, run proc.Runner) *paths {
	return &paths{cfg: cfg, run: run}
}

func (p *paths) Validate() error {
	p.dirs = map[string]string{}
	p.order = nil
	for _, path := range config.SplitList(p.cfg.ExtraPaths) {
		name := filepath.Base(path)
		if _, dup := p.dirs[name]; dup {
			return fmt.Errorf("extra_paths has two directories named %s", name)
		}
		p.dirs[name] = path
		p.order = append(p.order, name)
	}
	return nil
}

func (p *paths) Units() []string {
	return p.order
}

func (p *paths) Suffix() string {
	return ".tar.gz"
}

func (p *paths) Dump(ctx context.Context, unit string, w io.Writer) error {
	path := p.dirs[unit]
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return fmt.Errorf("extra_paths entry %s is not a directory", path)
	}
	return p.run.Run(ctx, proc.Spec{
		Name:   "tar",
		Args:   []string{"-C", filepath.Dir(path), "-cf", "-", unit},
		Env:    proc.BaseEnv(),
		Stdout: w,
	})
}

func (p *paths) Verify(ctx context.Context, unit, path string) error {
	return p.run.Run(ctx, proc.Spec{
		Name:   "tar",
		Args:   []string{"-tzf", path},
		Env:    proc.BaseEnv(),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
}
