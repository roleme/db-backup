package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vars(kv ...string) map[string]string {
	m := map[string]string{"BACKUP_DIR": os.TempDir()}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestFromVarsDefaults(t *testing.T) {
	c, err := FromVars(vars("DRIVER", "sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.KeepMins != 1440 || c.Policy.KeepDays != 7 || c.Policy.KeepWeeks != 4 || c.Policy.KeepMonths != 6 || c.GzipLevel != 6 {
		t.Errorf("defaults wrong: %+v gzip=%d", c.Policy, c.GzipLevel)
	}
}

func TestFromVarsErrors(t *testing.T) {
	cases := []struct {
		kv   []string
		want string
	}{
		{[]string{}, "driver is required"},
		{[]string{"DRIVER", "oracle"}, "driver must be postgres, mysql or sqlite (got 'oracle')"},
		{[]string{"DRIVER", "sqlite", "KEEP_MINS", "0"}, "KEEP_MINS must be a positive integer"},
		{[]string{"DRIVER", "sqlite", "KEEP_DAYS", "abc"}, "KEEP_DAYS must be a non-negative integer"},
		{[]string{"DRIVER", "sqlite", "GZIP_LEVEL", "10"}, "GZIP_LEVEL must be between 1 and 9"},
		{[]string{"DRIVER", "sqlite", "BACKUP_DIR", "/nonexistent/dir"}, "BACKUP_DIR /nonexistent/dir is not a writable directory"},
	}
	for _, c := range cases {
		_, err := FromVars(vars(c.kv...))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("FromVars(%v) error = %v, want %q", c.kv, err, c.want)
		}
	}
}

func TestSecretFromFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(f, []byte("s3cret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := FromVars(vars("DRIVER", "postgres", "DB_PASSWORD_FILE", f))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Secret("DB_PASSWORD")
	if err != nil || got != "s3cret" {
		t.Errorf("Secret = %q, %v", got, err)
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList(" a, b ,,c\nd ")
	if strings.Join(got, "|") != "a|b|c|d" {
		t.Errorf("SplitList = %v", got)
	}
}

func TestCheckSchedule(t *testing.T) {
	for _, ok := range []string{"@daily", "20 1 * * *", "*/5 * * * * *", "0 0 3 * * * 2026"} {
		if err := checkSchedule("schedule", ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "daily", "* * *", "0 1 * * *; touch /x"} {
		err := checkSchedule("schedule", bad)
		if err == nil || !strings.Contains(err.Error(), "invalid schedule in schedule: "+bad) {
			t.Errorf("%q: error = %v", bad, err)
		}
	}
}
