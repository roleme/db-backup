package driver

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/proc"
)

func newTestPG(t *testing.T, f *proc.Fake, kv ...string) *postgres {
	t.Helper()
	old := binExecutable
	binExecutable = func(string) bool { return true }
	t.Cleanup(func() { binExecutable = old })
	base := []string{"DRIVER", "postgres", "DB_HOST", "pg", "DB_USER", "backup", "DB_PASSWORD", "pw", "DATABASES", "app, we\"ird"}
	p := newPostgres(testConfig(t, append(base, kv...)...), f)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPostgresValidate(t *testing.T) {
	cases := []struct {
		kv   []string
		want string
	}{
		{[]string{"DRIVER", "postgres", "DB_USER", "u", "DATABASES", "a"}, "host is required"},
		{[]string{"DRIVER", "postgres", "DB_HOST", "h", "DB_USER", "u", "DATABASES", "a"}, "password_env or password_file is required"},
		{[]string{"DRIVER", "postgres", "DB_HOST", "h", "DB_USER", "u", "DB_PASSWORD", "x", "DATABASES", " , "}, "databases lists no databases"},
	}
	for _, c := range cases {
		err := newPostgres(testConfig(t, c.kv...), &proc.Fake{}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: error = %v, want %q", c.kv, err, c.want)
		}
	}
}

func TestPostgresDumpArgsAndEnv(t *testing.T) {
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		if s.Name == "psql" {
			_, _ = s.Stdout.Write([]byte("160004\n"))
		}
		return nil
	}}
	p := newTestPG(t, f, "EXTRA_OPTS", "--clean --if-exists", "EXCLUDE_TABLE_DATA", "logs,audit")
	if err := p.Dump(context.Background(), "app", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	dump := f.Calls[len(f.Calls)-1].Spec
	if dump.Name != "/usr/lib/postgresql/16/bin/pg_dump" {
		t.Errorf("binary = %s", dump.Name)
	}
	want := "-d app --no-owner --no-privileges --lock-wait-timeout=60s --clean --if-exists --exclude-table-data=logs --exclude-table-data=audit"
	if strings.Join(dump.Args, " ") != want {
		t.Errorf("args = %q", strings.Join(dump.Args, " "))
	}
	env := strings.Join(dump.Env, "\n")
	for _, e := range []string{"PGHOST=pg", "PGPORT=5432", "PGUSER=backup", "PGPASSWORD=pw"} {
		if !strings.Contains(env, e) {
			t.Errorf("env missing %s: %q", e, env)
		}
	}
	for _, a := range dump.Args {
		if strings.Contains(a, "pw") {
			t.Error("the password must never be an argument")
		}
	}
}

func TestPostgresVerifyQuotesTheScratchName(t *testing.T) {
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		if s.Stdout != nil && strings.Contains(strings.Join(s.Args, " "), "information_schema") {
			_, _ = s.Stdout.Write([]byte("2\n"))
		}
		return nil
	}}
	p := newTestPG(t, f)
	path := writeGz(t, "CREATE TABLE a (x int);\nCREATE TABLE b (x int);\n")
	if err := p.Verify(context.Background(), `we"ird`, path); err != nil {
		t.Fatal(err)
	}
	var sql []string
	for _, c := range f.Calls {
		sql = append(sql, strings.Join(c.Spec.Args, " "))
	}
	all := strings.Join(sql, "\n")
	if !strings.Contains(all, `DROP DATABASE IF EXISTS "dbb_verify_we""ird"`) || !strings.Contains(all, `CREATE DATABASE "dbb_verify_we""ird"`) {
		t.Errorf("calls = %s", all)
	}
}

func TestPostgresVerifyFailsOnZeroTablesAndStillDrops(t *testing.T) {
	f := &proc.Fake{Handler: func(s proc.Spec) error {
		if s.Stdout != nil && strings.Contains(strings.Join(s.Args, " "), "information_schema") {
			_, _ = s.Stdout.Write([]byte("0\n"))
		}
		return nil
	}}
	p := newTestPG(t, f)
	err := p.Verify(context.Background(), "app", writeGz(t, "SELECT 1;\n"))
	if err == nil || !strings.Contains(err.Error(), "app restored no tables") {
		t.Errorf("error = %v", err)
	}
	last := strings.Join(f.Calls[len(f.Calls)-1].Spec.Args, " ")
	if !strings.Contains(last, "DROP DATABASE IF EXISTS") {
		t.Errorf("the scratch database must be dropped even on failure; last call %q", last)
	}
}

func TestPostgresTablesCountsUnlogged(t *testing.T) {
	p := newTestPG(t, &proc.Fake{})
	n, err := p.Tables(context.Background(), "app", writeGz(t, "CREATE TABLE a (x int);\nCREATE UNLOGGED TABLE b (x int);\nCREATE INDEX i ON a (x);\n"))
	if err != nil || n != 2 {
		t.Errorf("Tables = %d, %v", n, err)
	}
}
