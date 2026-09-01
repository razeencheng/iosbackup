// Package boundedio provides concurrency-safe, size-bounded I/O primitives.
package boundedio

import "sync"

// TailBuffer retains only the most recent bytes written to it.
type TailBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

// NewTailBuffer creates a tail buffer. Non-positive limits are normalized to
// one byte so every instance remains useful as an io.Writer.
func NewTailBuffer(limit int) *TailBuffer {
	if limit < 1 {
		limit = 1
	}
	return &TailBuffer{data: make([]byte, 0, initialCapacity(limit)), limit: limit}
}

// Write implements io.Writer and always reports the original input length.
func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(p)
	if written == 0 {
		return 0, nil
	}
	if written > b.limit-len(b.data) {
		b.truncated = true
	}
	if written >= b.limit {
		b.data = append(b.data[:0], p[written-b.limit:]...)
		return written, nil
	}

	overflow := len(b.data) - (b.limit - written)
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

// Bytes returns a copy of the retained bytes.
func (b *TailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

// String returns the retained bytes as a string.
func (b *TailBuffer) String() string { return string(b.Bytes()) }

// Truncated reports whether any bytes have been discarded.
func (b *TailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// HeadBuffer retains only the first bytes written to it.
type HeadBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

// NewHeadBuffer creates a head buffer. Non-positive limits retain no bytes.
func NewHeadBuffer(limit int) *HeadBuffer {
	if limit < 0 {
		limit = 0
	}
	return &HeadBuffer{data: make([]byte, 0, initialCapacity(limit)), limit: limit}
}

// Write implements io.Writer and always reports the original input length.
func (b *HeadBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(p)
	remaining := b.limit - len(b.data)
	if written > remaining {
		b.truncated = true
	}
	if remaining > written {
		remaining = written
	}
	if remaining > 0 {
		b.data = append(b.data, p[:remaining]...)
	}
	return written, nil
}

// Bytes returns a copy of the retained bytes.
func (b *HeadBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

// Truncated reports whether any bytes have been discarded.
func (b *HeadBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func initialCapacity(limit int) int {
	if limit < 64 {
		return limit
	}
	return 64
}
