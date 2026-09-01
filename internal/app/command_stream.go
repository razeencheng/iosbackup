package app

import (
	"context"
	"io"
	"time"
)

type streamCmdRunner interface {
	Run(ctx context.Context, name string, args []string, env []string, stdout, stderr io.Writer) error
}

type defaultStreamCmdRunner struct{}

func (defaultStreamCmdRunner) Run(ctx context.Context, name string, args []string, env []string, stdout, stderr io.Writer) error {
	cmd := newExecCmd(ctx, name, args...)
	cmd.SetEnv(env)
	cmd.SetStdout(stdout)
	cmd.SetStderr(stderr)
	cmd.SetGracefulCancel(3 * time.Second)
	return cmd.Run()
}

func (app *application) runIdeviceCmdBoundedEnv(ctx context.Context, kind cmdKind, device *device, extraEnv []string, bin string, extraArgs ...string) ([]byte, error) {
	tail := newTailBuffer(maxCommandErrorBytes)
	err := app.runIdeviceCmdStreamEnv(ctx, kind, device, extraEnv, bin, tail, tail, extraArgs...)
	return tail.Bytes(), err
}
