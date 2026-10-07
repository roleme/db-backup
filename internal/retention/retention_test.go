package retention

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestStampWeekYearBoundaries(t *testing.T) {
	cases := map[string]string{
		"2020-12-31 10:00": "202053",
		"2021-01-03 10:00": "202053",
		"2021-01-04 10:00": "202101",
		"2024-12-30 10:00": "202501",
		"2026-10-07 10:00": "202641",
	}
	for when, want := range cases {
		if got := Stamp(Weekly, at(when)); got != want {
			t.Errorf("Stamp(Weekly, %s) = %s, want %s", when, got, want)
		}
	}
}

func TestStampOtherTiers(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 34, 56, 0, time.UTC)
	for tier, want := range map[Tier]string{Last: "20261007-123456", Daily: "20261007", Monthly: "202610"} {
		if got := Stamp(tier, now); got != want {
			t.Errorf("Stamp(%s) = %s, want %s", tier, got, want)
		}
	}
}

func TestParseWeekStartsOnMonday(t *testing.T) {
	got, err := Parse(Weekly, "202053", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := at("2020-12-28 00:00"); !got.Equal(want) {
		t.Errorf("Parse(Weekly, 202053) = %s, want %s", got, want)
	}
	got, err = Parse(Weekly, "202101", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := at("2021-01-04 00:00"); !got.Equal(want) {
		t.Errorf("Parse(Weekly, 202101) = %s, want %s", got, want)
	}
}

func TestCutoffMonthsCountFromFirstOfMonth(t *testing.T) {
	p := Policy{KeepMonths: 1}
	got := p.cutoff(Monthly, at("2026-03-31 09:00"))
	if want := at("2026-02-01 00:00"); !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}

func TestCutoffDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("tzdata missing")
	}
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, loc)
	got := Policy{KeepDays: 2}.cutoff(Daily, now)
	if want := time.Date(2026, 3, 28, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("cutoff = %s, want %s", got, want)
	}
}
