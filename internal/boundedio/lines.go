package boundedio

import (
	"io"
	"strings"
)

// ConsumeRawLines 保留空白、控制字符及原分隔符，显式报告数据是否被截断。
// delimiter 为 0 表示末尾没有分隔符；回调错误立即终止消费。
func ConsumeRawLines(reader io.Reader, limit int, emit func(string, byte, bool) error) error {
	if limit < 0 {
		limit = 0
	}
	buffer := make([]byte, 32<<10)
	line := make([]byte, 0, initialCapacity(limit))
	truncated := false
	flush := func(delimiter byte) error {
		err := emit(string(line), delimiter, truncated)
		line = line[:0]
		truncated = false
		return err
	}
	for {
		n, err := reader.Read(buffer)
		for _, b := range buffer[:n] {
			if b == '\r' || b == '\n' {
				if err := flush(b); err != nil {
					return err
				}
			} else if len(line) < limit {
				line = append(line, b)
			} else {
				truncated = true
			}
		}
		if err != nil {
			if len(line) > 0 || truncated {
				if flushErr := flush(0); flushErr != nil {
					return flushErr
				}
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// ConsumeLines splits reader output on newline and carriage-return bytes. Each
// emitted line is trimmed, bounded to maxLineBytes, and annotated with
// truncatedMarker when input beyond the limit was discarded. Returning an
// error from emit stops consumption immediately.
func ConsumeLines(reader io.Reader, maxLineBytes int, truncatedMarker string, emit func(line string, carriageReturn bool) error) error {
	if maxLineBytes < 0 {
		maxLineBytes = 0
	}

	readBuffer := make([]byte, 32<<10)
	line := make([]byte, 0, initialCapacity(maxLineBytes))
	truncated := false
	flush := func(carriageReturn bool) error {
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" || truncated {
			if truncated {
				trimmed += truncatedMarker
			}
			if err := emit(trimmed, carriageReturn); err != nil {
				return err
			}
		}
		line = make([]byte, 0, initialCapacity(maxLineBytes))
		truncated = false
		return nil
	}

	for {
		n, readErr := reader.Read(readBuffer)
		for _, current := range readBuffer[:n] {
			switch current {
			case '\n':
				if err := flush(false); err != nil {
					return err
				}
			case '\r':
				if err := flush(true); err != nil {
					return err
				}
			case '\b':
				// Progress output may use backspace to redraw the current line.
			default:
				if len(line) < maxLineBytes {
					line = append(line, current)
				} else {
					truncated = true
				}
			}
		}

		if readErr != nil {
			if len(line) > 0 || truncated {
				if err := flush(false); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}
