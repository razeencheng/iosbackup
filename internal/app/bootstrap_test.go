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

func TestRunPreservesLegacyRuntimeConfigErrorText(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	_, configErr := loadRuntimeConfig(os.Getenv)
	if configErr == nil {
		t.Fatal("loadRuntimeConfig() unexpectedly succeeded")
	}

	err := Run(context.Background())
	want := "运行配置无效: " + configErr.Error()
	if err == nil || err.Error() != want {
		t.Fatalf("Run() error = %q, want %q", err, want)
	}
	if strings.HasPrefix(err.Error(), "应用退出: ") {
		t.Fatalf("runtime config error gained application prefix: %q", err)
	}
}

func TestExitErrorsPreserveLegacyPrefixes(t *testing.T) {
	cause := errors.New("boom")
	if got := newRuntimeConfigExitError(cause).Error(); got != "运行配置无效: boom" {
		t.Fatalf("runtime config exit error = %q", got)
	}
	if got := newApplicationExitError(cause).Error(); got != "应用退出: boom" {
		t.Fatalf("application exit error = %q", got)
	}
}
