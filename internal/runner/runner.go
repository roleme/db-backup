package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/ping"
	"github.com/roleme/db-backup/internal/proc"
	"github.com/roleme/db-backup/internal/status"
)

const TimeoutExit = 124

type Runner struct {
	ConfigFile string
	LockDir    string
	Getenv     func(string) string
	Exec       proc.Runner
	Ping       ping.Pinger
	Now        func() time.Time
}

func (r Runner) Run(ctx context.Context, name, action string) int {
	f, err := config.Load(r.ConfigFile)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	t, err := f.Target(name)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	vars, err := t.Resolve(r.Getenv)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	env := []string{"PATH=" + r.Getenv("PATH")}
	if tz := r.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	backupDir := r.Getenv("BACKUP_DIR")
	if backupDir == "" {
		backupDir = config.DefaultBackupDir
	}
	env = append(env, "BACKUP_DIR="+backupDir)
	env = append(env, config.EnvList(vars)...)

	unlock, err := r.lock(name)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	defer unlock()

	runCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	err = r.Exec.Run(runCtx, proc.Spec{Name: "db-backup", Args: []string{action}, Env: env, Stdout: os.Stdout, Stderr: os.Stderr})
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	r.record(backupDir, name, action, err == nil && !timedOut)
	if timedOut {
		logx.Errorf("target %s %s timed out after %ds", name, action, int(t.Timeout.Seconds()))
		switch action {
		case "backup":
			r.Ping.Ping(vars["HC_PING_URL"], "/fail")
		case "verify":
			r.Ping.Ping(vars["HC_VERIFY_PING_URL"], "/fail")
		}
		return TimeoutExit
	}
	return proc.ExitCode(err)
}

func (r Runner) record(dir, name, action string, ok bool) {
	if action != "backup" && action != "verify" {
		return
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	if err := status.Record(dir, name, action, ok, now()); err != nil {
		logx.Warnf("cannot record the %s result of %s: %v", action, name, err)
	}
}

func (r Runner) lock(name string) (func(), error) {
	if err := os.MkdirAll(r.LockDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(r.LockDir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
