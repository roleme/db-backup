package startup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/job"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/ping"
	"github.com/roleme/db-backup/internal/proc"
)

type Startup struct {
	Getenv      func(string) string
	Supercronic string
	TargetsDir  string
	Crontab     string
	Snapshot    string
	SkippedFile string
	Runner      proc.Runner
	Ping        ping.Pinger
	Exec        func(path string, argv, env []string) error
}

func (s *Startup) scheduleWorks(ctx context.Context, schedule string) bool {
	f, err := os.CreateTemp("", "dbb-schedule-*")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(schedule + " true\n"); err != nil {
		f.Close()
		return false
	}
	f.Close()
	return s.Runner.Run(ctx, proc.Spec{Name: "supercronic", Args: []string{"-test", f.Name()}, Env: proc.BaseEnv(), Stdout: io.Discard, Stderr: io.Discard}) == nil
}

func (s *Startup) skip(name string) {
	_ = os.WriteFile(s.SkippedFile, nil, 0o600)
	s.Ping.Ping(config.FailPingURL(s.TargetsDir, name, s.Getenv), "/fail")
}

func (s *Startup) describe(name string) (*config.Target, map[string]string, []string, error) {
	t, err := config.LoadTarget(s.TargetsDir, name)
	if err != nil {
		return nil, nil, nil, err
	}
	vars, err := t.Resolve(s.Getenv)
	if err != nil {
		return nil, nil, nil, err
	}
	if bd := s.Getenv("BACKUP_DIR"); bd != "" {
		vars["BACKUP_DIR"] = bd
	}
	cfg, err := config.FromVars(vars)
	if err != nil {
		return nil, nil, nil, err
	}
	j, err := job.New(cfg, s.Runner, s.Ping, time.Now)
	if err != nil {
		return nil, nil, nil, err
	}
	names, err := j.Names()
	if err != nil {
		return nil, nil, nil, err
	}
	return t, vars, names, nil
}

func (s *Startup) Run(ctx context.Context) int {
	_ = os.Remove(s.SkippedFile)
	valid, invalid := config.ListTargets(s.TargetsDir)
	if len(valid)+len(invalid) > 0 {
		return s.central(ctx)
	}
	return s.single(ctx)
}

func (s *Startup) single(ctx context.Context) int {
	schedule := s.Getenv("SCHEDULE")
	if schedule == "" {
		schedule = "@daily"
	}
	if err := config.CheckSchedule("single", "SCHEDULE", schedule); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	verify := s.Getenv("VERIFY_SCHEDULE")
	if verify != "" {
		if err := config.CheckSchedule("single", "VERIFY_SCHEDULE", verify); err != nil {
			logx.Errorf("%v", err)
			return 1
		}
	}
	vars := map[string]string{}
	for _, k := range singleKeys {
		vars[k] = s.Getenv(k)
	}
	cfg, err := config.FromVars(vars)
	if err == nil {
		var j *job.Job
		if j, err = job.New(cfg, s.Runner, s.Ping, time.Now); err == nil {
			err = j.Check()
		}
	}
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	var tab strings.Builder
	tab.WriteString(schedule + " db-backup backup\n")
	if verify != "" {
		tab.WriteString(verify + " db-backup verify\n")
	}
	if err := os.WriteFile(s.Crontab, []byte(tab.String()), 0o644); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	if s.Getenv("BACKUP_ON_START") == "TRUE" {
		_ = s.Runner.Run(ctx, proc.Spec{Name: "db-backup", Args: []string{"backup"}, Env: os.Environ(), Stdout: os.Stdout, Stderr: os.Stderr})
	}
	return s.exec(os.Environ())
}

func (s *Startup) central(ctx context.Context) int {
	valid, invalid := config.ListTargets(s.TargetsDir)
	for _, name := range invalid {
		logx.Errorf("invalid target name '%s', skipping %s", name, filepath.Join(s.TargetsDir, name+".env"))
		_ = os.WriteFile(s.SkippedFile, nil, 0o600)
	}
	_ = os.RemoveAll(s.Snapshot)
	if err := os.MkdirAll(s.Snapshot, 0o755); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	owner := map[string]string{}
	var tab strings.Builder
	var ok []string
	for _, name := range valid {
		t, _, dumps, err := s.describe(name)
		if err != nil {
			logx.Errorf("%v", err)
			logx.Errorf("skipping target %s", name)
			s.skip(name)
			continue
		}
		backup, verify := t.Vars["SCHEDULE"], t.Vars["VERIFY_SCHEDULE"]
		if backup == "" {
			backup = "@daily"
		}
		if !s.scheduleWorks(ctx, backup) || (verify != "" && !s.scheduleWorks(ctx, verify)) {
			logx.Errorf("skipping target %s: the scheduler rejects its schedule", name)
			s.skip(name)
			continue
		}
		for _, d := range dumps {
			if prev, dup := owner[d]; dup {
				logx.Errorf("dump name %s is used by targets %s and %s", d, prev, name)
				return 1
			}
			owner[d] = name
		}
		tab.WriteString(backup + " db-backup-run " + name + " backup\n")
		if verify != "" {
			tab.WriteString(verify + " db-backup-run " + name + " verify\n")
		}
		b, err := os.ReadFile(filepath.Join(s.TargetsDir, name+".env"))
		if err == nil {
			err = os.WriteFile(filepath.Join(s.Snapshot, name+".env"), b, 0o600)
		}
		if err != nil {
			logx.Errorf("%v", err)
			return 1
		}
		ok = append(ok, name)
	}
	if len(ok) == 0 {
		logx.Errorf("no valid targets in %s", s.TargetsDir)
		return 1
	}
	if err := os.WriteFile(s.Crontab, []byte(tab.String()), 0o644); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	env := append(os.Environ(), "TARGETS_DIR="+s.Snapshot)
	if s.Getenv("BACKUP_ON_START") == "TRUE" {
		for _, name := range ok {
			_ = s.Runner.Run(ctx, proc.Spec{Name: "db-backup-run", Args: []string{name, "backup"}, Env: env, Stdout: os.Stdout, Stderr: os.Stderr})
		}
	}
	return s.exec(env)
}

func (s *Startup) exec(env []string) int {
	if err := s.Exec(s.Supercronic, []string{"supercronic", s.Crontab}, env); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	return 0
}

var singleKeys = []string{
	"DRIVER", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_PASSWORD_FILE", "DATABASES",
	"SQLITE_PATHS", "EXTRA_PATHS", "EXTRA_OPTS", "EXCLUDE_TABLE_DATA", "HC_PING_URL", "HC_VERIFY_PING_URL",
	"KEEP_MINS", "KEEP_DAYS", "KEEP_WEEKS", "KEEP_MONTHS", "GZIP_LEVEL", "BACKUP_DIR", "DB_SSL_CA", "DB_SSL_FINGERPRINT",
}
