package job

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/proc"
)

type recordingPinger struct{ got []string }

func (r *recordingPinger) Ping(base, suffix string) { r.got = append(r.got, base+suffix) }

func newJob(t *testing.T, f *proc.Fake, kv ...string) (*Job, *recordingPinger, string) {
	t.Helper()
	bk := t.TempDir()
	data := t.TempDir()
	db := filepath.Join(data, "app.db")
	if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{"DRIVER": "sqlite", "BACKUP_DIR": bk, "SQLITE_PATHS": db, "HC_PING_URL": "http://p/ok", "HC_VERIFY_PING_URL": "http://p/v"}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	cfg, err := config.FromVars(m)
	if err != nil {
		t.Fatal(err)
	}
	pg := &recordingPinger{}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	j, err := New(cfg, f, pg, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return j, pg, bk
}

func sqliteFake(tables string) *proc.Fake {
	return &proc.Fake{Handler: func(s proc.Spec) error {
		joined := strings.Join(s.Args, " ")
		switch {
		case strings.Contains(joined, "VACUUM INTO"):
			for _, a := range s.Args {
				if strings.HasPrefix(a, "VACUUM INTO '") {
					p := strings.TrimSuffix(strings.TrimPrefix(a, "VACUUM INTO '"), "'")
					return os.WriteFile(p, []byte("sqlite-bytes"), 0o600)
				}
			}
		case strings.Contains(joined, "select count(*)") && s.Stdout != nil:
			_, _ = io.WriteString(s.Stdout, tables+"\n")
		case strings.Contains(joined, "integrity_check") && s.Stdout != nil:
			_, _ = io.WriteString(s.Stdout, "ok\n")
		}
		return nil
	}}
}

func sqliteFakeCounts(counts ...string) *proc.Fake {
	i := 0
	return &proc.Fake{Handler: func(s proc.Spec) error {
		joined := strings.Join(s.Args, " ")
		switch {
		case strings.Contains(joined, "VACUUM INTO"):
			for _, a := range s.Args {
				if strings.HasPrefix(a, "VACUUM INTO '") {
					p := strings.TrimSuffix(strings.TrimPrefix(a, "VACUUM INTO '"), "'")
					return os.WriteFile(p, []byte("sqlite-bytes"), 0o600)
				}
			}
		case strings.Contains(joined, "select count(*)") && s.Stdout != nil:
			n := counts[len(counts)-1]
			if i < len(counts) {
				n = counts[i]
			}
			i++
			_, _ = io.WriteString(s.Stdout, n+"\n")
		case strings.Contains(joined, "integrity_check") && s.Stdout != nil:
			_, _ = io.WriteString(s.Stdout, "ok\n")
		}
		return nil
	}}
}

func TestBackupStoresPingsAndSkipsTiersCorrectly(t *testing.T) {
	j, pg, bk := newJob(t, sqliteFake("3"))
	if err := j.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(pg.got, ",") != "http://p/ok" {
		t.Errorf("pings = %v", pg.got)
	}
	for _, p := range []string{"last/app-20261007-120000.db.gz", "daily/app-20261007.db.gz", "last/app-latest.db.gz"} {
		if _, err := os.Lstat(filepath.Join(bk, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	sidecars, _ := filepath.Glob(filepath.Join(bk, "*", "*.tables"))
	if len(sidecars) != 0 {
		t.Errorf("no table-count file must be written: %v", sidecars)
	}
	left, _ := filepath.Glob(filepath.Join(bk, ".*.partial.*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestBackupRejectsADumpWithNoTables(t *testing.T) {
	j, pg, bk := newJob(t, sqliteFake("0"))
	err := j.Backup(context.Background())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Join(pg.got, ",") != "http://p/ok/fail" {
		t.Errorf("pings = %v", pg.got)
	}
	if _, err := os.Lstat(filepath.Join(bk, "last", "app-latest.db.gz")); err == nil {
		t.Error("a dump without tables must not be stored")
	}
	left, _ := filepath.Glob(filepath.Join(bk, ".app.partial.*"))
	if len(left) != 0 {
		t.Errorf("partial left: %v", left)
	}
}

func TestVerifyFailsWhenTheRestoredDumpHasNoTables(t *testing.T) {
	j, pg, _ := newJob(t, sqliteFakeCounts("3", "0"))
	if err := j.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	pg.got = nil
	if err := j.Verify(context.Background()); err == nil {
		t.Fatal("verify must fail when nothing was restored")
	}
	if strings.Join(pg.got, ",") != "http://p/v/fail" {
		t.Errorf("pings = %v", pg.got)
	}
}

func TestVerifyAcceptsADumpWhoseTableCountDiffersFromBackupTime(t *testing.T) {
	j, pg, _ := newJob(t, sqliteFakeCounts("3", "5"))
	if err := j.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	pg.got = nil
	if err := j.Verify(context.Background()); err != nil {
		t.Fatalf("verify compares nothing to a recorded count any more: %v", err)
	}
}

func TestVerifySucceedsAndPings(t *testing.T) {
	j, pg, _ := newJob(t, sqliteFake("3"))
	if err := j.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	pg.got = nil
	if err := j.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(pg.got, ",") != "http://p/v" {
		t.Errorf("pings = %v", pg.got)
	}
}

func TestSweepRemovesOnlyStalePartials(t *testing.T) {
	j, _, bk := newJob(t, sqliteFake("3"))
	old := time.Now().Add(-2 * time.Hour)
	for name, mod := range map[string]time.Time{".app.partial.OLD": old, ".sqlite.app.OLD": old, ".other.partial.OLD": old, ".app.partial.FRESH": time.Now()} {
		p := filepath.Join(bk, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	gone := []string{".app.partial.OLD", ".sqlite.app.OLD"}
	kept := []string{".other.partial.OLD", ".app.partial.FRESH"}
	for _, n := range gone {
		if _, err := os.Lstat(filepath.Join(bk, n)); err == nil {
			t.Errorf("%s should be swept", n)
		}
	}
	for _, n := range kept {
		if _, err := os.Lstat(filepath.Join(bk, n)); err != nil {
			t.Errorf("%s should stay", n)
		}
	}
}

func TestNamesListsDumpFileNames(t *testing.T) {
	j, _, _ := newJob(t, sqliteFake("3"))
	got, err := j.Names()
	if err != nil || strings.Join(got, ",") != "app.db.gz" {
		t.Errorf("Names = %v, %v", got, err)
	}
}
