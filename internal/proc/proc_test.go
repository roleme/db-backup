package proc

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecUsesOnlyTheGivenEnv(t *testing.T) {
	var out bytes.Buffer
	err := Exec{}.Run(context.Background(), Spec{Name: "env", Env: []string{"ONLY=this"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "ONLY=this" {
		t.Errorf("env = %q", out.String())
	}
}

func TestExecPassesArgsWithoutAShell(t *testing.T) {
	var out bytes.Buffer
	err := Exec{}.Run(context.Background(), Spec{Name: "echo", Args: []string{"a b", "$HOME;id"}, Env: []string{}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "a b $HOME;id\n" {
		t.Errorf("echo = %q", out.String())
	}
}

func TestExitCode(t *testing.T) {
	err := Exec{}.Run(context.Background(), Spec{Name: "sh", Args: []string{"-c", "exit 3"}, Env: []string{}})
	if ExitCode(err) != 3 {
		t.Errorf("ExitCode = %d (%v)", ExitCode(err), err)
	}
	if ExitCode(nil) != 0 {
		t.Error("nil error must be 0")
	}
}

func TestTimeoutKillsTheWholeGroup(t *testing.T) {
	marker := t.TempDir() + "/pid"
	script := "sleep 30 & echo $! > " + marker + "; wait"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Exec{NewGroup: true}.Run(ctx, Spec{Name: "sh", Args: []string{"-c", script}, Env: []string{"PATH=" + os.Getenv("PATH")}})
	if err == nil {
		t.Fatal("expected an error after the timeout")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("run took %s", time.Since(start))
	}
	b, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatal(perr)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("child %d survived the group kill", pid)
}

func TestOutputAndFake(t *testing.T) {
	f := &Fake{Handler: func(s Spec) error {
		_, err := s.Stdout.Write([]byte("42\n"))
		return err
	}}
	got, err := Output(context.Background(), f, Spec{Name: "psql", Args: []string{"-c", "x"}})
	if err != nil || got != "42\n" {
		t.Errorf("Output = %q, %v", got, err)
	}
	if len(f.Calls) != 1 || f.Calls[0].Spec.Name != "psql" {
		t.Errorf("calls = %+v", f.Calls)
	}
}

func TestTimeoutKillsDumpToolsStartedByTheChild(t *testing.T) {
	if os.Getenv("DBB_NESTED_HELPER") == "1" {
		_ = Exec{}.Run(context.Background(), Spec{Name: "sh", Args: []string{"-c", "echo $$ > " + os.Getenv("DBB_MARKER") + "; exec sleep 30"}, Env: []string{"PATH=" + os.Getenv("PATH")}})
		return
	}
	marker := t.TempDir() + "/pid"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Exec{NewGroup: true}.Run(ctx, Spec{
		Name: os.Args[0],
		Args: []string{"-test.run=TestTimeoutKillsDumpToolsStartedByTheChild"},
		Env:  []string{"DBB_NESTED_HELPER=1", "DBB_MARKER=" + marker, "PATH=" + os.Getenv("PATH")},
	})
	if err == nil {
		t.Fatal("expected an error after the timeout")
	}
	b, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatal(perr)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("tool %d started by the child survived the group kill", pid)
}
