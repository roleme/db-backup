package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTarget(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTargetErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct{ body, want string }{
		"typo":     {"DRIVER=sqlite\nSCHEDLE=@daily\n", "target typo: unknown key SCHEDLE"},
		"secret":   {"DRIVER=postgres\nDB_PASSWORD=hunter2\n", "target secret: unknown key DB_PASSWORD"},
		"dup":      {"DRIVER=sqlite\nDRIVER=mysql\n", "target dup: duplicate key DRIVER"},
		"noeq":     {"DRIVER=sqlite\nJUNK\n", "target noeq: line without '=': JUNK"},
		"both":     {"DRIVER=sqlite\nDB_HOST=a\nDB_HOST_ENV=B\n", "target both: unknown key DB_HOST_ENV"},
		"pingboth": {"DRIVER=sqlite\nHC_PING_URL=a\nHC_PING_URL_ENV=B\n", "target pingboth: set only one of HC_PING_URL and HC_PING_URL_ENV"},
		"pwboth":   {"DRIVER=postgres\nDB_PASSWORD_ENV=A\nDB_PASSWORD_FILE=/x\n", "target pwboth: set only one of DB_PASSWORD_ENV and DB_PASSWORD_FILE"},
		"badsch":   {"DRIVER=sqlite\nSCHEDULE=nonsense\n", "target badsch: invalid schedule in SCHEDULE: nonsense"},
		"badtime":  {"DRIVER=sqlite\nTIMEOUT=0\n", "target badtime: TIMEOUT must be a positive number of seconds"},
	}
	for name, c := range cases {
		writeTarget(t, dir, name, c.body)
		_, err := LoadTarget(dir, name)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want %q", name, err, c.want)
		}
	}
	if _, err := LoadTarget(dir, "bad/name"); err == nil || !strings.Contains(err.Error(), "invalid target name") {
		t.Errorf("bad name error = %v", err)
	}
}

func TestLoadTargetQuotesAndTimeout(t *testing.T) {
	dir := t.TempDir()
	writeTarget(t, dir, "a", "# comment\n\nDRIVER=\"sqlite\"\nSQLITE_PATHS='/data/a.db'\nTIMEOUT=7\n")
	tg, err := LoadTarget(dir, "a")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Vars["DRIVER"] != "sqlite" || tg.Vars["SQLITE_PATHS"] != "/data/a.db" || tg.Timeout.Seconds() != 7 {
		t.Errorf("target = %+v", tg)
	}
}

func TestResolveIsolatesAndIndirects(t *testing.T) {
	dir := t.TempDir()
	writeTarget(t, dir, "one", "DRIVER=sqlite\nDB_PASSWORD_ENV=ONE_PW\nHC_PING_URL_ENV=ONE_URL\nTIMEOUT=5\n")
	tg, err := LoadTarget(dir, "one")
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
	if err == nil || !strings.Contains(err.Error(), "environment variable ONE_PW (from DB_PASSWORD_ENV) is not set") {
		t.Errorf("unset variable error = %v", err)
	}
}

func TestListTargetsSeparatesInvalidNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"good", "also-good", "bad name", ".hidden"} {
		writeTarget(t, dir, n, "DRIVER=sqlite\n")
	}
	valid, invalid := ListTargets(dir)
	if strings.Join(valid, ",") != "also-good,good" || len(invalid) != 1 || invalid[0] != "bad name" {
		t.Errorf("valid=%v invalid=%v", valid, invalid)
	}
}

func TestFailPingURL(t *testing.T) {
	dir := t.TempDir()
	writeTarget(t, dir, "direct", "DRIVER=sqlite\nHC_PING_URL=http://x/direct\n")
	writeTarget(t, dir, "indirect", "DRIVER=sqlite\nHC_PING_URL_ENV=P\n")
	writeTarget(t, dir, "hostile", "DRIVER=sqlite\nHC_PING_URL_ENV=a b;c\n")
	get := func(k string) string { return map[string]string{"P": "http://x/indirect"}[k] }
	if got := FailPingURL(dir, "direct", get); got != "http://x/direct" {
		t.Errorf("direct = %q", got)
	}
	if got := FailPingURL(dir, "indirect", get); got != "http://x/indirect" {
		t.Errorf("indirect = %q", got)
	}
	if got := FailPingURL(dir, "hostile", get); got != "" {
		t.Errorf("hostile = %q", got)
	}
	if got := FailPingURL(dir, "missing", get); got != "" {
		t.Errorf("missing = %q", got)
	}
}
