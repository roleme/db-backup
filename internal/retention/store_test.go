package retention

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func touch(t *testing.T, dir, tier, name string) {
	t.Helper()
	p := filepath.Join(dir, tier)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(dir, tier, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, tier, name))
	return err == nil
}

func TestPruneByStampAndUnit(t *testing.T) {
	dir := t.TempDir()
	now := at("2026-10-07 12:00")
	s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepDays: 7, KeepWeeks: 4, KeepMonths: 6}, Now: func() time.Time { return now }}
	files := map[string][]string{
		"last":    {"app-20261006-110000.db.gz", "app-20261006-130000.db.gz", "app-2-20200101-010000.db.gz"},
		"daily":   {"app-20260929.db.gz", "app-20260930.db.gz", "app-2-20200101.db.gz", "app-extra-20200101.db.gz"},
		"weekly":  {"app-202636.db.gz", "app-202637.db.gz", "app-2-202001.db.gz"},
		"monthly": {"app-202603.db.gz", "app-202604.db.gz", "app-2-202001.db.gz"},
	}
	for tier, names := range files {
		for _, n := range names {
			touch(t, dir, tier, n)
		}
	}
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	gone := map[string][]string{
		"last":    {"app-20261006-110000.db.gz"},
		"daily":   {"app-20260929.db.gz"},
		"weekly":  {"app-202636.db.gz"},
		"monthly": {"app-202603.db.gz"},
	}
	kept := map[string][]string{
		"last":    {"app-20261006-130000.db.gz", "app-2-20200101-010000.db.gz"},
		"daily":   {"app-20260930.db.gz", "app-2-20200101.db.gz", "app-extra-20200101.db.gz"},
		"weekly":  {"app-202637.db.gz", "app-2-202001.db.gz"},
		"monthly": {"app-202604.db.gz", "app-2-202001.db.gz"},
	}
	for tier, names := range gone {
		for _, n := range names {
			if exists(dir, tier, n) {
				t.Errorf("%s/%s should be pruned", tier, n)
			}
		}
	}
	for tier, names := range kept {
		for _, n := range names {
			if !exists(dir, tier, n) {
				t.Errorf("%s/%s should be kept", tier, n)
			}
		}
	}
}

func TestPruneIgnoresMtime(t *testing.T) {
	dir := t.TempDir()
	now := at("2026-10-07 12:00")
	s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepDays: 7, KeepWeeks: 4, KeepMonths: 6}, Now: func() time.Time { return now }}
	touch(t, dir, "daily", "app-20260930.db.gz")
	old := now.AddDate(-5, 0, 0)
	if err := os.Chtimes(filepath.Join(dir, "daily", "app-20260930.db.gz"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "daily", "app-20260930.db.gz") {
		t.Error("age must come from the stamp, not the file time")
	}
}

func TestPruneWeekFiftyThree(t *testing.T) {
	dir := t.TempDir()
	now := at("2021-02-15 12:00")
	s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepDays: 7, KeepWeeks: 4, KeepMonths: 6}, Now: func() time.Time { return now }}
	touch(t, dir, "weekly", "app-202053.db.gz")
	touch(t, dir, "weekly", "app-202106.db.gz")
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "weekly", "app-202053.db.gz") {
		t.Error("week 53 of 2020 is 7 weeks old and must be pruned")
	}
	if !exists(dir, "weekly", "app-202106.db.gz") {
		t.Error("current week must be kept")
	}
}

func TestSaveLinks(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepDays: 7, KeepWeeks: 4, KeepMonths: 6}, Now: func() time.Time { return now }}
	tmp := filepath.Join(dir, ".app.partial.1")
	if err := os.WriteFile(tmp, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("app", ".db.gz", tmp); err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(dir, "last", "app-20261007-120000.db.gz")
	if _, err := os.Stat(last + ".tables"); err == nil {
		t.Error("no table-count file must be written")
	}
	li, _ := os.Stat(last)
	for tier, name := range map[string]string{"daily": "app-20261007.db.gz", "weekly": "app-202641.db.gz", "monthly": "app-202610.db.gz"} {
		fi, err := os.Stat(filepath.Join(dir, tier, name))
		if err != nil || !os.SameFile(li, fi) {
			t.Errorf("%s/%s must be a hardlink of the last file (err=%v)", tier, name, err)
		}
	}
	for tier, name := range map[string]string{"last": "app-20261007-120000.db.gz", "daily": "app-20261007.db.gz", "weekly": "app-202641.db.gz", "monthly": "app-202610.db.gz"} {
		target, err := os.Readlink(filepath.Join(dir, tier, "app-latest.db.gz"))
		if err != nil || target != name {
			t.Errorf("%s latest -> %q (err=%v), want %q", tier, target, err, name)
		}
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("the temporary file must be moved away")
	}
}

func TestPruneWeeklyKeepsTheCurrentWeekWhenKeepWeeksIsZero(t *testing.T) {
	for _, day := range []string{"2026-10-05 12:00", "2026-10-06 12:00", "2026-10-11 12:00"} {
		dir := t.TempDir()
		now := at(day)
		s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepWeeks: 0}, Now: func() time.Time { return now }}
		touch(t, dir, "weekly", "app-202641.db.gz")
		touch(t, dir, "weekly", "app-202640.db.gz")
		if err := s.Prune("app", ".db.gz"); err != nil {
			t.Fatal(err)
		}
		if !exists(dir, "weekly", "app-202641.db.gz") {
			t.Errorf("%s: this week's copy must survive KeepWeeks=0", day)
		}
		if exists(dir, "weekly", "app-202640.db.gz") {
			t.Errorf("%s: last week's copy must go with KeepWeeks=0", day)
		}
	}
}

func TestPruneWeeklyKeepsTheSameNumberOnEveryWeekday(t *testing.T) {
	for d := 5; d <= 11; d++ {
		dir := t.TempDir()
		now := time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC)
		s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepWeeks: 4}, Now: func() time.Time { return now }}
		for w := 36; w <= 41; w++ {
			touch(t, dir, "weekly", "app-2026"+strconv.Itoa(w)+".db.gz")
		}
		if err := s.Prune("app", ".db.gz"); err != nil {
			t.Fatal(err)
		}
		kept := 0
		for w := 36; w <= 41; w++ {
			if exists(dir, "weekly", "app-2026"+strconv.Itoa(w)+".db.gz") {
				kept++
			}
		}
		if kept != 5 || exists(dir, "weekly", "app-202636.db.gz") {
			t.Errorf("2026-10-%02d: kept %d weekly copies, want this week plus 4", d, kept)
		}
	}
}
