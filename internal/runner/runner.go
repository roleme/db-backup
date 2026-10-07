package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/ping"
	"github.com/roleme/db-backup/internal/proc"
)

const TimeoutExit = 124

type Runner struct {
	TargetsDir string
	LockDir    string
	Getenv     func(string) string
	Exec       proc.Runner
	Ping       ping.Pinger
}

func (r Runner) Run(ctx context.Context, name, action string) int {
	t, err := config.LoadTarget(r.TargetsDir, name)
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
	if bd := r.Getenv("BACKUP_DIR"); bd != "" {
		env = append(env, "BACKUP_DIR="+bd)
	} else {
		env = append(env, "BACKUP_DIR="+config.DefaultBackupDir)
	}
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
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
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
