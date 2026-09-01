package boundedio

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type emittedLine struct {
	text           string
	carriageReturn bool
}

func collectLines(reader io.Reader, maxLineBytes int, marker string) ([]emittedLine, error) {
	var lines []emittedLine
	err := ConsumeLines(reader, maxLineBytes, marker, func(line string, carriageReturn bool) error {
		lines = append(lines, emittedLine{text: line, carriageReturn: carriageReturn})
		return nil
	})
	return lines, err
}

func TestConsumeLinesRecognizesNewlineAndCarriageReturn(t *testing.T) {
	got, err := collectLines(strings.NewReader("first\nsecond\rthird"), 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	want := []emittedLine{
		{text: "first", carriageReturn: false},
		{text: "second", carriageReturn: true},
		{text: "third", carriageReturn: false},
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestConsumeLinesIgnoresBackspaceTrimsAndSuppressesEmpty(t *testing.T) {
	got, err := collectLines(strings.NewReader(" \t\n  a\bb  \r\n"), 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	want := []emittedLine{{text: "ab", carriageReturn: true}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestConsumeLinesFlushesAtEOF(t *testing.T) {
	got, err := collectLines(strings.NewReader(" final "), 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (emittedLine{text: "final"}) {
		t.Fatalf("lines = %#v, want final EOF line", got)
	}
}

var errReaderFailed = errors.New("reader failed")

type dataErrorReader struct {
	done bool
}

func (r *dataErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errReaderFailed
	}
	r.done = true
	return copy(p, "partial"), errReaderFailed
}

func TestConsumeLinesFlushesBeforePropagatingReadError(t *testing.T) {
	got, err := collectLines(&dataErrorReader{}, 64, " [cut]")
	if !errors.Is(err, errReaderFailed) {
		t.Fatalf("error = %v, want %v", err, errReaderFailed)
	}
	if len(got) != 1 || got[0].text != "partial" {
		t.Fatalf("lines = %#v, want partial line", got)
	}
}

func TestConsumeLinesEmitterErrorStopsImmediately(t *testing.T) {
	wantErr := context.Canceled
	calls := 0
	err := ConsumeLines(strings.NewReader("first\nsecond\n"), 64, " [cut]", func(string, bool) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("emitter calls = %d, want 1", calls)
	}
}

func TestConsumeLinesBoundsOversizedLineAndUsesExactMarker(t *testing.T) {
	const marker = "<truncated>"
	got, err := collectLines(strings.NewReader("0123456789\nnext\n"), 4, marker)
	if err != nil {
		t.Fatal(err)
	}
	want := []emittedLine{
		{text: "0123" + marker},
		{text: "next"},
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestConsumeLinesZeroLimitRetainsNoContentButMarksTruncation(t *testing.T) {
	got, err := collectLines(strings.NewReader("abc\n"), 0, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].text != " [cut]" {
		t.Fatalf("lines = %#v, want exact marker", got)
	}
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

func TestConsumeLinesHandlesDelimiterChunkBoundaries(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		[]byte("one\r"),
		[]byte("\ntwo"),
		[]byte("\n"),
	}}
	got, err := collectLines(reader, 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	want := []emittedLine{
		{text: "one", carriageReturn: true},
		{text: "two", carriageReturn: false},
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestConsumeLinesEmptyReaderEmitsNothing(t *testing.T) {
	got, err := collectLines(strings.NewReader(""), 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("lines = %#v, want none", got)
	}
}

func TestConsumeLinesDoesNotCloseReader(t *testing.T) {
	reader := &trackingReadCloser{Reader: strings.NewReader("line")}
	_, err := collectLines(reader, 64, " [cut]")
	if err != nil {
		t.Fatal(err)
	}
	if reader.closed {
		t.Fatal("ConsumeLines closed the reader")
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
