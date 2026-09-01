package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestBackupLogLineIsBounded(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", 32<<20))
	var emitted []string
	consumeBackupOutput(input, func(line string, _ bool) {
		emitted = append(emitted, line)
	})
	if len(emitted) != 1 {
		t.Fatalf("expected one bounded line, got %d", len(emitted))
	}
	if len(emitted[0]) > maxBackupLogLineBytes+len(backupLogTruncatedMarker) {
		t.Fatalf("line retained %d bytes", len(emitted[0]))
	}
	if !strings.HasSuffix(emitted[0], backupLogTruncatedMarker) {
		t.Fatalf("missing truncation marker: %q", emitted[0][len(emitted[0])-32:])
	}
}

func TestTailBufferRetainsOnlyLimit(t *testing.T) {
	buffer := newTailBuffer(64)
	_, _ = buffer.Write(bytes.Repeat([]byte("a"), 1024))
	if got := len(buffer.Bytes()); got != 64 {
		t.Fatalf("tail length=%d", got)
	}
	if !buffer.Truncated() {
		t.Fatal("expected truncated flag")
	}
}
