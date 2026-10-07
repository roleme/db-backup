package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	envRefRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	quotedRe = regexp.MustCompile(`^"(.*)"$|^'(.*)'$`)
)

const DefaultTimeout = 3600 * time.Second

var targetKeys = map[string]bool{
	"DRIVER": true, "DB_HOST": true, "DB_PORT": true, "DB_USER": true,
	"DB_PASSWORD_ENV": true, "DB_PASSWORD_FILE": true, "DATABASES": true,
	"SQLITE_PATHS": true, "EXTRA_PATHS": true, "EXTRA_OPTS": true,
	"EXCLUDE_TABLE_DATA": true, "SCHEDULE": true, "VERIFY_SCHEDULE": true,
	"HC_PING_URL": true, "HC_PING_URL_ENV": true,
	"HC_VERIFY_PING_URL": true, "HC_VERIFY_PING_URL_ENV": true,
	"KEEP_MINS": true, "KEEP_DAYS": true, "KEEP_WEEKS": true, "KEEP_MONTHS": true,
	"GZIP_LEVEL": true, "TIMEOUT": true,
}

type Target struct {
	Name    string
	Vars    map[string]string
	Timeout time.Duration
}

func CheckName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid target name '%s'", name)
	}
	return nil
}

func targetPath(dir, name string) string {
	return filepath.Join(dir, name+".env")
}

func ListTargets(dir string) (valid, invalid []string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.env"))
	sort.Strings(files)
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".env")
		if strings.HasPrefix(name, ".") {
			continue
		}
		if nameRe.MatchString(name) {
			valid = append(valid, name)
		} else {
			invalid = append(invalid, name)
		}
	}
	return valid, invalid
}

func LoadTarget(dir, name string) (*Target, error) {
	if err := CheckName(name); err != nil {
		return nil, err
	}
	path := targetPath(dir, name)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("target %s: %s is not readable", name, path)
	}
	defer f.Close()
	t := &Target{Name: name, Vars: map[string]string{}, Timeout: DefaultTimeout}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		if line != "" && !strings.HasPrefix(line, "#") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("target %s: line without '=': %s", name, line)
			}
			if !targetKeys[key] {
				return nil, fmt.Errorf("target %s: unknown key %s", name, key)
			}
			if _, dup := t.Vars[key]; dup {
				return nil, fmt.Errorf("target %s: duplicate key %s", name, key)
			}
			if m := quotedRe.FindStringSubmatch(value); m != nil {
				value = m[1] + m[2]
			}
			t.Vars[key] = value
		}
		if err != nil {
			break
		}
	}
	for _, key := range sortedKeys(t.Vars) {
		if base, ok := strings.CutSuffix(key, "_ENV"); ok {
			if _, both := t.Vars[base]; both {
				return nil, fmt.Errorf("target %s: set only one of %s and %s", name, base, key)
			}
		}
	}
	_, hasEnv := t.Vars["DB_PASSWORD_ENV"]
	_, hasFile := t.Vars["DB_PASSWORD_FILE"]
	if hasEnv && hasFile {
		return nil, fmt.Errorf("target %s: set only one of DB_PASSWORD_ENV and DB_PASSWORD_FILE", name)
	}
	for _, key := range []string{"SCHEDULE", "VERIFY_SCHEDULE"} {
		if v, ok := t.Vars[key]; ok {
			if err := CheckSchedule("target "+name, key, v); err != nil {
				return nil, err
			}
		}
	}
	if raw, ok := t.Vars["TIMEOUT"]; ok {
		secs, err := strconv.Atoi(raw)
		if err != nil || secs < 1 {
			return nil, fmt.Errorf("target %s: TIMEOUT must be a positive number of seconds", name)
		}
		t.Timeout = time.Duration(secs) * time.Second
	}
	return t, nil
}

func (t *Target) Resolve(getenv func(string) string) (map[string]string, error) {
	out := make(map[string]string, len(t.Vars))
	for _, key := range sortedKeys(t.Vars) {
		value := t.Vars[key]
		switch {
		case key == "TIMEOUT":
		case strings.HasSuffix(key, "_ENV"):
			if !envRefRe.MatchString(value) {
				return nil, fmt.Errorf("%s must name an environment variable", key)
			}
			resolved := getenv(value)
			if resolved == "" {
				return nil, fmt.Errorf("environment variable %s (from %s) is not set", value, key)
			}
			out[strings.TrimSuffix(key, "_ENV")] = resolved
		default:
			out[key] = value
		}
	}
	return out, nil
}

func EnvList(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func FailPingURL(dir, name string, getenv func(string) string) string {
	b, err := os.ReadFile(targetPath(dir, name))
	if err != nil {
		return ""
	}
	var direct, ref string
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "HC_PING_URL="); ok && direct == "" {
			direct = v
		}
		if v, ok := strings.CutPrefix(line, "HC_PING_URL_ENV="); ok && ref == "" {
			ref = v
		}
	}
	if direct != "" {
		return direct
	}
	if envRefRe.MatchString(ref) {
		return getenv(ref)
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
