package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/proc"
)

const sqliteBusyTimeoutMs = "30000"

var unitNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type sqlite struct {
	cfg   *config.Config
	run   proc.Runner
	files map[string]string
	order []string
}

func newSQLite(cfg *config.Config, run proc.Runner) *sqlite {
	return &sqlite{cfg: cfg, run: run}
}

func (s *sqlite) Validate() error {
	if err := s.cfg.Require("SQLITE_PATHS"); err != nil {
		return err
	}
	if err := rejectTLSKeys(s.cfg, "sqlite"); err != nil {
		return err
	}
	s.files = map[string]string{}
	s.order = nil
	for _, entry := range config.SplitList(s.cfg.Get("SQLITE_PATHS")) {
		name, path, err := sqliteEntry(entry)
		if err != nil {
			return err
		}
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("paths entry %s does not exist", path)
		}
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		if _, dup := s.files[name]; dup {
			return fmt.Errorf("paths has two files named %s", name)
		}
		s.files[name] = path
		s.order = append(s.order, name)
	}
	if len(s.files) == 0 {
		return errors.New("paths lists no files")
	}
	return nil
}

func sqliteEntry(entry string) (name, path string, err error) {
	idx := strings.Index(entry, "=")
	if idx < 0 || strings.Contains(entry[:idx], "/") {
		return "", entry, nil
	}
	name = strings.TrimSpace(entry[:idx])
	if !unitNameRe.MatchString(name) {
		return "", "", fmt.Errorf("paths entry %q has an invalid name", entry)
	}
	return name, strings.TrimSpace(entry[idx+1:]), nil
}

func (s *sqlite) Units() []string {
	return s.order
}

func (s *sqlite) Suffix() string {
	return ".db.gz"
}

func (s *sqlite) sql(ctx context.Context, db, query string) (string, error) {
	out, err := proc.Output(ctx, s.run, proc.Spec{Name: "sqlite3", Args: []string{db, query}, Env: proc.BaseEnv()})
	return strings.TrimRight(out, "\n"), err
}

func (s *sqlite) excludeRows(ctx context.Context, copy string) error {
	tables := config.SplitList(s.cfg.ExcludeTableData)
	if len(tables) == 0 {
		return nil
	}
	drops, err := s.sql(ctx, copy, `select 'DROP TRIGGER "' || replace(name, '"', '""') || '";' from sqlite_master where type = 'trigger'`)
	if err != nil {
		return err
	}
	recreates, err := s.sql(ctx, copy, `select sql || ';' from sqlite_master where type = 'trigger' and sql is not null`)
	if err != nil {
		return err
	}
	var deletes strings.Builder
	for _, t := range tables {
		deletes.WriteString("DELETE FROM " + quoteSQLite(t) + "; ")
	}
	if _, err := s.sql(ctx, copy, "BEGIN; "+drops+" "+deletes.String()+" "+recreates+" COMMIT;"); err != nil {
		return err
	}
	_, err = s.sql(ctx, copy, "VACUUM")
	return err
}

func (s *sqlite) Dump(ctx context.Context, unit string, w io.Writer) error {
	dir, err := os.MkdirTemp(s.cfg.BackupDir, ".sqlite."+unit+".")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	copy := filepath.Join(dir, "db")
	err = s.run.Run(ctx, proc.Spec{
		Name:   "sqlite3",
		Args:   []string{"-cmd", ".timeout " + sqliteBusyTimeoutMs, s.files[unit], "VACUUM INTO " + quoteSQLiteString(copy)},
		Env:    proc.BaseEnv(),
		Stdout: io.Discard,
	})
	if err != nil {
		return err
	}
	if err := s.excludeRows(ctx, copy); err != nil {
		return err
	}
	f, err := os.Open(copy)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (s *sqlite) gunzipToTemp(unit, path string) (string, error) {
	r, err := openGzip(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	f, err := os.CreateTemp(s.cfg.BackupDir, ".sqlite."+unit+".*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

func (s *sqlite) count(ctx context.Context, db string) (int, error) {
	out, err := s.sql(ctx, db, "select count(*) from sqlite_master where type = 'table'")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func (s *sqlite) Tables(ctx context.Context, unit, path string) (int, error) {
	copy, err := s.gunzipToTemp(unit, path)
	if err != nil {
		return 0, err
	}
	defer os.Remove(copy)
	return s.count(ctx, copy)
}

func (s *sqlite) Verify(ctx context.Context, unit, path string) error {
	copy, err := s.gunzipToTemp(unit, path)
	if err != nil {
		return err
	}
	defer os.Remove(copy)
	res, err := s.sql(ctx, copy, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("%s: %s", unit, res)
	}
	got, countErr := s.count(ctx, copy)
	fk := ""
	if s.cfg.ExcludeTableData != "" {
		out, err := s.sql(ctx, copy, "PRAGMA foreign_key_check")
		if err != nil {
			out = "check failed: " + out
		}
		fk = out
	}
	if res != "ok" {
		return fmt.Errorf("%s integrity_check: %s", unit, res)
	}
	if countErr != nil || got == 0 {
		return fmt.Errorf("%s restored no tables", unit)
	}
	if fk != "" {
		return fmt.Errorf("%s foreign key violations after excluding rows: %s", unit, fk)
	}
	return nil
}
