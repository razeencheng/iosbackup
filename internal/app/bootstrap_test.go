package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunReturnsImmediatelyWhenContextIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Run() did not return immediately for an already-cancelled context")
	}
}

func TestRunReportsEnglishRuntimeConfigError(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	_, configErr := loadRuntimeConfig(os.Getenv)
	if configErr == nil {
		t.Fatal("loadRuntimeConfig() unexpectedly succeeded")
	}

	err := Run(context.Background())
	want := "event=config.invalid Invalid runtime configuration: " + configErr.Error()
	if err == nil || err.Error() != want {
		t.Fatalf("Run() error = %q, want %q", err, want)
	}
	if strings.HasPrefix(err.Error(), "event=application.failed Application failed: ") {
		t.Fatalf("runtime config error gained application prefix: %q", err)
	}
}

func TestExitErrorsUseStableEnglishEvents(t *testing.T) {
	cause := errors.New("boom")
	if got := newRuntimeConfigExitError(cause).Error(); got != "event=config.invalid Invalid runtime configuration: boom" {
		t.Fatalf("runtime config exit error = %q", got)
	}
	if got := newApplicationExitError(cause).Error(); got != "event=application.failed Application failed: boom" {
		t.Fatalf("application exit error = %q", got)
	}
}

func TestExitErrorsPreserveOriginalDiagnosticAndCause(t *testing.T) {
	cause := errors.New("open /备份/我的设备: 原始诊断")
	for _, wrap := range []func(error) error{newRuntimeConfigExitError, newApplicationExitError} {
		err := wrap(cause)
		if !errors.Is(err, cause) || !strings.HasSuffix(err.Error(), cause.Error()) {
			t.Fatalf("exit error changed the original diagnostic or cause: %v", err)
		}
	}
}
