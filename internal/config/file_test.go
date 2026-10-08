package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadTarget(t *testing.T, body, name string) (*Target, error) {
	t.Helper()
	f, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return f.Target(name)
}

func TestLoadMapsEveryFieldToTheJobSettings(t *testing.T) {
	tg, err := loadTarget(t, `
targets:
  full:
    driver: mysql
    host: db.internal
    port: 3307
    user: backup
    password_env: FULL_PW
    tls:
      ca: /certs/ca.pem
    databases: [alpha, beta]
    extra_paths: [/src/one, /src/two]
    extra_opts: ["--single-transaction", "--quick"]
    exclude_table_data: [logs, history]
    schedule: "20 1 * * *"
    verify_schedule: "@weekly"
    ping_url: http://x/ping
    verify_ping_url_env: FULL_VERIFY_URL
    keep: {mins: 60, days: 3, weeks: 2, months: 0}
    gzip_level: 9
    timeout: 120
`, "full")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DRIVER": "mysql", "DB_HOST": "db.internal", "DB_PORT": "3307", "DB_USER": "backup",
		"DB_PASSWORD_ENV": "FULL_PW", "DB_SSL_CA": "/certs/ca.pem",
		"DATABASES": "alpha,beta", "EXTRA_PATHS": "/src/one,/src/two",
		"EXTRA_OPTS": "--single-transaction --quick", "EXCLUDE_TABLE_DATA": "logs,history",
		"SCHEDULE": "20 1 * * *", "VERIFY_SCHEDULE": "@weekly",
		"HC_PING_URL": "http://x/ping", "HC_VERIFY_PING_URL_ENV": "FULL_VERIFY_URL",
		"KEEP_MINS": "60", "KEEP_DAYS": "3", "KEEP_WEEKS": "2", "KEEP_MONTHS": "0",
		"GZIP_LEVEL": "9",
	}
	for k, v := range want {
		if tg.Vars[k] != v {
			t.Errorf("%s = %q, want %q", k, tg.Vars[k], v)
		}
	}
	if len(tg.Vars) != len(want) {
		t.Errorf("vars = %v", tg.Vars)
	}
	if tg.Timeout.Seconds() != 120 {
		t.Errorf("timeout = %v", tg.Timeout)
	}
}

func TestLoadSQLitePathsAreNamed(t *testing.T) {
	tg, err := loadTarget(t, `
targets:
  app:
    driver: sqlite
    paths:
      zeta: /src/z/db.sqlite3
      alpha: /src/a/db.sqlite3
`, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := tg.Vars["SQLITE_PATHS"]; got != "alpha=/src/a/db.sqlite3,zeta=/src/z/db.sqlite3" {
		t.Errorf("SQLITE_PATHS = %q", got)
	}
	if tg.Timeout != DefaultTimeout {
		t.Errorf("default timeout = %v", tg.Timeout)
	}
}

func TestLoadTargetErrors(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"typo":      {"driver: sqlite\nschedle: \"@daily\"", "target typo: field schedle not found"},
		"secret":    {"driver: postgres\npassword: hunter2", "target secret: field password not found"},
		"nodriver":  {"host: a", "target nodriver: driver is required"},
		"baddriver": {"driver: oracle", "target baddriver: driver must be postgres, mysql or sqlite (got 'oracle')"},
		"pingboth":  {"driver: sqlite\nping_url: a\nping_url_env: B", "target pingboth: set only one of ping_url and ping_url_env"},
		"vpingboth": {"driver: sqlite\nverify_ping_url: a\nverify_ping_url_env: B", "target vpingboth: set only one of verify_ping_url and verify_ping_url_env"},
		"pwboth":    {"driver: postgres\npassword_env: A\npassword_file: /x", "target pwboth: set only one of password_env and password_file"},
		"badsch":    {"driver: sqlite\nschedule: nonsense", "target badsch: invalid schedule in schedule: nonsense"},
		"badvsch":   {"driver: sqlite\nverify_schedule: nonsense", "target badvsch: invalid schedule in verify_schedule: nonsense"},
		"badtime":   {"driver: sqlite\ntimeout: 0", "target badtime: timeout must be a positive number of seconds"},
		"negkeep":   {"driver: sqlite\nkeep: {days: -1}", "target negkeep: keep.days must be a non-negative integer"},
		"zeromins":  {"driver: sqlite\nkeep: {mins: 0}", "target zeromins: keep.mins must be a positive integer"},
		"badgzip":   {"driver: sqlite\ngzip_level: 10", "target badgzip: gzip_level must be between 1 and 9"},
		"badport":   {"driver: mysql\nport: abc", "target badport:"},
		"comma":     {"driver: postgres\ndatabases: [\"a,b\"]", "target comma: databases entry a,b must not contain a comma or a line break"},
		"commapath": {"driver: sqlite\npaths: {a: \"/x,y\"}", "target commapath: paths entry a must not contain a comma or a line break"},
		"badname":   {"driver: sqlite\npaths: {\"a b\": /x}", "target badname: paths name a b is invalid"},
		"emptylist": {"driver: postgres\ndatabases: [\"\"]", "target emptylist: databases entry must not be empty"},
		"notmap":    {"- driver", "target notmap:"},
	}
	for name, c := range cases {
		var body strings.Builder
		body.WriteString("targets:\n  " + name + ":\n")
		for _, line := range strings.Split(c.body, "\n") {
			body.WriteString("    " + line + "\n")
		}
		_, err := loadTarget(t, body.String(), name)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want %q", name, err, c.want)
		}
	}
}

