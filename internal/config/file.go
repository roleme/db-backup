package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var (
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	envRefRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	yamlLineRe  = regexp.MustCompile(`^line [0-9]+: `)
	yamlTypeRe  = regexp.MustCompile(` in type [^ ]+$`)
	fieldByKey  = map[string]string{"DB_PASSWORD_ENV": "password_env", "HC_PING_URL_ENV": "ping_url_env", "HC_VERIFY_PING_URL_ENV": "verify_ping_url_env"}
	listBreaker = strings.NewReplacer(",", "", "\n", "", "\r", "")
)

const DefaultTimeout = 3600 * time.Second

type Target struct {
	Name    string
	Vars    map[string]string
	Timeout time.Duration
}

type Entry struct {
	Name       string
	Target     *Target
	Err        error
	pingURL    string
	pingURLEnv string
}

type File struct {
	Entries []Entry
}

type keep struct {
	Mins   *int `yaml:"mins"`
	Days   *int `yaml:"days"`
	Weeks  *int `yaml:"weeks"`
	Months *int `yaml:"months"`
}

type tls struct {
	CA          string `yaml:"ca"`
	Fingerprint string `yaml:"fingerprint"`
}

type doc struct {
	Driver           string            `yaml:"driver"`
	Host             string            `yaml:"host"`
	Port             *int              `yaml:"port"`
	User             string            `yaml:"user"`
	PasswordEnv      string            `yaml:"password_env"`
	PasswordFile     string            `yaml:"password_file"`
	TLS              tls               `yaml:"tls"`
	Databases        []string          `yaml:"databases"`
	Paths            map[string]string `yaml:"paths"`
	ExtraPaths       []string          `yaml:"extra_paths"`
	ExtraOpts        []string          `yaml:"extra_opts"`
	ExcludeTableData []string          `yaml:"exclude_table_data"`
	Schedule         string            `yaml:"schedule"`
	VerifySchedule   string            `yaml:"verify_schedule"`
	PingURL          string            `yaml:"ping_url"`
	PingURLEnv       string            `yaml:"ping_url_env"`
	VerifyPingURL    string            `yaml:"verify_ping_url"`
	VerifyPingURLEnv string            `yaml:"verify_ping_url_env"`
	Keep             keep              `yaml:"keep"`
	GzipLevel        *int              `yaml:"gzip_level"`
	Timeout          *int              `yaml:"timeout"`
}

type pingDoc struct {
	PingURL    string `yaml:"ping_url"`
	PingURLEnv string `yaml:"ping_url_env"`
}

func CheckName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid target name '%s'", name)
	}
	return nil
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config %s is not readable", path)
	}
	return Parse(path, b)
}

func Parse(path string, b []byte) (*File, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: the file must be a mapping with a targets key", path)
	}
	var targets *yaml.Node
	top, err := pairs(root.Content[0])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range top {
		if p.name != "targets" {
			return nil, fmt.Errorf("%s: unknown field %s (only targets is allowed)", path, p.name)
		}
		targets = p.value
	}
	if targets == nil || targets.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: targets must be a mapping of target name to settings", path)
	}
	list, err := pairs(targets)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f := &File{}
	for _, p := range list {
		e := Entry{Name: p.name}
		e.pingURL, e.pingURLEnv = lenientPing(p.value)
		if err := CheckName(p.name); err != nil {
			e.Err = err
		} else if t, err := buildTarget(p.name, p.value); err != nil {
			e.Err = err
		} else {
			e.Target = t
		}
		f.Entries = append(f.Entries, e)
	}
	sort.Slice(f.Entries, func(i, j int) bool { return f.Entries[i].Name < f.Entries[j].Name })
	return f, nil
}

func (f *File) Target(name string) (*Target, error) {
	for _, e := range f.Entries {
		if e.Name == name {
			return e.Target, e.Err
		}
	}
	if err := CheckName(name); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("target %s is not defined", name)
}

