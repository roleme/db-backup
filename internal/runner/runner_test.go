package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roleme/db-backup/internal/proc"
	"github.com/roleme/db-backup/internal/status"
)

type recPinger struct{ got []string }

func (r *recPinger) Ping(base, suffix string) { r.got = append(r.got, base+suffix) }

func setup(t *testing.T, body string) (Runner, *proc.Fake, *recPinger) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("targets:\n  one: {"+body+"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &proc.Fake{}
	p := &recPinger{}
	env := map[string]string{"ONE_PW": "secret-one", "TWO_PW": "secret-two", "BACKUP_DIR": "/backups", "PATH": "/usr/bin"}
	return Runner{
		ConfigFile: path,
		LockDir:    t.TempDir(),
		Getenv:     func(k string) string { return env[k] },
		Exec:       f,
		Ping:       p,
	}, f, p
}

func TestRunStartsDbBackupWithAnIsolatedEnvironment(t *testing.T) {
	r, f, _ := setup(t, "driver: sqlite, password_env: ONE_PW, timeout: 5")
	if code := r.Run(context.Background(), "one", "backup"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	c := f.Calls[0].Spec
	if c.Name != "db-backup" || strings.Join(c.Args, " ") != "backup" {
		t.Errorf("call = %s %v", c.Name, c.Args)
	}
	env := strings.Join(c.Env, "\n")
	if !strings.Contains(env, "DB_PASSWORD=secret-one") || !strings.Contains(env, "BACKUP_DIR=/backups") {
		t.Errorf("env = %q", env)
	}
	for _, leak := range []string{"secret-two", "TWO_PW", "ONE_PW", "TIMEOUT"} {
		if strings.Contains(env, leak) {
			t.Errorf("%q leaked into %q", leak, env)
		}
	}
}

func TestRunRejectsBadTargetsWithExitOne(t *testing.T) {
	r, _, _ := setup(t, "driver: sqlite, schedle: x")
	if code := r.Run(context.Background(), "one", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
	if code := r.Run(context.Background(), "bad/name", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
	r2, _, _ := setup(t, "driver: sqlite, password_env: NOT_SET")
	if code := r2.Run(context.Background(), "one", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestRunPassesThroughTheChildExitCode(t *testing.T) {
	r, f, _ := setup(t, "driver: sqlite")
	f.Handler = func(proc.Spec) error { return exitErr(t, 7) }
	if code := r.Run(context.Background(), "one", "backup"); code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

func TestRunTimeoutExits124AndPingsFail(t *testing.T) {
	r, _, p := setup(t, "driver: sqlite, timeout: 1, ping_url: http://p/ok, verify_ping_url: http://p/v")
	r.Exec = blockUntilCancelled{}
	if code := r.Run(context.Background(), "one", "backup"); code != 124 {
		t.Fatalf("exit = %d, want 124", code)
	}
	if strings.Join(p.got, ",") != "http://p/ok/fail" {
		t.Errorf("pings = %v", p.got)
	}
	p.got = nil
	if code := r.Run(context.Background(), "one", "verify"); code != 124 {
		t.Fatalf("exit = %d, want 124", code)
	}
	if strings.Join(p.got, ",") != "http://p/v/fail" {
		t.Errorf("pings = %v", p.got)
	}
}

type blockUntilCancelled struct{}

func (blockUntilCancelled) Run(ctx context.Context, _ proc.Spec) error {
	<-ctx.Done()
	return ctx.Err()
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	return exec.CommandContext(t.Context(), "sh", "-c", "exit "+strconv.Itoa(code)).Run()
}

type counting struct {
	active, max int32
	mu          sync.Mutex
}

func (c *counting) Run(_ context.Context, _ proc.Spec) error {
	now := atomic.AddInt32(&c.active, 1)
	c.mu.Lock()
	if now > c.max {
		c.max = now
	}
	c.mu.Unlock()
	time.Sleep(150 * time.Millisecond)
	atomic.AddInt32(&c.active, -1)
	return nil
}

func TestRunsOfOneTargetAreSerialised(t *testing.T) {
	r, _, _ := setup(t, "driver: sqlite")
	c := &counting{}
	r.Exec = c
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(context.Background(), "one", "backup")
		}()
	}
	wg.Wait()
	if c.max != 1 {
		t.Errorf("up to %d runs of one target overlapped", c.max)
	}
}

func withBackupDir(t *testing.T, r Runner) (Runner, string) {
	t.Helper()
	dir := t.TempDir()
	prev := r.Getenv
	r.Getenv = func(k string) string {
		if k == "BACKUP_DIR" {
			return dir
		}
		return prev(k)
	}
	r.Now = func() time.Time { return time.Unix(5_000, 0) }
	return r, dir
}

func TestRunRecordsTheOutcomeOfBackupAndVerify(t *testing.T) {
	r, f, _ := setup(t, "driver: sqlite")
	r, dir := withBackupDir(t, r)

	if code := r.Run(context.Background(), "one", "backup"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	e, ok, _ := status.Read(dir, "one", "backup")
	if !ok || !e.OK || e.At != 5_000 || e.LastOK != 5_000 {
		t.Errorf("backup entry = %+v ok=%v", e, ok)
	}

	f.Handler = func(proc.Spec) error { return exitErr(t, 7) }
	if code := r.Run(context.Background(), "one", "verify"); code != 7 {
		t.Fatalf("exit = %d", code)
	}
	e, ok, _ = status.Read(dir, "one", "verify")
	if !ok || e.OK || e.LastOK != 0 {
		t.Errorf("verify entry = %+v ok=%v", e, ok)
	}
}

func TestRunRecordsATimeoutAsAFailure(t *testing.T) {
	r, _, _ := setup(t, "driver: sqlite, timeout: 1")
	r, dir := withBackupDir(t, r)
	r.Exec = blockUntilCancelled{}

	if code := r.Run(context.Background(), "one", "backup"); code != 124 {
		t.Fatalf("exit = %d", code)
	}
	if e, ok, _ := status.Read(dir, "one", "backup"); !ok || e.OK {
		t.Errorf("entry = %+v ok=%v, want a failed run", e, ok)
	}
}

func TestRunDoesNotRecordCheckOrNames(t *testing.T) {
	r, _, _ := setup(t, "driver: sqlite")
	r, dir := withBackupDir(t, r)

	r.Run(context.Background(), "one", "check")
	r.Run(context.Background(), "one", "names")
	for _, action := range []string{"check", "names", "backup", "verify"} {
		if _, ok, _ := status.Read(dir, "one", action); ok {
			t.Errorf("%s must not leave an entry", action)
		}
	}
}

func TestRunPassesTheProxySettingsToTheChild(t *testing.T) {
	r, f, _ := setup(t, "driver: sqlite")
	prev := r.Getenv
	extra := map[string]string{"HTTPS_PROXY": "http://proxy:3128", "no_proxy": "db,localhost", "OTHER_SECRET": "x"}
	r.Getenv = func(k string) string {
		if v, ok := extra[k]; ok {
			return v
		}
		return prev(k)
	}
	if code := r.Run(context.Background(), "one", "backup"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	env := strings.Join(f.Calls[0].Spec.Env, "\n")
	for _, want := range []string{"HTTPS_PROXY=http://proxy:3128", "no_proxy=db,localhost"} {
		if !strings.Contains(env, want) {
			t.Errorf("child env lacks %s: %q", want, env)
		}
	}
	if strings.Contains(env, "OTHER_SECRET") || strings.Contains(env, "HTTP_PROXY=") {
		t.Errorf("only the proxy settings that are set may be passed: %q", env)
	}
}

func TestRunRecordsAFailureWhenTheRunnerFailsBeforeStartingTheJob(t *testing.T) {
	r, _, _ := setup(t, "driver: sqlite, password_env: NOT_SET")
	r, dir := withBackupDir(t, r)

	if code := r.Run(context.Background(), "one", "backup"); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if e, ok, _ := status.Read(dir, "one", "backup"); !ok || e.OK {
		t.Errorf("entry = %+v ok=%v, want a failed run", e, ok)
	}
	r.Run(context.Background(), "one", "check")
	if _, ok, _ := status.Read(dir, "one", "check"); ok {
		t.Error("check must not leave an entry")
	}
}

func TestRunPingsFailWhenTheRunnerFailsBeforeStartingTheJob(t *testing.T) {
	cases := []struct {
		action, body, want string
	}{
		{"backup", "driver: sqlite, password_env: NOT_SET, ping_url: https://monitor.test/b", "https://monitor.test/b/fail"},
		{"verify", "driver: sqlite, password_env: NOT_SET, verify_ping_url: https://monitor.test/v", "https://monitor.test/v/fail"},
		{"backup", "driver: sqlite, password_env: NOT_SET, ping_url_env: ONE_PING", "https://monitor.test/env/fail"},
	}
	for _, c := range cases {
		r, _, p := setup(t, c.body)
		get := r.Getenv
		r.Getenv = func(k string) string {
			if k == "ONE_PING" {
				return "https://monitor.test/env"
			}
			return get(k)
		}
		if code := r.Run(context.Background(), "one", c.action); code != 1 {
			t.Fatalf("%s: exit = %d", c.action, code)
		}
		if len(p.got) != 1 || p.got[0] != c.want {
			t.Errorf("%s: pings = %v, want [%s]", c.action, p.got, c.want)
		}
	}
}

func TestRunDoesNotPingForActionsWithoutAMonitor(t *testing.T) {
	r, _, p := setup(t, "driver: sqlite, password_env: NOT_SET, ping_url: https://monitor.test/b")
	r.Run(context.Background(), "one", "check")
	r.Run(context.Background(), "one", "verify")
	if len(p.got) != 0 {
		t.Errorf("pings = %v, want none", p.got)
	}
}
