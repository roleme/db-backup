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
		TargetsDir:  filepath.Join(root, "targets.d"),
		Crontab:     filepath.Join(root, "crontab"),
		Snapshot:    filepath.Join(root, "snapshot"),
		SkippedFile: filepath.Join(root, "skipped"),
		Runner:      f,
		Ping:        p,
		Exec:        func(string, []string, []string) error { return nil },
	}
	if err := os.MkdirAll(s.TargetsDir, 0o755); err != nil {
		t.Fatal(err)
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

func target(t *testing.T, s *Startup, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.TargetsDir, name+".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func okSchedule(f *proc.Fake) {
	f.Handler = func(sp proc.Spec) error { return nil }
}

func TestSingleModeWritesTheCrontab(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "app")
	s, f, _, _ := newStartup(t, env{"DRIVER": "sqlite", "SQLITE_PATHS": db, "SCHEDULE": "0 3 * * *", "VERIFY_SCHEDULE": "30 4 * * 0"})
	okSchedule(f)
	if code := s.Run(context.Background()); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	b, _ := os.ReadFile(s.Crontab)
	want := "0 3 * * * db-backup backup\n30 4 * * 0 db-backup verify\n"
	if string(b) != want {
		t.Errorf("crontab = %q, want %q", b, want)
	}
}

func TestSingleModeBadConfigExitsOne(t *testing.T) {
	s, _, _, _ := newStartup(t, env{"DRIVER": "sqlite"})
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestCentralSkipsBadTargetsAndSignals(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "good")
	s, f, p, _ := newStartup(t, env{"BAD_URL": "http://p/bad"})
	okSchedule(f)
	target(t, s, "good", "DRIVER=sqlite\nSQLITE_PATHS="+db+"\nSCHEDULE=0 3 * * *\nVERIFY_SCHEDULE=30 4 * * 0\n")
	target(t, s, "bad", "DRIVER=sqlite\nSQLITE_PATHS=/nope.db\nHC_PING_URL_ENV=BAD_URL\n")
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
	if _, err := os.Stat(filepath.Join(s.Snapshot, "good.env")); err != nil {
		t.Error("the valid target must be snapshotted")
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
	target(t, s, "a", "DRIVER=sqlite\nSQLITE_PATHS="+db+"\nSCHEDULE=99 99 * * *\n")
	target(t, s, "b", "DRIVER=sqlite\nSQLITE_PATHS="+db+"\n")
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
	target(t, s, "one", "DRIVER=sqlite\nSQLITE_PATHS="+dbA+"\n")
	target(t, s, "two", "DRIVER=sqlite\nSQLITE_PATHS="+dbB+"\n")
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestCentralWithNoValidTargetStops(t *testing.T) {
	s, f, _, _ := newStartup(t, env{})
	okSchedule(f)
	target(t, s, "bad", "DRIVER=sqlite\nSQLITE_PATHS=/nope.db\n")
	if code := s.Run(context.Background()); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestBackupOnStartRunsEveryTargetOnce(t *testing.T) {
	data := t.TempDir()
	db := addDB(t, data, "a")
	s, f, _, _ := newStartup(t, env{"BACKUP_ON_START": "TRUE"})
	okSchedule(f)
	target(t, s, "a", "DRIVER=sqlite\nSQLITE_PATHS="+db+"\n")
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
