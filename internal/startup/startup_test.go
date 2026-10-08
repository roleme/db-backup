package startup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roleme/db-backup/internal/proc"
)

type recPinger struct{ got []string }

func (r *recPinger) Ping(base, suffix string) { r.got = append(r.got, base+suffix) }

type env map[string]string

func (e env) get(k string) string { return e[k] }

func newStartup(t *testing.T, e env) (*Startup, *proc.Fake, *recPinger, string) {
	t.Helper()
	root := t.TempDir()
	f := &proc.Fake{}
	p := &recPinger{}
	e["BACKUP_DIR"] = t.TempDir()
	e["PATH"] = "/usr/bin"
	s := &Startup{
		Getenv:      e.get,
		Supercronic: "/usr/local/bin/supercronic",
		ConfigFile:  filepath.Join(root, "config.yaml"),
		Crontab:     filepath.Join(root, "crontab"),
		Snapshot:    filepath.Join(root, "snapshot.yaml"),
		SkippedFile: filepath.Join(root, "skipped"),
		Runner:      f,
		Ping:        p,
		Exec:        func(string, []string, []string) error { return nil },
	}
	return s, f, p, root
}

func addDB(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name+".db")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeConfig(t *testing.T, s *Startup, body string) {
	t.Helper()
	if err := os.WriteFile(s.ConfigFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func okSchedule(f *proc.Fake) {
	f.Handler = func(sp proc.Spec) error { return nil }
}

func TestCentralSkipsBadTargetsAndSignals(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "good")
	s, f, p, _ := newStartup(t, env{"BAD_URL": "http://p/bad"})
	okSchedule(f)
	writeConfig(t, s, `
targets:
  good:
    driver: sqlite
    paths: {good: `+db+`}
    schedule: "0 3 * * *"
    verify_schedule: "30 4 * * 0"
  bad:
    driver: sqlite
    paths: {bad: /nope.db}
    ping_url_env: BAD_URL
`)
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	b, _ := os.ReadFile(s.Crontab)
	want := "0 3 * * * db-backup-run good backup\n30 4 * * 0 db-backup-run good verify\n"
	if string(b) != want {
		t.Errorf("crontab = %q, want %q", b, want)
	}
	if _, err := os.Stat(s.SkippedFile); err != nil {
		t.Error("the skipped marker must exist")
	}
	if strings.Join(p.got, ",") != "http://p/bad/fail" {
		t.Errorf("pings = %v", p.got)
	}
	snap, err := os.ReadFile(s.Snapshot)
	if err != nil {
		t.Fatal("the config must be snapshotted")
	}
	orig, _ := os.ReadFile(s.ConfigFile)
	if string(snap) != string(orig) {
		t.Errorf("snapshot differs from the config: %q", snap)
	}
}

func TestCentralSkipsTargetsTheSchedulerRejects(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "a")
	s, f, _, _ := newStartup(t, env{})
	f.Handler = func(sp proc.Spec) error {
		if sp.Name == "supercronic" {
			b, _ := os.ReadFile(sp.Args[len(sp.Args)-1])
			if strings.Contains(string(b), "99 99 * * *") {
				return os.ErrInvalid
			}
		}
		return nil
	}
	writeConfig(t, s, `
targets:
  a: {driver: sqlite, paths: {a: `+db+`}, schedule: "99 99 * * *"}
  b: {driver: sqlite, paths: {b: `+db+`}}
`)
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	b, _ := os.ReadFile(s.Crontab)
	if strings.Contains(string(b), "db-backup-run a ") {
		t.Errorf("a target with a rejected schedule must be skipped: %q", b)
	}
}

func TestCentralDuplicateDumpNamesStop(t *testing.T) {
	data := t.TempDir()
	a := filepath.Join(data, "x")
	b := filepath.Join(data, "y")
	for _, d := range []string{a, b} {
		_ = os.MkdirAll(d, 0o755)
	}
	dbA, dbB := addDB(t, a, "app"), addDB(t, b, "app")
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	writeConfig(t, s, `
targets:
  one: {driver: sqlite, paths: {app: `+dbA+`}}
  two: {driver: sqlite, paths: {app: `+dbB+`}}
`)
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestCentralWithNoValidTargetStops(t *testing.T) {
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	writeConfig(t, s, "targets:\n  bad: {driver: sqlite, paths: {bad: /nope.db}}\n")
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestBackupOnStartRunsEveryTargetOnce(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "a")
	s, f, _, _ := newStartup(t, env{"BACKUP_ON_START": "TRUE"})
	okSchedule(f)
	writeConfig(t, s, "targets:\n  a: {driver: sqlite, paths: {a: "+db+"}}\n")
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	ran := 0
	for _, c := range f.Calls {
		if c.Spec.Name == "db-backup-run" && strings.Join(c.Spec.Args, " ") == "a backup" {
			ran++
		}
	}
	if ran != 1 {
		t.Errorf("db-backup-run a backup ran %d times", ran)
	}
}

func TestHealthy(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "skipped")
	if !healthyWith(marker, func() bool { return true }) {
		t.Error("alive scheduler and no marker is healthy")
	}
	if healthyWith(marker, func() bool { return false }) {
		t.Error("a missing scheduler is unhealthy")
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if healthyWith(marker, func() bool { return true }) {
		t.Error("a skipped target makes the container unhealthy")
	}
}

func TestUnreadableOrBrokenConfigStops(t *testing.T) {
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("missing config: exit = %d, want 1", code)
	}
	writeConfig(t, s, "targets: [")
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("broken yaml: exit = %d, want 1", code)
	}
}

func TestInvalidTargetNamesAreSkippedWithASignal(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "good")
	s, f, p, _ := newStartup(t, env{})
	okSchedule(f)
	writeConfig(t, s, `
targets:
  good: {driver: sqlite, paths: {good: `+db+`}}
  "bad name": {driver: sqlite, paths: {x: `+db+`}, ping_url: http://p/bad-name}
`)
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(s.SkippedFile); err != nil {
		t.Error("the skipped marker must exist")
	}
	if strings.Join(p.got, ",") != "http://p/bad-name/fail" {
		t.Errorf("pings = %v", p.got)
	}
}

func TestCentralPassesTLSToTheDriver(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "app")
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	writeConfig(t, s, "targets:\n  a: {driver: sqlite, paths: {a: "+db+"}, tls: {ca: /certs/ca.pem}}\n")
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d, want 1: tls.ca must reach the driver, which rejects it for sqlite", code)
	}
}

func TestSupercronicRunsAgainstTheSnapshot(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "a")
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	var gotEnv []string
	s.Exec = func(_ string, _, env []string) error { gotEnv = env; return nil }
	writeConfig(t, s, "targets:\n  a: {driver: sqlite, paths: {a: "+db+"}}\n")
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(strings.Join(gotEnv, "\n"), "CONFIG_FILE="+s.Snapshot) {
		t.Errorf("env = %v", gotEnv)
	}
}
