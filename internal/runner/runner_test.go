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
)

type recPinger struct{ got []string }

func (r *recPinger) Ping(base, suffix string) { r.got = append(r.got, base+suffix) }

func setup(t *testing.T, body string) (Runner, *proc.Fake, *recPinger) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &proc.Fake{}
	p := &recPinger{}
	env := map[string]string{"ONE_PW": "secret-one", "TWO_PW": "secret-two", "BACKUP_DIR": "/backups", "PATH": "/usr/bin"}
	return Runner{
		TargetsDir: dir,
		LockDir:    t.TempDir(),
		Getenv:     func(k string) string { return env[k] },
		Exec:       f,
		Ping:       p,
	}, f, p
}

func TestRunStartsDbBackupWithAnIsolatedEnvironment(t *testing.T) {
	r, f, _ := setup(t, "DRIVER=sqlite\nDB_PASSWORD_ENV=ONE_PW\nTIMEOUT=5\n")
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
	r, _, _ := setup(t, "DRIVER=sqlite\nSCHEDLE=@daily\n")
	if code := r.Run(context.Background(), "one", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
	if code := r.Run(context.Background(), "bad/name", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
	r2, _, _ := setup(t, "DRIVER=sqlite\nDB_PASSWORD_ENV=NOT_SET\n")
	if code := r2.Run(context.Background(), "one", "check"); code != 1 {
		t.Errorf("exit = %d", code)
	}
}

func TestRunPassesThroughTheChildExitCode(t *testing.T) {
	r, f, _ := setup(t, "DRIVER=sqlite\n")
	f.Handler = func(proc.Spec) error { return exitErr(t, 7) }
	if code := r.Run(context.Background(), "one", "backup"); code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

func TestRunTimeoutExits124AndPingsFail(t *testing.T) {
	r, _, p := setup(t, "DRIVER=sqlite\nTIMEOUT=1\nHC_PING_URL=http://p/ok\nHC_VERIFY_PING_URL=http://p/v\n")
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
	return exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
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
	r, _, _ := setup(t, "DRIVER=sqlite\n")
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