func (e Entry) FailPingURL(getenv func(string) string) string {
	if e.pingURL != "" {
		return e.pingURL
	}
	if envRefRe.MatchString(e.pingURLEnv) {
		return getenv(e.pingURLEnv)
	}
	return ""
}

type pair struct {
	name  string
	value *yaml.Node
}

func pairs(m *yaml.Node) ([]pair, error) {
	var out []pair
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		name := m.Content[i].Value
		if seen[name] {
			return nil, fmt.Errorf("line %d: %s is already defined", m.Content[i].Line, name)
		}
		seen[name] = true
		out = append(out, pair{name, m.Content[i+1]})
	}
	return out, nil
}

func lenientPing(n *yaml.Node) (string, string) {
	var p pingDoc
	_ = n.Decode(&p)
	return p.PingURL, p.PingURLEnv
}

func decodeStrict(n *yaml.Node) (doc, error) {
	var d doc
	raw, err := yaml.Marshal(n)
	if err != nil {
		return d, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			msgs := make([]string, len(te.Errors))
			for i, m := range te.Errors {
				msgs[i] = yamlTypeRe.ReplaceAllString(yamlLineRe.ReplaceAllString(m, ""), "")
			}
			return d, errors.New(strings.Join(msgs, "; "))
		}
		return d, err
	}
	return d, nil
}

func buildTarget(name string, n *yaml.Node) (*Target, error) {
	d, err := decodeStrict(n)
	if err != nil {
		return nil, fmt.Errorf("target %s: %v", name, err)
	}
	t, err := d.target(name)
	if err != nil {
		return nil, fmt.Errorf("target %s: %v", name, err)
	}
	return t, nil
}

func (d doc) target(name string) (*Target, error) {
	switch d.Driver {
	case "":
		return nil, errors.New("driver is required")
	case "postgres", "mysql", "sqlite":
	default:
		return nil, fmt.Errorf("driver must be postgres, mysql or sqlite (got '%s')", d.Driver)
	}
	for _, c := range [][4]string{
		{"password_env", d.PasswordEnv, "password_file", d.PasswordFile},
		{"ping_url", d.PingURL, "ping_url_env", d.PingURLEnv},
		{"verify_ping_url", d.VerifyPingURL, "verify_ping_url_env", d.VerifyPingURLEnv},
	} {
		if c[1] != "" && c[3] != "" {
			return nil, fmt.Errorf("set only one of %s and %s", c[0], c[2])
		}
	}
	t := &Target{Name: name, Vars: map[string]string{}, Timeout: DefaultTimeout}
	set := func(key, value string) {
		if value != "" {
			t.Vars[key] = value
		}
	}
	set("DRIVER", d.Driver)
	set("DB_HOST", d.Host)
	set("DB_USER", d.User)
	set("DB_PASSWORD_ENV", d.PasswordEnv)
	set("DB_PASSWORD_FILE", d.PasswordFile)
	set("DB_SSL_CA", d.TLS.CA)
	set("DB_SSL_FINGERPRINT", d.TLS.Fingerprint)
	set("HC_PING_URL", d.PingURL)
	set("HC_PING_URL_ENV", d.PingURLEnv)
	set("HC_VERIFY_PING_URL", d.VerifyPingURL)
	set("HC_VERIFY_PING_URL_ENV", d.VerifyPingURLEnv)
	if d.Port != nil {
		set("DB_PORT", strconv.Itoa(*d.Port))
	}
	for _, s := range []struct{ key, field, value string }{
		{"SCHEDULE", "schedule", d.Schedule}, {"VERIFY_SCHEDULE", "verify_schedule", d.VerifySchedule},
	} {
		if s.value == "" {
			continue
		}
		if err := checkSchedule(s.field, s.value); err != nil {
			return nil, err
		}
		t.Vars[s.key] = s.value
	}
	for _, l := range []struct {
		key, field string
		items      []string
		sep        string
	}{
		{"DATABASES", "databases", d.Databases, ","},
		{"EXTRA_PATHS", "extra_paths", d.ExtraPaths, ","},
		{"EXCLUDE_TABLE_DATA", "exclude_table_data", d.ExcludeTableData, ","},
		{"EXTRA_OPTS", "extra_opts", d.ExtraOpts, " "},
	} {
		joined, err := joinList(l.field, l.items, l.sep)
		if err != nil {
			return nil, err
		}
		set(l.key, joined)
	}
	paths, err := joinPaths(d.Paths)
	if err != nil {
		return nil, err
	}
	set("SQLITE_PATHS", paths)
	for _, k := range []struct {
		key, field string
		value      *int
		min        int
		msg        string
	}{
		{"KEEP_MINS", "keep.mins", d.Keep.Mins, 1, "must be a positive integer"},
		{"KEEP_DAYS", "keep.days", d.Keep.Days, 0, "must be a non-negative integer"},
		{"KEEP_WEEKS", "keep.weeks", d.Keep.Weeks, 0, "must be a non-negative integer"},
		{"KEEP_MONTHS", "keep.months", d.Keep.Months, 0, "must be a non-negative integer"},
	} {
		if k.value == nil {
			continue
		}
		if *k.value < k.min {
			return nil, fmt.Errorf("%s %s", k.field, k.msg)
		}
		t.Vars[k.key] = strconv.Itoa(*k.value)
	}
	if d.GzipLevel != nil {
		if *d.GzipLevel < 1 || *d.GzipLevel > 9 {
			return nil, errors.New("gzip_level must be between 1 and 9")
		}
		t.Vars["GZIP_LEVEL"] = strconv.Itoa(*d.GzipLevel)
	}
	if d.Timeout != nil {
		if *d.Timeout < 1 {
			return nil, errors.New("timeout must be a positive number of seconds")
		}
		t.Timeout = time.Duration(*d.Timeout) * time.Second
	}
	return t, nil
}

