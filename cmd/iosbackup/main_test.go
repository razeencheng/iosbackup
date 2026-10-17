package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"testing"

	"iosbackup/internal/app"
)

func TestExitErrorTextDoesNotAddAnotherPrefix(t *testing.T) {
	err := errors.New("event=config.invalid Invalid runtime configuration: PORT: invalid syntax")
	if got := exitErrorText(err); got != err.Error() {
		t.Fatalf("exitErrorText() = %q, want %q", got, err)
	}
}

func TestStartupFailureLogUsesEnglish(t *testing.T) {
	t.Setenv("IOSBK_LISTEN_ADDR", "")
	t.Setenv("PORT", "0")
	t.Setenv("LANG", "zh_CN.UTF-8")
	err := app.Run(context.Background())
	if err == nil {
		t.Fatal("invalid port must stop startup")
	}
	var output bytes.Buffer
	log.New(&output, "", 0).Print(exitErrorText(err))
	want := "event=config.invalid Invalid runtime configuration: PORT must be between 1 and 65535\n"
	if got := output.String(); got != want {
		t.Fatalf("startup log = %q, want %q", got, want)
	}
}
