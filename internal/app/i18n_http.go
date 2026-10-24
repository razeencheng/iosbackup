package app

import (
	"errors"
	"net/http"

	"iosbackup/internal/i18n"
)

func localizedLoginError(r *http.Request, code string) string {
	if code == "" {
		return ""
	}
	return i18n.Text(i18n.ResolveRequest(r), code, nil)
}

// Locale is request-scoped. This middleware does not wrap ResponseWriter, preserving SSE Flusher.
func localeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Language", i18n.ResolveRequest(r))
		w.Header().Add("Vary", "Accept-Language")
		w.Header().Add("Vary", "Cookie")
		next.ServeHTTP(w, r)
	})
}

func apiMessage(w http.ResponseWriter, code string, params map[string]any, failure bool) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	payload := map[string]any{"code": code, "message_code": code, "params": params}
	field := "message"
	if failure {
		field = "error"
	}
	payload[field] = i18n.Text(w.Header().Get("Content-Language"), code, params)
	return payload
}

func backupQueryMessageCode(reason string) string {
	switch reason {
	case "device_error":
		return i18n.BackupQueryDeviceError
	case "encrypted_query_failed":
		return i18n.BackupQueryEncryptedQueryFailed
	case "timeout":
		return i18n.BackupQueryTimeout
	case "cancelled":
		return i18n.BackupQueryCancelled
	case "parse_failed":
		return i18n.BackupQueryParseFailed
	case "output_limit":
		return i18n.BackupQueryOutputLimit
	case "tool_unavailable":
		return i18n.BackupQueryToolUnavailable
	case "storage_unavailable":
		return i18n.BackupQueryStorageUnavailable
	case "export_busy":
		return i18n.BackupQueryExportBusy
	case "export_storage_failed":
		return i18n.BackupQueryExportStorageFailed
	default:
		return i18n.BackupQueryCommandFailed
	}
}

func operationErrorPayload(w http.ResponseWriter, err error, fallbackCode string) map[string]any {
	var queryErr *backupQueryError
	if errors.As(err, &queryErr) {
		d := queryErr.diagnostic
		params := map[string]any{}
		if d.DeviceErrorCode != nil {
			params["device_code"] = *d.DeviceErrorCode
		}
		payload := apiMessage(w, backupQueryMessageCode(d.Reason), params, true)
		payload["code"] = fallbackCode
		payload["detail"] = d.Summary
		payload["diagnostic"] = d
		return payload
	}
	var translated *i18n.Error
	if errors.As(err, &translated) {
		payload := apiMessage(w, translated.Code, translated.Params, true)
		if translated.Detail != "" {
			payload["detail"] = translated.Detail
		}
		return payload
	}
	code := fallbackCode
	switch {
	case errors.Is(err, errDeviceNotFound):
		code = i18n.DeviceNotFound
	case errors.Is(err, errDeviceBusy):
		code = i18n.Busy
	case errors.Is(err, errDeviceRemoved):
		code = i18n.DeviceRemovedOperation
	case errors.Is(err, errDeviceRemovalPending):
		code = i18n.RemovalPending
	case errors.Is(err, errConnectionRestarting):
		code = i18n.ConnectionRestarting
	case errors.Is(err, errConnectionUnavailable):
		code = i18n.ConnectionUnavailable
	case errors.Is(err, errDeviceDisconnected):
		code = i18n.DeviceDisconnected
	}
	payload := apiMessage(w, code, nil, true)
	// Unknown tool and OS output is diagnostic data, never a translatable message identifier.
	if code == fallbackCode && err != nil {
		payload["detail"] = err.Error()
	}
	return payload
}

func writeOperationError(w http.ResponseWriter, status int, err error, fallbackCode string) {
	writeJSON(w, status, operationErrorPayload(w, err, fallbackCode))
}

func handleAPIMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	w.Header().Del("Vary")
	w.Header().Del("Content-Language")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodGet {
		_, _ = w.Write(i18n.CatalogJavaScript())
	}
}