func TestLoadFileErrors(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"syntax":    {"targets: [", "config.yaml"},
		"nokey":     {"other: 1\n", "targets"},
		"empty":     {"", "targets"},
		"notmap":    {"targets: [a]\n", "targets"},
		"duplicate": {"targets:\n  a: {driver: sqlite}\n  a: {driver: sqlite}\n", "already defined"},
	}
	for name, c := range cases {
		_, err := Load(writeConfig(t, c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want %q", name, err, c.want)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "is not readable") {
		t.Errorf("missing file error = %v", err)
	}
}

func TestOneBadTargetDoesNotHideTheOthers(t *testing.T) {
	f, err := Load(writeConfig(t, `
targets:
  good: {driver: sqlite}
  also-good: {driver: sqlite}
  broken: {driver: sqlite, nonsense: 1}
  "bad name": {driver: sqlite}
`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range f.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "also-good,bad name,broken,good" {
		t.Errorf("entries = %v", names)
	}
	for _, e := range f.Entries {
		bad := e.Name == "broken" || e.Name == "bad name"
		if bad != (e.Err != nil) {
			t.Errorf("%s: err = %v", e.Name, e.Err)
		}
	}
	if _, err := f.Target("missing"); err == nil || !strings.Contains(err.Error(), "target missing is not defined") {
		t.Errorf("missing target error = %v", err)
	}
	if _, err := f.Target("bad name"); err == nil || !strings.Contains(err.Error(), "invalid target name") {
		t.Errorf("bad name error = %v", err)
	}
}

func TestResolveIsolatesAndIndirects(t *testing.T) {
	tg, err := loadTarget(t, `
targets:
  one: {driver: sqlite, password_env: ONE_PW, ping_url_env: ONE_URL, timeout: 5}
`, "one")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ONE_PW": "secret-one", "ONE_URL": "http://x/one", "TWO_PW": "secret-two"}
	got, err := tg.Resolve(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(EnvList(got), "\n")
	if !strings.Contains(joined, "DB_PASSWORD=secret-one") || !strings.Contains(joined, "HC_PING_URL=http://x/one") {
		t.Errorf("resolved env = %q", joined)
	}
	for _, leak := range []string{"secret-two", "TWO_PW", "ONE_PW", "TIMEOUT"} {
		if strings.Contains(joined, leak) {
			t.Errorf("%q must not reach the child: %q", leak, joined)
		}
	}
	_, err = tg.Resolve(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "environment variable ONE_PW (from password_env) is not set") {
		t.Errorf("unset variable error = %v", err)
	}
}

func TestResolveReportsTheFirstUnsetVariableInFieldOrder(t *testing.T) {
	tg, err := loadTarget(t, `
targets:
  one: {driver: sqlite, ping_url_env: ONE_URL, password_env: ONE_PW}
`, "one")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		_, err := tg.Resolve(func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), "environment variable ONE_PW (from password_env) is not set") {
			t.Fatalf("run %d: error = %v, want the password_env one first", i, err)
		}
	}
}

func TestAmbiguousPairsAreReportedInFieldOrder(t *testing.T) {
	body := "targets:\n  two: {driver: sqlite, verify_ping_url: a, verify_ping_url_env: B, ping_url: a, ping_url_env: B}\n"
	for i := 0; i < 60; i++ {
		_, err := loadTarget(t, body, "two")
		if err == nil || !strings.Contains(err.Error(), "set only one of ping_url and ping_url_env") {
			t.Fatalf("run %d: error = %v", i, err)
		}
	}
}

func TestFailPingURL(t *testing.T) {
	f, err := Load(writeConfig(t, `
targets:
  direct: {driver: sqlite, ping_url: http://x/direct}
  indirect: {driver: sqlite, ping_url_env: P}
  hostile: {driver: sqlite, ping_url_env: "a b;c"}
  broken: {driver: oracle, ping_url: http://x/broken}
  garbled: {driver: sqlite, ping_url: [a], nonsense: 1}
`))
	if err != nil {
		t.Fatal(err)
	}
	get := func(k string) string { return map[string]string{"P": "http://x/indirect"}[k] }
	want := map[string]string{
		"direct": "http://x/direct", "indirect": "http://x/indirect",
		"hostile": "", "broken": "http://x/broken", "garbled": "",
	}
	for _, e := range f.Entries {
		if got := e.FailPingURL(get); got != want[e.Name] {
			t.Errorf("%s = %q, want %q", e.Name, got, want[e.Name])
		}
	}
}

func TestLoadAcceptsTLSFields(t *testing.T) {
	for name, body := range map[string]string{
		"db": "driver: mysql\ntls: {ca: /certs/ca.pem}",
		"fp": "driver: mysql\ntls: {fingerprint: \"AA:BB\"}",
	} {
		tg, err := loadTarget(t, "targets:\n  "+name+": {"+strings.ReplaceAll(strings.ReplaceAll(body, "\n", ", "), "driver: ", "driver: ")+"}\n", name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if tg.Vars["DB_SSL_CA"] == "" && tg.Vars["DB_SSL_FINGERPRINT"] == "" {
			t.Errorf("%s: no TLS setting reached the job: %v", name, tg.Vars)
		}
	}
}
