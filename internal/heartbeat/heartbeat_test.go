package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roleme/db-backup/internal/config"
	"github.com/roleme/db-backup/internal/status"
)

type call struct{ base, suffix, body string }

type recNotifier struct{ calls []call }

func (r *recNotifier) Notify(base, suffix, body string) {
	r.calls = append(r.calls, call{base, suffix, body})
}

var now = time.Unix(1_000_000, 0)

func fixture(t *testing.T, body string) (*config.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f, t.TempDir()
}

const twoTargets = `
heartbeat:
  ping_url: http://hc/dumps
  schedule: "5 3 * * *"
  verify_ping_url: http://hc/verify
  verify_schedule: "30 6 * * 0"
targets:
  alpha: {driver: sqlite, verify_schedule: "0 5 * * 0"}
  beta: {driver: sqlite}
`

func ago(h float64) time.Time { return now.Add(-time.Duration(h * float64(time.Hour))) }

func TestDumpsAreHealthyWhenEveryTargetSucceededRecently(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	_ = status.Record(dir, "alpha", "backup", true, ago(3))
	_ = status.Record(dir, "beta", "backup", true, ago(20))

	if got := Problems("dumps", f, dir, now); len(got) != 0 {
		t.Errorf("problems = %v", got)
	}
}

func TestDumpsReportEachTargetThatIsMissingStaleOrFailed(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	_ = status.Record(dir, "alpha", "backup", true, ago(30))
	_ = status.Record(dir, "alpha", "backup", false, ago(1))

	got := strings.Join(Problems("dumps", f, dir, now), "\n")
	for _, want := range []string{"alpha: last backup failed", "beta: no backup recorded"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %q lack %q", got, want)
		}
	}

	f, dir = fixture(t, twoTargets)
	_ = status.Record(dir, "alpha", "backup", true, ago(27))
	_ = status.Record(dir, "beta", "backup", true, ago(1))
	if got := strings.Join(Problems("dumps", f, dir, now), "\n"); !strings.Contains(got, "alpha: last successful backup is 27 h old") {
		t.Errorf("problems = %q", got)
	}
}

func TestVerifyOnlyLooksAtTargetsWithARestoreTest(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	if got := Problems("verify", f, dir, now); strings.Join(got, "\n") != "alpha: no verify recorded" {
		t.Errorf("problems = %v", got)
	}
	_ = status.Record(dir, "alpha", "verify", true, ago(100))
	if got := Problems("verify", f, dir, now); len(got) != 0 {
		t.Errorf("problems = %v", got)
	}
	_ = status.Record(dir, "alpha", "verify", true, ago(200))
	_ = status.Record(dir, "alpha", "verify", false, ago(1))
	if got := strings.Join(Problems("verify", f, dir, now), "\n"); !strings.Contains(got, "alpha: last verify failed") {
		t.Errorf("problems = %q", got)
	}
}

func TestInvalidTargetsAreProblems(t *testing.T) {
	f, dir := fixture(t, "heartbeat:\n  ping_url: http://hc\n  schedule: \"@daily\"\ntargets:\n  good: {driver: sqlite}\n  bad: {driver: oracle}\n")
	_ = status.Record(dir, "good", "backup", true, ago(1))

	got := strings.Join(Problems("dumps", f, dir, now), "\n")
	if !strings.Contains(got, "bad: invalid configuration") || strings.Contains(got, "good") {
		t.Errorf("problems = %q", got)
	}
}

func TestRunPingsSuccessWhenHealthy(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	_ = status.Record(dir, "alpha", "backup", true, ago(1))
	_ = status.Record(dir, "beta", "backup", true, ago(1))
	n := &recNotifier{}

	code := Run(context.Background(), "dumps", f, Deps{Dir: dir, Getenv: func(string) string { return "" }, Notifier: n, Now: func() time.Time { return now }})
	if code != 0 || len(n.calls) != 1 || n.calls[0] != (call{"http://hc/dumps", "", ""}) {
		t.Errorf("code = %d calls = %+v", code, n.calls)
	}
}

func TestRunPingsFailureWithTheReasonWhenNot(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	n := &recNotifier{}

	code := Run(context.Background(), "dumps", f, Deps{Dir: dir, Getenv: func(string) string { return "" }, Notifier: n, Now: func() time.Time { return now }})
	if code != 1 || len(n.calls) != 1 {
		t.Fatalf("code = %d calls = %+v", code, n.calls)
	}
	c := n.calls[0]
	if c.base != "http://hc/dumps" || c.suffix != "/fail" || !strings.Contains(c.body, "alpha: no backup recorded") || !strings.Contains(c.body, "beta: no backup recorded") {
		t.Errorf("call = %+v", c)
	}
}

func TestRunUsesTheVerifyURLForVerify(t *testing.T) {
	f, dir := fixture(t, twoTargets)
	_ = status.Record(dir, "alpha", "verify", true, ago(1))
	n := &recNotifier{}

	code := Run(context.Background(), "verify", f, Deps{Dir: dir, Getenv: func(string) string { return "" }, Notifier: n, Now: func() time.Time { return now }})
	if code != 0 || len(n.calls) != 1 || n.calls[0].base != "http://hc/verify" {
		t.Errorf("code = %d calls = %+v", code, n.calls)
	}
}

func TestRunRefusesWhatIsNotConfigured(t *testing.T) {
	n := &recNotifier{}
	d := func(dir string) Deps {
		return Deps{Dir: dir, Getenv: func(string) string { return "" }, Notifier: n, Now: func() time.Time { return now }}
	}

	f, dir := fixture(t, "targets:\n  a: {driver: sqlite}\n")
	if code := Run(context.Background(), "dumps", f, d(dir)); code != 1 {
		t.Errorf("no heartbeat section: code = %d", code)
	}
	f, dir = fixture(t, "heartbeat:\n  ping_url: http://hc\n  schedule: \"@daily\"\ntargets:\n  a: {driver: sqlite}\n")
	if code := Run(context.Background(), "verify", f, d(dir)); code != 1 {
		t.Errorf("no verify url: code = %d", code)
	}
	f, dir = fixture(t, "heartbeat:\n  ping_url_env: UNSET\n  schedule: \"@daily\"\ntargets:\n  a: {driver: sqlite}\n")
	if code := Run(context.Background(), "dumps", f, d(dir)); code != 1 {
		t.Errorf("unset variable: code = %d", code)
	}
	if code := Run(context.Background(), "other", f, d(dir)); code != 2 {
		t.Errorf("unknown kind: code = %d", code)
	}
	if len(n.calls) != 0 {
		t.Errorf("nothing may be pinged when the heartbeat cannot be resolved: %+v", n.calls)
	}
}
