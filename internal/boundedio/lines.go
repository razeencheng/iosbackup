package boundedio

import (
	"io"
	"strings"
)

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
