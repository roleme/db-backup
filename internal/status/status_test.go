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

	e, ok := Read(dir, "alpha", "backup")
	if !ok {
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

	e, _ := Read(dir, "alpha", "verify")
	if !e.OK || e.LastOK != 3_000 {
		t.Errorf("entry = %+v", e)
	}
}

func TestReadMissingOrBrokenEntry(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Read(dir, "nope", "backup"); ok {
		t.Error("a missing entry must not be reported")
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Dir, "bad.backup.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(dir, "bad", "backup"); ok {
		t.Error("a broken entry must not be reported")
	}
}

func TestRecordKeepsActionsAndTargetsApart(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, "alpha", "backup", true, time.Unix(1, 0))
	if _, ok := Read(dir, "alpha", "verify"); ok {
		t.Error("a backup entry must not answer for verify")
	}
	if _, ok := Read(dir, "beta", "backup"); ok {
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
