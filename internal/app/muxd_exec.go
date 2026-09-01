package app

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// execCmd 包装 exec.Cmd，提供统一接口供 muxd.go 使用。
// 目的：让 muxd.go 不直接 import os/exec，便于测试通过 cmdRunner 注入替换。
type execCmd struct {
	cmd      *exec.Cmd
	done     chan struct{}
	doneOnce sync.Once
}

// newExecCmd 创建新的 execCmd（带 context）。
func newExecCmd(ctx context.Context, name string, args ...string) *execCmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &execCmd{cmd: cmd, done: make(chan struct{})}
}

func (e *execCmd) Output() ([]byte, error) {
	return e.cmd.Output()
}

func (e *execCmd) Run() error {
	defer e.markDone()
	return e.cmd.Run()
}

func (e *execCmd) Start() error {
	return e.cmd.Start()
}

func (e *execCmd) Wait() error {
	defer e.markDone()
	return e.cmd.Wait()
}

func (e *execCmd) markDone() { e.doneOnce.Do(func() { close(e.done) }) }

func (e *execCmd) SetEnv(env []string) {
	e.cmd.Env = env
}

func (e *execCmd) SetStdout(w io.Writer) {
	e.cmd.Stdout = w
}

func (e *execCmd) SetStderr(w io.Writer) {
	e.cmd.Stderr = w
}

// SetGracefulCancel 让 context 取消时先发 SIGINT 优雅退出，grace 后仍未退出再强制 kill。
// 替代手动 time.Sleep 等待进程退出（不持锁 sleep）。
func (e *execCmd) SetGracefulCancel(grace time.Duration) {
	e.cmd.Cancel = func() error {
		if e.cmd.Process == nil {
			return nil
		}
		// 终止整个进程组，避免 idevicebackup2 的 helper 变成孤儿进程。
		pid := e.cmd.Process.Pid
		go func() {
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-e.done:
			case <-timer.C:
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
		return syscall.Kill(-pid, syscall.SIGINT)
	}
	e.cmd.WaitDelay = grace
}
