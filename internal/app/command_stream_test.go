package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"testing"
	"time"
)

type streamRunnerFunc func(context.Context, string, []string, []string, io.Writer, io.Writer) error

func (f streamRunnerFunc) Run(ctx context.Context, name string, args, env []string, stdout, stderr io.Writer) error {
	return f(ctx, name, args, env, stdout, stderr)
}

func TestStreamCommandErrorPreservesCause(t *testing.T) {
	exitErr := exec.Command("/bin/sh", "-c", "exit 49").Run()
	consumerErr := errors.New("consumer rejected output")
	for _, legacy := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			cause   error
			success bool
		}{
			{"exit", nil, false}, {"cancel", context.Canceled, false},
			{"consumer", consumerErr, false}, {"success_after_cancel", context.Canceled, true},
		} {
			t.Run(fmt.Sprintf("%s/legacy=%v", tc.name, legacy), func(t *testing.T) {
				app := newApplication()
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				run := func() error {
					if tc.cause != nil {
						cancel(tc.cause)
					}
					if tc.success {
						return nil
					}
					return exitErr
				}
				if legacy {
					app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, run() }
				} else {
					app.streamCmdRunner = streamRunnerFunc(func(context.Context, string, []string, []string, io.Writer, io.Writer) error { return run() })
				}
				err := app.runIdeviceCmdStreamEnv(ctx, cmdKindMedium, nil, nil, "/bin/false", io.Discard, io.Discard)
				if tc.success {
					if err != nil {
						t.Fatalf("successful command became failure: %v", err)
					}
					return
				}
				var gotExit *exec.ExitError
				if !errors.As(err, &gotExit) || gotExit.ExitCode() != 49 {
					t.Fatalf("lost exit error: %v", err)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Fatalf("lost context cause %v: %v", tc.cause, err)
				}
			})
		}
	}
	t.Run("real_deadline", func(t *testing.T) {
		app := newApplication()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := app.runIdeviceCmdStreamEnv(ctx, cmdKindMedium, nil, nil, "/bin/sh", io.Discard, io.Discard, "-c", "exec sleep 30")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lost real deadline: %v", err)
		}
	})
}

func TestCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := defaultStreamCmdRunner{}
	err := runner.Run(ctx, "/bin/sh", []string{"-c", "sleep 30"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		t.Fatalf("cancelled command returned %v", err)
	}
}

func TestBackupListJSONStopsMaterializingAfterLimit(t *testing.T) {
	app := newApplication()
	addOnlineDevice(app, "LIMIT", connectionTypeDesc(connectTypeUSB))
	row := []byte("file.db,AppDomain,1024\n")
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, _ io.Writer) error {
		for i := 0; i < 100000; i++ {
			if _, err := stdout.Write(row); err != nil {
				return err
			}
		}
		return nil
	})

	entries, total, truncated, err := app.BackupListLimited(context.Background(), "LIMIT", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5000 || total != 100000 || !truncated {
		t.Fatalf("len=%d total=%d truncated=%v", len(entries), total, truncated)
	}
}

func TestLongCommandKeepsOnlyErrorTail(t *testing.T) {
	app := newApplication()
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, stdout, stderr io.Writer) error {
		_, _ = io.CopyN(stdout, bytes.NewReader(bytes.Repeat([]byte("o"), 1<<20)), 1<<20)
		_, _ = io.CopyN(stderr, bytes.NewReader(bytes.Repeat([]byte("e"), 1<<20)), 1<<20)
		return errors.New("boom")
	})
	out, err := app.runIdeviceCmdBoundedEnv(context.Background(), cmdKindLong, nil, nil, "/bin/false")
	if err == nil {
		t.Fatal("expected command error")
	}
	if len(out) != maxCommandErrorBytes {
		t.Fatalf("tail length=%d", len(out))
	}
}
