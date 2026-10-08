package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/proc"
)

const pgTablesSQL = "select count(*) from information_schema.tables where table_type = 'BASE TABLE' and table_schema not in ('pg_catalog', 'information_schema')"

var pgCreateTable = regexp.MustCompile(`^CREATE (UNLOGGED )?TABLE `)

var binExecutable = func(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode()&0o111 != 0
}

type postgres struct {
	cfg *config.Config
	run proc.Runner
	env []string
	dbs []string
}

func newPostgres(cfg *config.Config, run proc.Runner) *postgres {
	return &postgres{cfg: cfg, run: run}
}

func (p *postgres) Validate() error {
	if err := p.cfg.Require("DB_HOST", "DB_USER", "DATABASES"); err != nil {
		return err
	}
	password, err := p.cfg.Secret("DB_PASSWORD")
	if err != nil {
		return err
	}
	if password == "" {
		return errors.New("password_env or password_file is required")
	}
	port := p.cfg.Get("DB_PORT")
	if port == "" {
		port = "5432"
	}
	ca, fingerprint, err := tlsKeys(p.cfg)
	if err != nil {
		return err
	}
	if fingerprint != "" {
		return errors.New("tls.fingerprint is only supported for mysql")
	}
	p.env = append(proc.BaseEnv(), "PGHOST="+p.cfg.Get("DB_HOST"), "PGPORT="+port, "PGUSER="+p.cfg.Get("DB_USER"), "PGPASSWORD="+password)
	if ca != "" {
		p.env = append(p.env, "PGSSLMODE=verify-full", "PGSSLROOTCERT="+ca)
	}
	p.dbs = config.SplitList(p.cfg.Get("DATABASES"))
	if len(p.dbs) == 0 {
		return errors.New("databases lists no databases")
	}
	return nil
}

func (p *postgres) Units() []string {
	return p.dbs
}

func (p *postgres) Suffix() string {
	return ".sql.gz"
}

func (p *postgres) psql(ctx context.Context, db, sql string) error {
	return p.run.Run(ctx, proc.Spec{Name: "psql", Args: []string{"-d", db, "-q", "-c", sql}, Env: p.env, Stdout: io.Discard})
}

func (p *postgres) dumpBin(ctx context.Context) (string, error) {
	out, err := proc.Output(ctx, p.run, proc.Spec{Name: "psql", Args: []string{"-d", "postgres", "-Atq", "-c", "show server_version_num"}, Env: p.env})
	if err != nil {
		return "", err
	}
	num, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("unexpected server version %q", strings.TrimSpace(out))
	}
	bin := fmt.Sprintf("/usr/lib/postgresql/%d/bin/pg_dump", num/10000)
	if !binExecutable(bin) {
		return "", fmt.Errorf("no pg_dump for server version %d", num)
	}
	return bin, nil
}

func (p *postgres) Dump(ctx context.Context, unit string, w io.Writer) error {
	bin, err := p.dumpBin(ctx)
	if err != nil {
		return err
	}
	args := []string{"-d", unit, "--no-owner", "--no-privileges", "--lock-wait-timeout=60s"}
	args = append(args, p.cfg.ExtraOpts...)
	for _, t := range config.SplitList(p.cfg.ExcludeTableData) {
		args = append(args, "--exclude-table-data="+t)
	}
	return p.run.Run(ctx, proc.Spec{Name: bin, Args: args, Env: p.env, Stdout: w})
}

func (p *postgres) Tables(_ context.Context, _, path string) (int, error) {
	return countTables(path, pgCreateTable)
}

func (p *postgres) restoreAndCount(ctx context.Context, scratch, path string) (int, error) {
	r, err := openGzip(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	err = p.run.Run(ctx, proc.Spec{Name: "psql", Args: []string{"-v", "ON_ERROR_STOP=1", "-q", "-d", scratch}, Env: p.env, Stdin: r, Stdout: io.Discard})
	if err != nil {
		return 0, err
	}
	out, err := proc.Output(ctx, p.run, proc.Spec{Name: "psql", Args: []string{"-d", scratch, "-Atq", "-c", pgTablesSQL}, Env: p.env})
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func (p *postgres) Verify(ctx context.Context, unit, path string, want int) error {
	scratch := "dbb_verify_" + unit
	if err := p.psql(ctx, "postgres", "DROP DATABASE IF EXISTS "+quotePG(scratch)); err != nil {
		return err
	}
	if err := p.psql(ctx, "postgres", "CREATE DATABASE "+quotePG(scratch)); err != nil {
		return err
	}
	got, rerr := p.restoreAndCount(ctx, scratch, path)
	if err := p.psql(ctx, "postgres", "DROP DATABASE IF EXISTS "+quotePG(scratch)); err != nil {
		logx.Warnf("could not drop %s", scratch)
	}
	if rerr != nil {
		return rerr
	}
	if got != want {
		return fmt.Errorf("%s restored %d tables, expected %d", unit, got, want)
	}
	return nil
}