func joinList(field string, items []string, sep string) (string, error) {
	for _, item := range items {
		switch {
		case strings.TrimSpace(item) == "":
			return "", fmt.Errorf("%s entry must not be empty", field)
		case sep == "," && listBreaker.Replace(item) != item:
			return "", fmt.Errorf("%s entry %s must not contain a comma or a line break", field, item)
		case sep == " " && strings.ContainsAny(item, " \t\r\n"):
			return "", fmt.Errorf("%s entry %s must not contain whitespace", field, item)
		}
	}
	return strings.Join(items, sep), nil
}

func joinPaths(paths map[string]string) (string, error) {
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		if !nameRe.MatchString(name) {
			return "", fmt.Errorf("paths name %s is invalid", name)
		}
		path := paths[name]
		switch {
		case strings.TrimSpace(path) == "":
			return "", fmt.Errorf("paths entry %s must not be empty", name)
		case listBreaker.Replace(path) != path:
			return "", fmt.Errorf("paths entry %s must not contain a comma or a line break", name)
		}
		entries = append(entries, name+"="+path)
	}
	return strings.Join(entries, ","), nil
}

func (t *Target) Resolve(getenv func(string) string) (map[string]string, error) {
	out := make(map[string]string, len(t.Vars))
	for _, key := range sortedKeys(t.Vars) {
		value := t.Vars[key]
		if base, ok := strings.CutSuffix(key, "_ENV"); ok {
			if !envRefRe.MatchString(value) {
				return nil, fmt.Errorf("%s must name an environment variable", fieldByKey[key])
			}
			resolved := getenv(value)
			if resolved == "" {
				return nil, fmt.Errorf("environment variable %s (from %s) is not set", value, fieldByKey[key])
			}
			out[base] = resolved
			continue
		}
		out[key] = value
	}
	return out, nil
}

func EnvList(vars map[string]string) []string {
	keys := sortedKeys(vars)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
