package driver

import (
	"context"
	"fmt"
	"io"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/proc"
)

type Adapter interface {
	Validate() error
	Units() []string
	Suffix() string
	Dump(ctx context.Context, unit string, w io.Writer) error
	Verify(ctx context.Context, unit, path string, wantTables int) error
}

type TableCounter interface {
	Tables(ctx context.Context, unit, path string) (int, error)
}

func ForConfig(cfg *config.Config, run proc.Runner) ([]Adapter, error) {
	var main Adapter
	switch cfg.Driver {
	case "postgres":
		main = newPostgres(cfg, run)
	case "mysql":
		main = newMySQL(cfg, run)
	case "sqlite":
		main = newSQLite(cfg, run)
	default:
		return nil, fmt.Errorf("driver must be postgres, mysql or sqlite (got '%s')", cfg.Driver)
	}
	adapters := []Adapter{main}
	if cfg.ExtraPaths != "" {
		adapters = append(adapters, newPaths(cfg, run))
	}
	return adapters, nil
}
