package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/job"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/ping"
	"github.com/roleme/db-backup/internal/proc"
	"github.com/roleme/db-backup/internal/runner"
	"github.com/roleme/db-backup/internal/startup"
)

const (
	defaultConfigFile = "/config/config.yaml"
	defaultSkipped    = "/tmp/dbb-skipped"
	defaultLockDir    = "/tmp/dbb-locks"
	crontabPath       = "/tmp/crontab"
	snapshotFile      = "/tmp/dbb-config.yaml"
)

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	os.Exit(dispatch(filepath.Base(os.Args[0]), os.Args[1:]))
}

func dispatch(name string, args []string) int {
	ctx := context.Background()
	switch name {
	case "db-backup-run":
		return runnerRole(ctx, args)
	case "entrypoint":
		return startupRole(ctx)
	default:
		return jobRole(ctx, args)
	}
}

func jobRole(ctx context.Context, args []string) int {
	if len(args) == 1 && args[0] == "healthcheck" {
		if startup.Healthy(getenvDefault("DBB_SKIPPED", defaultSkipped)) {
			return 0
		}
		return 1
	}
	if len(args) != 1 || !map[string]bool{"backup": true, "verify": true, "check": true, "names": true}[args[0]] {
		fmt.Fprintln(os.Stderr, "usage: db-backup backup|verify|check|names")
		return 2
	}
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	cfg, err := config.FromVars(vars)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	j, err := job.New(cfg, proc.Exec{}, ping.NewHTTP(), time.Now)
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	switch args[0] {
	case "check":
		err = j.Check()
	case "names":
		var names []string
		if names, err = j.Names(); err == nil {
			for _, n := range names {
				fmt.Println(n)
			}
		}
	case "backup":
		err = j.Backup(ctx)
	case "verify":
		err = j.Verify(ctx)
	}
	if err != nil {
		logx.Errorf("%v", err)
		return 1
	}
	return 0
}

func runnerRole(ctx context.Context, args []string) int {
	if len(args) != 2 || !map[string]bool{"backup": true, "verify": true, "check": true, "names": true}[args[1]] {
		fmt.Fprintln(os.Stderr, "usage: db-backup-run NAME backup|verify|check|names")
		return 2
	}
	r := runner.Runner{
		ConfigFile: getenvDefault("CONFIG_FILE", defaultConfigFile),
		LockDir:    getenvDefault("DBB_LOCK_DIR", defaultLockDir),
		Getenv:     os.Getenv,
		Exec:       proc.Exec{NewGroup: true},
		Ping:       ping.NewHTTP(),
	}
	return r.Run(ctx, args[0], args[1])
}

func startupRole(ctx context.Context) int {
	supercronic, err := exec.LookPath("supercronic")
	if err != nil {
		logx.Errorf("supercronic not found")
		return 1
	}
	s := &startup.Startup{
		Getenv:      os.Getenv,
		Supercronic: supercronic,
		ConfigFile:  getenvDefault("CONFIG_FILE", defaultConfigFile),
		Crontab:     crontabPath,
		Snapshot:    snapshotFile,
		SkippedFile: getenvDefault("DBB_SKIPPED", defaultSkipped),
		Runner:      proc.Exec{},
		Ping:        ping.NewHTTP(),
		Exec:        syscall.Exec,
	}
	return s.Run(ctx)
}
