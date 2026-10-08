package startup

import (
	"context"
	"io"
	"os"
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
	ConfigFile  string
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

func (s *Startup) skip(e config.Entry) {
	_ = os.WriteFile(s.SkippedFile, nil, 0o600)
	s.Ping.Ping(e.FailPingURL(s.Getenv), "/fail")
}

func (s *Startup) describe(t *config.Target) ([]string, error) {
	vars, err := t.Resolve(s.Getenv)
	if err != nil {
		return nil, err
	}
	if bd := s.Getenv("BACKUP_DIR"); bd != "" {
		vars["BACKUP_DIR"] = bd
	}
	cfg, err := config.FromVars(vars)
	if err != nil {
		return nil, err
	}
	j, err := job.New(cfg, s.Runner, s.Ping, time.Now)
	if err != nil {
		return nil, err
	}
	return j.Names()
}

func (s *Startup) Run(ctx context.Context) int {
	_ = os.Remove(s.SkippedFile)
	raw, err := os.ReadFile(s.ConfigFile)
	if err != nil {
		logx.Errorf("config %s is not readable", s.ConfigFile)
		return 1
	}
	f, err := config.Parse(s.ConfigFile, raw)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	owner := map[string]string{}
	var tab strings.Builder
	var ok []string
	for _, e := range f.Entries {
		var dumps []string
		err := e.Err
		if err == nil {
			dumps, err = s.describe(e.Target)
		}
		if err != nil {
			logx.Errorf("%v", err)
			logx.Errorf("skipping target %s", e.Name)
			s.skip(e)
			continue
		}
		backup, verify := e.Target.Vars["SCHEDULE"], e.Target.Vars["VERIFY_SCHEDULE"]
		if backup == "" {
			backup = "@daily"
		}
		if !s.scheduleWorks(ctx, backup) || (verify != "" && !s.scheduleWorks(ctx, verify)) {
			logx.Errorf("skipping target %s: the scheduler rejects its schedule", e.Name)
			s.skip(e)
			continue
		}
		for _, d := range dumps {
			if prev, dup := owner[d]; dup {
				logx.Errorf("dump name %s is used by targets %s and %s", d, prev, e.Name)
				return 1
			}
			owner[d] = e.Name
		}
		tab.WriteString(backup + " db-backup-run " + e.Name + " backup\n")
		if verify != "" {
			tab.WriteString(verify + " db-backup-run " + e.Name + " verify\n")
		}
		ok = append(ok, e.Name)
	}
	if len(ok) == 0 {
		logx.Errorf("no valid targets in %s", s.ConfigFile)
		return 1
	}
	if err := os.WriteFile(s.Snapshot, raw, 0o600); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	if err := os.WriteFile(s.Crontab, []byte(tab.String()), 0o644); err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	env := append(os.Environ(), "CONFIG_FILE="+s.Snapshot)
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
