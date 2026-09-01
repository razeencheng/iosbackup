package boundedio

import (
	"bytes"
	"strconv"
	"sync"
	"testing"
)

func TestTailBufferNormalizesNonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			buffer := NewTailBuffer(limit)
			if n, err := buffer.Write([]byte("ab")); err != nil || n != 2 {
				t.Fatalf("Write() = (%d, %v), want (2, nil)", n, err)
			}
			if got := buffer.String(); got != "b" {
				t.Fatalf("String() = %q, want %q", got, "b")
			}
			if !buffer.Truncated() {
				t.Fatal("Truncated() = false, want true")
			}
		})
	}
}

func TestTailBufferEmptyWrite(t *testing.T) {
	buffer := NewTailBuffer(4)
	if n, err := buffer.Write(nil); err != nil || n != 0 {
		t.Fatalf("Write() = (%d, %v), want (0, nil)", n, err)
	}
	if got := buffer.Bytes(); len(got) != 0 {
		t.Fatalf("Bytes() = %q, want empty", got)
	}
	if buffer.Truncated() {
		t.Fatal("Truncated() = true, want false")
	}
}

func TestTailBufferExactLimitIsNotTruncated(t *testing.T) {
	buffer := NewTailBuffer(4)
	if n, err := buffer.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatalf("Write() = (%d, %v), want (4, nil)", n, err)
	}
	if got := buffer.String(); got != "abcd" {
		t.Fatalf("String() = %q, want %q", got, "abcd")
	}
	if buffer.Truncated() {
		t.Fatal("Truncated() = true, want false")
	}
}

func TestTailBufferMultiWriteOverflowRetainsTail(t *testing.T) {
	buffer := NewTailBuffer(5)
	for _, part := range []string{"ab", "cd", "ef"} {
		if n, err := buffer.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("Write(%q) = (%d, %v)", part, n, err)
		}
	}
	if got := buffer.String(); got != "bcdef" {
		t.Fatalf("String() = %q, want %q", got, "bcdef")
	}
	if !buffer.Truncated() {
		t.Fatal("Truncated() = false, want true")
	}
}

func TestTailBufferHugeWriteRetainsTailAndOriginalLength(t *testing.T) {
	buffer := NewTailBuffer(4)
	input := []byte("0123456789")
	if n, err := buffer.Write(input); err != nil || n != len(input) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(input))
	}
	if got := buffer.String(); got != "6789" {
		t.Fatalf("String() = %q, want %q", got, "6789")
	}
	if !buffer.Truncated() {
		t.Fatal("Truncated() = false, want true")
	}
}

func TestTailBufferBytesReturnsDefensiveCopy(t *testing.T) {
	buffer := NewTailBuffer(4)
	_, _ = buffer.Write([]byte("abcd"))
	got := buffer.Bytes()
	got[0] = 'X'
	if actual := buffer.String(); actual != "abcd" {
		t.Fatalf("String() after mutating Bytes() = %q, want %q", actual, "abcd")
	}
}

func TestTailBufferConcurrentWritesStayBounded(t *testing.T) {
	const limit = 64
	buffer := NewTailBuffer(limit)
	var writers sync.WaitGroup
	for i := 0; i < 32; i++ {
		writers.Add(1)
		go func(fill byte) {
			defer writers.Done()
			for j := 0; j < 100; j++ {
				if n, err := buffer.Write(bytes.Repeat([]byte{fill}, 17)); err != nil || n != 17 {
					t.Errorf("Write() = (%d, %v), want (17, nil)", n, err)
					return
				}
				if got := len(buffer.Bytes()); got > limit {
					t.Errorf("buffer length = %d, want <= %d", got, limit)
					return
				}
			}
		}(byte(i))
	}
	writers.Wait()
	if got := len(buffer.Bytes()); got > limit {
		t.Fatalf("final buffer length = %d, want <= %d", got, limit)
	}
	if !buffer.Truncated() {
		t.Fatal("Truncated() = false, want true")
	}
}

func TestHeadBufferNonPositiveLimitRetainsNothing(t *testing.T) {
	for _, limit := range []int{0, -1} {
		buffer := NewHeadBuffer(limit)
		if n, err := buffer.Write([]byte("abc")); err != nil || n != 3 {
			t.Fatalf("limit %d: Write() = (%d, %v), want (3, nil)", limit, n, err)
		}
		if got := buffer.Bytes(); len(got) != 0 {
			t.Fatalf("limit %d: Bytes() = %q, want empty", limit, got)
		}
		if !buffer.Truncated() {
			t.Fatalf("limit %d: Truncated() = false, want true", limit)
		}
	}
}

func TestHeadBufferExactLimitIsNotTruncated(t *testing.T) {
	buffer := NewHeadBuffer(4)
	if n, err := buffer.Write([]byte("abcd")); err != nil || n != 4 {
		t.Fatalf("Write() = (%d, %v), want (4, nil)", n, err)
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("Bytes() = %q, want %q", got, "abcd")
	}
	if buffer.Truncated() {
		t.Fatal("Truncated() = true, want false")
	}
}

func TestHeadBufferMultiWriteOverflowRetainsHeadAndOriginalLength(t *testing.T) {
	buffer := NewHeadBuffer(5)
	for _, part := range []string{"ab", "cd", "efg"} {
		if n, err := buffer.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", part, n, err, len(part))
		}
	}
	if got := string(buffer.Bytes()); got != "abcde" {
		t.Fatalf("Bytes() = %q, want %q", got, "abcde")
	}
	if !buffer.Truncated() {
		t.Fatal("Truncated() = false, want true")
	}
}

func TestHeadBufferBytesReturnsDefensiveCopy(t *testing.T) {
	buffer := NewHeadBuffer(4)
	_, _ = buffer.Write([]byte("abcd"))
	got := buffer.Bytes()
	got[0] = 'X'
	if actual := string(buffer.Bytes()); actual != "abcd" {
		t.Fatalf("Bytes() after mutating copy = %q, want %q", actual, "abcd")
	}
}
