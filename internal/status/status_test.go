package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordSuccessThenFailureKeepsTheLastSuccess(t *testing.T) {
	dir := t.TempDir()
	t1 := time.Unix(1_000, 0)
	t2 := time.Unix(2_000, 0)

	if err := Record(dir, "alpha", "backup", true, t1); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, "alpha", "backup", false, t2); err != nil {
		t.Fatal(err)
	}

	e, ok, err := Read(dir, "alpha", "backup")
	if err != nil || !ok {
		t.Fatal("entry missing")
	}
	if e.OK || e.At != t2.Unix() || e.LastOK != t1.Unix() {
		t.Errorf("entry = %+v, want a failed run at %d with last success %d", e, t2.Unix(), t1.Unix())
	}
}

func TestRecordSuccessUpdatesTheLastSuccess(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, "alpha", "verify", false, time.Unix(1_000, 0))
	_ = Record(dir, "alpha", "verify", true, time.Unix(3_000, 0))

	e, _, _ := Read(dir, "alpha", "verify")
	if !e.OK || e.LastOK != 3_000 {
		t.Errorf("entry = %+v", e)
	}
}

func TestReadMissingOrBrokenEntry(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Read(dir, "nope", "backup"); ok || err != nil {
		t.Errorf("a missing entry is neither found nor an error: ok=%v err=%v", ok, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Dir, "bad.backup.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Read(dir, "bad", "backup"); ok || err == nil {
		t.Errorf("a broken entry is an error, not an entry: ok=%v err=%v", ok, err)
	}
}

func TestRecordKeepsActionsAndTargetsApart(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, "alpha", "backup", true, time.Unix(1, 0))
	if _, ok, _ := Read(dir, "alpha", "verify"); ok {
		t.Error("a backup entry must not answer for verify")
	}
	if _, ok, _ := Read(dir, "beta", "backup"); ok {
		t.Error("another target's entry must not answer")
	}
}

func TestRecordLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, "alpha", "backup", true, time.Unix(1, 0))
	entries, _ := os.ReadDir(filepath.Join(dir, Dir))
	if len(entries) != 1 {
		t.Errorf("status dir holds %d files, want 1", len(entries))
	}
}

func TestRecordSweepsOnlyStaleTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, Dir)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(sd, ".alpha.backup.stale")
	fresh := filepath.Join(sd, ".alpha.backup.fresh")
	other := filepath.Join(sd, ".beta.backup.stale")
	for _, f := range []string{stale, fresh, other} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, f := range []string{stale, other} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := Record(dir, "alpha", "backup", true, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a stale temporary file of this target must be removed")
	}
	for _, f := range []string{fresh, other} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s must stay: %v", filepath.Base(f), err)
		}
	}
}
