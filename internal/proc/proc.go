package proc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const KillAfter = 30 * time.Second

type Spec struct {
	Name   string
	Args   []string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Runner interface {
	Run(ctx context.Context, s Spec) error
}

type Exec struct{}

func (Exec) Run(ctx context.Context, s Spec) error {
	cmd := exec.CommandContext(ctx, s.Name, s.Args...)
	cmd.Env = s.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdin = s.Stdin
	cmd.Stdout = s.Stdout
	cmd.Stderr = s.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		time.AfterFunc(KillAfter, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		return nil
	}
	cmd.WaitDelay = KillAfter + 5*time.Second
	return cmd.Run()
}

func Output(ctx context.Context, r Runner, s Spec) (string, error) {
	var buf bytes.Buffer
	s.Stdout = &buf
	err := r.Run(ctx, s)
	return buf.String(), err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func BaseEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	if tz := os.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	return env
}
