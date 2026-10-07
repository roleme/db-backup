package job

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/driver"
	"github.com/roleme/db-backup/internal/logx"
	"github.com/roleme/db-backup/internal/ping"
	"github.com/roleme/db-backup/internal/proc"
	"github.com/roleme/db-backup/internal/retention"
)

const PartialMaxAge = 60 * time.Minute

type Job struct {
	cfg      *config.Config
	adapters []driver.Adapter
	store    retention.Store
	ping     ping.Pinger
	now      func() time.Time
}

type unit struct {
	adapter driver.Adapter
	name    string
}

func New(cfg *config.Config, run proc.Runner, p ping.Pinger, now func() time.Time) (*Job, error) {
	adapters, err := driver.ForConfig(cfg, run)
	if err != nil {
		return nil, err
	}
	return &Job{
		cfg:      cfg,
		adapters: adapters,
		store:    retention.Store{Dir: cfg.BackupDir, Policy: cfg.Policy, Now: now},
		ping:     p,
		now:      now,
	}, nil
}

func (j *Job) validate() error {
	for _, a := range j.adapters {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (j *Job) units() []unit {
	var out []unit
	for _, a := range j.adapters {
		for _, n := range a.Units() {
			out = append(out, unit{a, n})
		}
	}
	return out
}

func (j *Job) Check() error {
	if err := j.validate(); err != nil {
		return err
	}
	logx.Infof("config ok")
	return nil
}

func (j *Job) Names() ([]string, error) {
	if err := j.validate(); err != nil {
		return nil, err
	}
	var out []string
	for _, u := range j.units() {
		out = append(out, u.name+u.adapter.Suffix())
	}
	return out, nil
}

func (j *Job) sweep(u unit) {
	entries, err := os.ReadDir(j.cfg.BackupDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-PartialMaxAge)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "."+u.name+".partial.") && !strings.HasPrefix(name, ".sqlite."+u.name+".") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(j.cfg.BackupDir, name))
	}
}

func (j *Job) backupOne(ctx context.Context, u unit) error {
	tmp, err := os.CreateTemp(j.cfg.BackupDir, "."+u.name+".partial.*")
	if err != nil {
		return err
	}
	path := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(path)
		return err
	}
	gz, err := gzip.NewWriterLevel(tmp, j.cfg.GzipLevel)
	if err != nil {
		return fail(err)
	}
	if err := u.adapter.Dump(ctx, u.name, gz); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(path)
		return err
	}
	tables := ""
	if tc, ok := u.adapter.(driver.TableCounter); ok {
		n, err := tc.Tables(ctx, u.name, path)
		if err != nil {
			os.Remove(path)
			return err
		}
		if n == 0 {
			os.Remove(path)
			return fmt.Errorf("dump of %s contains no tables", u.name)
		}
		tables = strconv.Itoa(n)
	}
	if err := j.store.Save(u.name, u.adapter.Suffix(), path, tables); err != nil {
		os.Remove(path)
		return err
	}
	if err := j.store.Prune(u.name, u.adapter.Suffix()); err != nil {
		return err
	}
	logx.Infof("backed up %s", u.name)
	return nil
}

func (j *Job) backupAll(ctx context.Context) error {
	if err := j.validate(); err != nil {
		return err
	}
	units := j.units()
	for _, u := range units {
		j.sweep(u)
	}
	failures := 0
	for _, u := range units {
		if err := j.backupOne(ctx, u); err != nil {
			logx.Errorf("%v", err)
			logx.Errorf("backup of %s failed", u.name)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d backup(s) failed", failures)
	}
	return nil
}

func (j *Job) verifyOne(ctx context.Context, u unit) error {
	latest := filepath.Join(j.cfg.BackupDir, "last", retention.LatestName(u.name, u.adapter.Suffix()))
	if _, err := os.Stat(latest); err != nil {
		return fmt.Errorf("no dump found for %s", u.name)
	}
	want := 0
	if _, ok := u.adapter.(driver.TableCounter); ok {
		target, err := os.Readlink(latest)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(j.cfg.BackupDir, "last", target+".tables"))
		if err != nil || strings.TrimSpace(string(b)) == "" {
			return fmt.Errorf("no table count recorded for %s", target)
		}
		want, err = strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return fmt.Errorf("no table count recorded for %s", target)
		}
	}
	if err := u.adapter.Verify(ctx, u.name, latest, want); err != nil {
		return err
	}
	logx.Infof("verified %s", u.name)
	return nil
}

func (j *Job) verifyAll(ctx context.Context) error {
	if err := j.validate(); err != nil {
		return err
	}
	failures := 0
	for _, u := range j.units() {
		if err := j.verifyOne(ctx, u); err != nil {
			logx.Errorf("%v", err)
			logx.Errorf("verify of %s failed", u.name)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d verification(s) failed", failures)
	}
	return nil
}

func (j *Job) Backup(ctx context.Context) error {
	if err := j.backupAll(ctx); err != nil {
		j.ping.Ping(j.cfg.PingURL, "/fail")
		return err
	}
	j.ping.Ping(j.cfg.PingURL, "")
	return nil
}

func (j *Job) Verify(ctx context.Context) error {
	if err := j.verifyAll(ctx); err != nil {
		j.ping.Ping(j.cfg.VerifyPingURL, "/fail")
		return err
	}
	j.ping.Ping(j.cfg.VerifyPingURL, "")
	return nil
}
