package main

import (
	"errors"
	"testing"
)

func TestExitErrorTextDoesNotAddAnotherPrefix(t *testing.T) {
	err := errors.New("运行配置无效: PORT: invalid syntax")
	if got := exitErrorText(err); got != err.Error() {
		t.Fatalf("exitErrorText() = %q, want %q", got, err)
	}
}
