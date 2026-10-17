package app

import (
	"strings"

	"iosbackup/internal/i18n"
)

// operationError keeps the user-facing message separate from raw tool diagnostics.
func operationError(code string, cause error, output string) *i18n.Error {
	message := i18n.NewError(code, nil)
	message.Cause = cause
	if cause != nil {
		message.Detail = cause.Error()
	}
	if output = strings.TrimSpace(output); output != "" {
		if message.Detail != "" {
			message.Detail += ": "
		}
		message.Detail += output
	}
	return message
}
