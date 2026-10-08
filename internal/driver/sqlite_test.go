package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/proc"
)

func TestSQLiteValidate(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a", "app.db"), filepath.Join(dir, "b", "app.sqlite")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ paths, want string }{
		{"", "paths is required"},
		{filepath.Join(dir, "missing.db"), "paths entry " + filepath.Join(dir, "missing.db") + " does not exist"},
		{a + "," + b, "paths has two files named app"},
	}
	for _, c := range cases {
		cfg := testConfig(t, "SQLITE_PATHS", c.paths)
		err := newSQLite(cfg, &proc.Fake{}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("paths %q: error = %v, want %q", c.paths, err, c.want)
		}
	}
	s := newSQLite(testConfig(t, "SQLITE_PATHS", a), &proc.Fake{})
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := s.Units(); len(got) != 1 || got[0] != "app" {
		t.Errorf("units = %v", got)
	}
}

func TestSQLiteExcludeRowsBuildsAnInjectionSafeScript(t *testing.T) {
	var scripts []string
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		scripts = append(scripts, strings.Join(s.Args, " "))
		if s.Stdout != nil && strings.Contains(strings.Join(s.Args, " "), "sqlite_master") {
			_, _ = s.Stdout.Write([]byte("DROP TRIGGER \"t1\";\n"))
		}
		return nil
	}}
	cfg := testConfig(t, "EXCLUDE_TABLE_DATA", `logs, we"ird`)
	s := newSQLite(cfg, f)
	if err := s.excludeRows(context.Background(), "/tmp/copy.db"); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(scripts, "\n")
	if !strings.Contains(all, `DELETE FROM "logs";`) || !strings.Contains(all, `DELETE FROM "we""ird";`) {
		t.Errorf("script = %s", all)
	}
	if !strings.Contains(all, "BEGIN;") || !strings.Contains(all, "COMMIT;") || !strings.Contains(all, "VACUUM") {
		t.Errorf("script = %s", all)
	}
}

func TestSQLiteNamedPaths(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b, eq := mk("a/db.db"), mk("b/db.sqlite3"), mk("c/x=y.db")

	s := newSQLite(testConfig(t, "SQLITE_PATHS", "alpha="+a+", beta = "+b), &proc.Fake{})
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Units(), ","); got != "alpha,beta" {
		t.Errorf("units = %q", got)
	}
	if s.files["alpha"] != a || s.files["beta"] != b {
		t.Errorf("files = %v", s.files)
	}

	plain := newSQLite(testConfig(t, "SQLITE_PATHS", eq), &proc.Fake{})
	if err := plain.Validate(); err != nil {
		t.Fatalf("a path that contains '=' but no name must still work: %v", err)
	}
	if got := strings.Join(plain.Units(), ","); got != "x=y" {
		t.Errorf("units = %q", got)
	}

	cases := []struct{ paths, want string }{
		{"a b=" + a, `paths entry "a b=` + a + `" has an invalid name`},
		{"=" + a, `paths entry "=` + a + `" has an invalid name`},
		{"one=" + a + ",one=" + b, "paths has two files named one"},
		{"db=" + a + "," + b, "paths has two files named db"},
	}
	for _, c := range cases {
		err := newSQLite(testConfig(t, "SQLITE_PATHS", c.paths), &proc.Fake{}).Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%q: unexpected error %v", c.paths, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error = %v, want %q", c.paths, err, c.want)
		}
	}
}
