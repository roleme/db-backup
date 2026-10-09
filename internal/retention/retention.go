package retention

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

type Tier string

const (
	Last    Tier = "last"
	Daily   Tier = "daily"
	Weekly  Tier = "weekly"
	Monthly Tier = "monthly"
)

var Tiers = []Tier{Last, Daily, Weekly, Monthly}

const (
	layoutLast  = "20060102-150405"
	layoutDay   = "20060102"
	layoutMonth = "200601"
)

type Policy struct {
	KeepMins   int
	KeepDays   int
	KeepWeeks  int
	KeepMonths int
}

func Stamp(t Tier, at time.Time) string {
	switch t {
	case Last:
		return at.Format(layoutLast)
	case Daily:
		return at.Format(layoutDay)
	case Weekly:
		year, week := at.ISOWeek()
		return fmt.Sprintf("%04d%02d", year, week)
	default:
		return at.Format(layoutMonth)
	}
}

func FileName(unit string, t Tier, at time.Time, suffix string) string {
	return unit + "-" + Stamp(t, at) + suffix
}

func LatestName(suffix string) string {
	return "latest" + suffix
}

func pattern(unit string, t Tier, suffix string) *regexp.Regexp {
	digits := `\d{6}`
	switch t {
	case Last:
		digits = `\d{8}-\d{6}`
	case Daily:
		digits = `\d{8}`
	}
	return regexp.MustCompile(`^` + regexp.QuoteMeta(unit) + `-(` + digits + `)` + regexp.QuoteMeta(suffix) + `$`)
}

func Parse(t Tier, stamp string, loc *time.Location) (time.Time, error) {
	switch t {
	case Last:
		return time.ParseInLocation(layoutLast, stamp, loc)
	case Daily:
		return time.ParseInLocation(layoutDay, stamp, loc)
	case Weekly:
		year, err := strconv.Atoi(stamp[:4])
		if err != nil {
			return time.Time{}, err
		}
		week, err := strconv.Atoi(stamp[4:])
		if err != nil {
			return time.Time{}, err
		}
		return isoWeekStart(year, week, loc), nil
	default:
		return time.ParseInLocation(layoutMonth, stamp, loc)
	}
}

func isoWeekStart(year, week int, loc *time.Location) time.Time {
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, loc)
	sinceMonday := (int(jan4.Weekday()) + 6) % 7
	return jan4.AddDate(0, 0, -sinceMonday+(week-1)*7)
}

func (p Policy) cutoff(t Tier, now time.Time) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch t {
	case Last:
		return now.Add(-time.Duration(p.KeepMins) * time.Minute)
	case Daily:
		return day.AddDate(0, 0, -p.KeepDays)
	case Weekly:
		year, week := now.ISOWeek()
		return isoWeekStart(year, week, now.Location()).AddDate(0, 0, -p.KeepWeeks*7)
	default:
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return first.AddDate(0, -p.KeepMonths, 0)
	}
}
