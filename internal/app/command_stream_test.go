package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type streamRunnerFunc func(context.Context, string, []string, []string, io.Writer, io.Writer) error

func (f streamRunnerFunc) Run(ctx context.Context, name string, args, env []string, stdout, stderr io.Writer) error {
	return f(ctx, name, args, env, stdout, stderr)
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
