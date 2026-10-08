package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/proc"
)

func caFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, []byte("pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMySQLTLSOptions(t *testing.T) {
	ca := caFile(t)
	cases := []struct {
		name          string
		kv            []string
		want, notWant string
	}{
		{"none", nil, "--skip-ssl-verify-server-cert", "--ssl"},
		{"ca", []string{"DB_SSL_CA", ca}, "--ssl-ca=" + ca + " --ssl-verify-server-cert", "--skip-ssl-verify-server-cert"},
		{"fingerprint", []string{"DB_SSL_FINGERPRINT", "AA:BB:CC"}, "--ssl-fp=AA:BB:CC", "--skip-ssl-verify-server-cert"},
	}
	for _, c := range cases {
		m := newTestMySQL(t, &proc.Fake{}, c.kv...)
		got := strings.Join(m.args, " ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: args %q lack %q", c.name, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("%s: args %q must not contain %q", c.name, got, c.notWant)
		}
	}
}

func TestMySQLTLSErrors(t *testing.T) {
	base := []string{"DRIVER", "mysql", "DB_HOST", "my", "DB_USER", "u", "DB_PASSWORD", "pw", "DATABASES", "shop"}
	cases := []struct {
		kv   []string
		want string
	}{
		{[]string{"DB_SSL_CA", caFile(t), "DB_SSL_FINGERPRINT", "AA"}, "set only one of tls.ca and tls.fingerprint"},
		{[]string{"DB_SSL_CA", "/nope/ca.pem"}, "tls.ca /nope/ca.pem is not readable"},
	}
	for _, c := range cases {
		err := newMySQL(testConfig(t, append(base, c.kv...)...), &proc.Fake{}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: error = %v, want %q", c.kv, err, c.want)
		}
	}
}

func TestPostgresTLSOptions(t *testing.T) {
	ca := caFile(t)
	p := newTestPG(t, &proc.Fake{}, "DB_SSL_CA", ca)
	env := strings.Join(p.env, "\n")
	if !strings.Contains(env, "PGSSLMODE=verify-full") || !strings.Contains(env, "PGSSLROOTCERT="+ca) {
		t.Errorf("env = %q", env)
	}
	plain := strings.Join(newTestPG(t, &proc.Fake{}).env, "\n")
	if strings.Contains(plain, "PGSSL") {
		t.Errorf("without DB_SSL_CA no PGSSL variable may be set: %q", plain)
	}
}

func TestPostgresTLSErrors(t *testing.T) {
	base := []string{"DRIVER", "postgres", "DB_HOST", "pg", "DB_USER", "u", "DB_PASSWORD", "pw", "DATABASES", "app"}
	cases := []struct {
		kv   []string
		want string
	}{
		{[]string{"DB_SSL_FINGERPRINT", "AA"}, "tls.fingerprint is only supported for mysql"},
		{[]string{"DB_SSL_CA", "/nope/ca.pem"}, "tls.ca /nope/ca.pem is not readable"},
	}
	for _, c := range cases {
		err := newPostgres(testConfig(t, append(base, c.kv...)...), &proc.Fake{}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: error = %v, want %q", c.kv, err, c.want)
		}
	}
}

func TestSQLiteRejectsTLSKeys(t *testing.T) {
	db := filepath.Join(t.TempDir(), "app.db")
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DB_SSL_CA", "DB_SSL_FINGERPRINT"} {
		err := newSQLite(testConfig(t, "SQLITE_PATHS", db, key, "x"), &proc.Fake{}).Validate()
		if err == nil || !strings.Contains(err.Error(), key+" does not apply to sqlite") {
			t.Errorf("%s: error = %v", key, err)
		}
	}
}
