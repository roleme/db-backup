package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/roleme/db-backup/internal/retention"
)

var (
	nonNegativeRe = regexp.MustCompile(`^[0-9]+$`)
	positiveRe    = regexp.MustCompile(`^[1-9][0-9]*$`)
	gzipRe        = regexp.MustCompile(`^[1-9]$`)
	shortcutRe    = regexp.MustCompile(`^@[a-z]+$`)
	fieldsRe      = regexp.MustCompile(`^[0-9A-Za-z*/,?-]+( [0-9A-Za-z*/,?-]+){4,6}$`)
)

const (
	DefaultBackupDir  = "/backups"
	defaultKeepMins   = 1440
	defaultKeepDays   = 7
	defaultKeepWeeks  = 4
	defaultKeepMonths = 6
	defaultGzipLevel  = 6
)

type Config struct {
	Driver           string
	BackupDir        string
	Policy           retention.Policy
	GzipLevel        int
	ExtraOpts        []string
	ExtraPaths       string
	ExcludeTableData string
	PingURL          string
	VerifyPingURL    string
	vars             map[string]string
}

func (c *Config) Get(key string) string {
	return c.vars[key]
}

var fieldNames = map[string]string{
	"DRIVER": "driver", "DB_HOST": "host", "DB_USER": "user", "DATABASES": "databases",
	"SQLITE_PATHS": "paths", "DB_PASSWORD": "password_file",
}

func fieldName(key string) string {
	if f, ok := fieldNames[key]; ok {
		return f
	}
	return key
}

func (c *Config) Require(names ...string) error {
	for _, n := range names {
		if c.vars[n] == "" {
			return fmt.Errorf("%s is required", fieldName(n))
		}
	}
	return nil
}

func (c *Config) Secret(name string) (string, error) {
	if file := c.vars[name+"_FILE"]; file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("%s is not readable", fieldName(name))
		}
		return strings.NewReplacer("\r", "", "\n", "").Replace(string(b)), nil
	}
	return c.vars[name], nil
}

func SplitList(s string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func checkSchedule(field, value string) error {
	if shortcutRe.MatchString(value) || fieldsRe.MatchString(value) {
		return nil
	}
	return fmt.Errorf("invalid schedule in %s: %s", field, value)
}

func intVar(vars map[string]string, key string, def int, re *regexp.Regexp, msg string) (int, error) {
	raw, ok := vars[key]
	if !ok || raw == "" {
		return def, nil
	}
	if !re.MatchString(raw) {
		return 0, fmt.Errorf("%s %s", key, msg)
	}
	return strconv.Atoi(raw)
}

func writable(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".dbb-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	return os.Remove(name) == nil
}

func FromVars(vars map[string]string) (*Config, error) {
	c := &Config{vars: vars}
	if err := c.Require("DRIVER"); err != nil {
		return nil, err
	}
	switch vars["DRIVER"] {
	case "postgres", "mysql", "sqlite":
		c.Driver = vars["DRIVER"]
	default:
		return nil, fmt.Errorf("driver must be postgres, mysql or sqlite (got '%s')", vars["DRIVER"])
	}
	c.BackupDir = vars["BACKUP_DIR"]
	if c.BackupDir == "" {
		c.BackupDir = DefaultBackupDir
	}
	var err error
	if c.Policy.KeepDays, err = intVar(vars, "KEEP_DAYS", defaultKeepDays, nonNegativeRe, "must be a non-negative integer"); err != nil {
		return nil, err
	}
	if c.Policy.KeepWeeks, err = intVar(vars, "KEEP_WEEKS", defaultKeepWeeks, nonNegativeRe, "must be a non-negative integer"); err != nil {
		return nil, err
	}
	if c.Policy.KeepMonths, err = intVar(vars, "KEEP_MONTHS", defaultKeepMonths, nonNegativeRe, "must be a non-negative integer"); err != nil {
		return nil, err
	}
	if c.Policy.KeepMins, err = intVar(vars, "KEEP_MINS", defaultKeepMins, positiveRe, "must be a positive integer"); err != nil {
		return nil, err
	}
	if c.GzipLevel, err = intVar(vars, "GZIP_LEVEL", defaultGzipLevel, gzipRe, "must be between 1 and 9"); err != nil {
		return nil, err
	}
	if !writable(c.BackupDir) {
		return nil, errors.New("BACKUP_DIR " + c.BackupDir + " is not a writable directory")
	}
	c.ExtraOpts = strings.Fields(vars["EXTRA_OPTS"])
	c.ExtraPaths = vars["EXTRA_PATHS"]
	c.ExcludeTableData = vars["EXCLUDE_TABLE_DATA"]
	c.PingURL = vars["HC_PING_URL"]
	c.VerifyPingURL = vars["HC_VERIFY_PING_URL"]
	return c, nil
}
