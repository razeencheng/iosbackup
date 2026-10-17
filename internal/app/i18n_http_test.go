package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iosbackup/internal/i18n"
)

func TestLocalizedAuthenticationErrorContract(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	for _, tc := range []struct{ name, cookie, accept, locale, message string }{
		{"default", "", "", "en", "Sign in to continue."},
		{"accept", "", "fr;q=1,zh-CN;q=0.8,en;q=0.5", "zh-CN", "请先登录后再操作。"},
		{"cookie", "en", "zh-CN", "en", "Sign in to continue."},
		{"legacy", "zh", "en", "zh-CN", "请先登录后再操作。"},
		{"invalid", "javascript:alert(1)", "en", "en", "Sign in to continue."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			req.Header.Set("Accept-Language", tc.accept)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "iosbk_lang", Value: tc.cookie})
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			var payload map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if rr.Code != http.StatusUnauthorized || payload["code"] != "api.unauthenticated" || payload["message_code"] != "api.unauthenticated" || payload["error"] != tc.message {
				t.Fatalf("localized error = %d %s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Language"); got != tc.locale {
				t.Fatalf("Content-Language = %q", got)
			}
		})
	}
}

func TestLocalizedTypedErrorAndDiagnosticSeparation(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		rr := httptest.NewRecorder()
		rr.Header().Set("Content-Language", locale)
		cause := errors.New("raw tool failure: 设置失败")
		err := &i18n.Error{Code: "api.backup_interval_range", Params: map[string]any{"min": 1, "max": 720}, Detail: cause.Error(), Cause: cause}
		writeOperationError(rr, http.StatusBadRequest, err, "api.operation_failed")
		var payload map[string]any
		if decodeErr := json.Unmarshal(rr.Body.Bytes(), &payload); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if payload["code"] != err.Code || payload["detail"] != cause.Error() || payload["error"] != i18n.Text(locale, err.Code, err.Params) {
			t.Fatalf("error payload = %s", rr.Body.String())
		}
		if strings.Contains(payload["error"].(string), "raw tool failure") {
			t.Fatal("raw diagnostic leaked into user message")
		}
		if !errors.Is(err, cause) {
			t.Fatal("typed error lost its cause")
		}
	}
	rr := httptest.NewRecorder()
	writeOperationError(rr, http.StatusInternalServerError, errors.New("unknown 工具错误"), "api.operation_failed")
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "The operation could not be completed. Retry or check the server logs." || payload["detail"] != "unknown 工具错误" {
		t.Fatalf("unknown error = %s", rr.Body.String())
	}
}

func TestLocaleMiddlewarePreservesSSEFlusher(t *testing.T) {
	handler := localeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("locale middleware removed Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {}\n\n"))
		flusher.Flush()
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if !rr.Flushed || rr.Header().Get("Content-Language") != "en" {
		t.Fatalf("SSE response = %+v", rr)
	}
}

func TestConfigFieldErrorsCarryStableCodesAndParameters(t *testing.T) {
	for _, tc := range []struct{ field, value, code string }{
		{"unrecognized_setting", `true`, "api.config_unknown_field"},
		{"backup_interval", `"invalid"`, "api.config_field_invalid"},
	} {
		var config backupConfig
		err := mergeBackupConfig(&config, map[string]json.RawMessage{tc.field: json.RawMessage(tc.value)})
		var message *i18n.Error
		if !errors.As(err, &message) || message.Code != tc.code || message.Params["field"] != tc.field {
			t.Fatalf("field %s error = %#v", tc.field, err)
		}
	}
}

func TestLocalizedConfigValidationParameters(t *testing.T) {
	app := newLoginTestApp(t)
	handler := app.setupRoutes()
	for _, locale := range []string{"en", "zh-CN"} {
		req := httptest.NewRequest(http.MethodPost, "/api/save-config/TEST-DEVICE", strings.NewReader(`{"min_battery_level":101}`))
		req.SetBasicAuth("iosbackup", "correct-password")
		req.Header.Set(csrfHeader, app.csrfManager.Token())
		req.AddCookie(&http.Cookie{Name: "iosbk_lang", Value: locale})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var payload struct {
			Code   string         `json:"code"`
			Error  string         `json:"error"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if rr.Code != http.StatusBadRequest || payload.Code != "api.battery_level_range" || payload.Params["min"] != float64(0) || payload.Params["max"] != float64(100) {
			t.Fatalf("validation response = %d %s", rr.Code, rr.Body.String())
		}
		if payload.Error != i18n.Text(locale, payload.Code, payload.Params) {
			t.Fatalf("incorrect locale %s: %s", locale, rr.Body.String())
		}
	}
}

func TestAPICatalogPublicAndReadOnly(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/static/api-messages.js", nil, nil)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Body.String(), "IOSBKI18n.register(") || !strings.Contains(rr.Body.String(), "api.unauthenticated") || !strings.Contains(rr.Body.String(), "zh-CN") {
		t.Fatalf("catalog response = %d %s", rr.Code, rr.Body.String())
	}
	rr = performRequest(handler, http.MethodPost, "/static/api-messages.js", nil, nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("catalog POST = %d", rr.Code)
	}
}

func TestStatusIncludesUnambiguousBackupTimestamp(t *testing.T) {
	app := newLoginTestApp(t)
	app.devices["test"] = &device{UDID: "test", LastBackup: time.Date(2026, 10, 11, 12, 34, 0, 0, time.FixedZone("UTC+8", 8*3600))}
	data, err := json.Marshal(app.buildStatusSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Devices) != 1 || payload.Devices[0]["last_backup_at"] != "2026-10-11T12:34:00+08:00" {
		t.Fatalf("snapshot = %s", data)
	}
	if payload.Devices[0]["last_backup"] != "2026-10-11 12:34" {
		t.Fatalf("legacy timestamp = %s", data)
	}
}
