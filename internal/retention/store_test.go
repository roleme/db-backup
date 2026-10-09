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

func newStore(dir string, now time.Time) Store {
	return Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepDays: 7, KeepWeeks: 4, KeepMonths: 6}, Now: func() time.Time { return now }}
}

func writeTmp(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPruneByStampAndUnit(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	s := newStore(dir, at("2026-10-07 12:00"))
	files := map[string][]string{
		"last":    {"app-20261006-110000.db.gz", "app-20261006-130000.db.gz", "app-2-20200101-010000.db.gz"},
		"daily":   {"app-20260929.db.gz", "app-20260930.db.gz", "app-2-20200101.db.gz", "app-extra-20200101.db.gz"},
		"weekly":  {"app-202636.db.gz", "app-202637.db.gz", "app-2-202001.db.gz"},
		"monthly": {"app-202603.db.gz", "app-202604.db.gz", "app-2-202001.db.gz"},
	}
	for tier, names := range files {
		for _, n := range names {
			touch(t, app, tier, n)
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
			if exists(app, tier, n) {
				t.Errorf("%s/%s should be pruned", tier, n)
			}
		}
	}
	for tier, names := range kept {
		for _, n := range names {
			if !exists(app, tier, n) {
				t.Errorf("%s/%s should be kept", tier, n)
			}
		}
	}
}

func TestPruneIgnoresMtime(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	now := at("2026-10-07 12:00")
	s := newStore(dir, now)
	touch(t, app, "daily", "app-20260930.db.gz")
	old := now.AddDate(-5, 0, 0)
	if err := os.Chtimes(filepath.Join(app, "daily", "app-20260930.db.gz"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	if !exists(app, "daily", "app-20260930.db.gz") {
		t.Error("age must come from the stamp, not the file time")
	}
}

func TestPruneWeekFiftyThree(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	s := newStore(dir, at("2021-02-15 12:00"))
	touch(t, app, "weekly", "app-202053.db.gz")
	touch(t, app, "weekly", "app-202106.db.gz")
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	if exists(app, "weekly", "app-202053.db.gz") {
		t.Error("week 53 of 2020 is 7 weeks old and must be pruned")
	}
	if !exists(app, "weekly", "app-202106.db.gz") {
		t.Error("current week must be kept")
	}
}

func TestSaveLinksAndPointer(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	tmp := writeTmp(t, dir, ".app.partial.1")
	if err := s.Save("app", ".db.gz", tmp); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "app")
	li, err := os.Stat(filepath.Join(base, "last", "app-20261007-120000.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	for tier, name := range map[string]string{"daily": "app-20261007.db.gz", "weekly": "app-202641.db.gz", "monthly": "app-202610.db.gz"} {
		fi, err := os.Stat(filepath.Join(base, tier, name))
		if err != nil || !os.SameFile(li, fi) {
			t.Errorf("%s/%s must be a hardlink of the last file (err=%v)", tier, name, err)
		}
	}
	if s.Latest("app", ".db.gz") != filepath.Join(base, "latest.db.gz") {
		t.Errorf("Latest = %s", s.Latest("app", ".db.gz"))
	}
	target, err := os.Readlink(s.Latest("app", ".db.gz"))
	if err != nil || target != "last/app-20261007-120000.db.gz" {
		t.Errorf("pointer -> %q (err=%v)", target, err)
	}
	if _, err := os.Stat(s.Latest("app", ".db.gz")); err != nil {
		t.Errorf("the pointer must resolve: %v", err)
	}
	for _, tier := range []string{"last", "daily", "weekly", "monthly"} {
		if _, err := os.Lstat(filepath.Join(base, tier, "app-latest.db.gz")); err == nil {
			t.Errorf("%s must not hold a latest link", tier)
		}
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("the temporary file must be moved away")
	}
}

func TestSavePointerFollowsTheNewestDump(t *testing.T) {
	dir := t.TempDir()
	first := newStore(dir, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	second := newStore(dir, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	if err := first.Save("app", ".db.gz", writeTmp(t, dir, ".a1")); err != nil {
		t.Fatal(err)
	}
	if err := second.Save("app", ".db.gz", writeTmp(t, dir, ".a2")); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(second.Latest("app", ".db.gz"))
	if err != nil || target != "last/app-20261008-120000.db.gz" {
		t.Errorf("pointer -> %q (err=%v)", target, err)
	}
}

func TestSaveKeepsUnitsApart(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if err := s.Save("alpha", ".sql.gz", writeTmp(t, dir, ".n1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("beta", ".sql.gz", writeTmp(t, dir, ".f1")); err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"alpha", "beta"} {
		if _, err := os.Stat(s.Latest(unit, ".sql.gz")); err != nil {
			t.Errorf("%s pointer: %v", unit, err)
		}
	}
	touch(t, filepath.Join(dir, "alpha"), "daily", "alpha-20200101.sql.gz")
	if err := s.Prune("beta", ".sql.gz"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "alpha"), "daily", "alpha-20200101.sql.gz") {
		t.Error("pruning one unit must not touch another")
	}
}

func TestSaveRejectsUnitNamesThatEscape(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "../x"} {
		dir := t.TempDir()
		s := newStore(dir, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
		tmp := writeTmp(t, dir, ".bad.partial.1")
		if err := s.Save(bad, ".db.gz", tmp); err == nil {
			t.Errorf("Save(%q) must fail", bad)
		}
		if err := s.Prune(bad, ".db.gz"); err == nil {
			t.Errorf("Prune(%q) must fail", bad)
		}
		if _, err := os.Stat(tmp); err != nil {
			t.Errorf("%q: the temporary file must stay for the caller to remove: %v", bad, err)
		}
	}
}

func TestPruneNeverTouchesLegacyFlatTiers(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	touch(t, dir, "last", "app-20200101-010000.db.gz")
	touch(t, dir, "daily", "app-20200101.db.gz")
	if err := s.Save("app", ".db.gz", writeTmp(t, dir, ".app.partial.1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune("app", ".db.gz"); err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "last", "app-20200101-010000.db.gz") || !exists(dir, "daily", "app-20200101.db.gz") {
		t.Error("files in the old flat tiers must be left alone")
	}
}

func TestPruneWeeklyKeepsTheCurrentWeekWhenKeepWeeksIsZero(t *testing.T) {
	for _, day := range []string{"2026-10-05 12:00", "2026-10-06 12:00", "2026-10-11 12:00"} {
		dir := t.TempDir()
		app := filepath.Join(dir, "app")
		s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepWeeks: 0}, Now: func() time.Time { return at(day) }}
		touch(t, app, "weekly", "app-202641.db.gz")
		touch(t, app, "weekly", "app-202640.db.gz")
		if err := s.Prune("app", ".db.gz"); err != nil {
			t.Fatal(err)
		}
		if !exists(app, "weekly", "app-202641.db.gz") {
			t.Errorf("%s: this week's copy must survive KeepWeeks=0", day)
		}
		if exists(app, "weekly", "app-202640.db.gz") {
			t.Errorf("%s: last week's copy must go with KeepWeeks=0", day)
		}
	}
}

func TestPruneWeeklyKeepsTheSameNumberOnEveryWeekday(t *testing.T) {
	for d := 5; d <= 11; d++ {
		dir := t.TempDir()
		app := filepath.Join(dir, "app")
		now := time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC)
		s := Store{Dir: dir, Policy: Policy{KeepMins: 1440, KeepWeeks: 4}, Now: func() time.Time { return now }}
		for w := 36; w <= 41; w++ {
			touch(t, app, "weekly", "app-2026"+strconv.Itoa(w)+".db.gz")
		}
		if err := s.Prune("app", ".db.gz"); err != nil {
			t.Fatal(err)
		}
		kept := 0
		for w := 36; w <= 41; w++ {
			if exists(app, "weekly", "app-2026"+strconv.Itoa(w)+".db.gz") {
				kept++
			}
		}
		if kept != 5 || exists(app, "weekly", "app-202636.db.gz") {
			t.Errorf("2026-10-%02d: kept %d weekly copies, want this week plus 4", d, kept)
		}
	}
}
