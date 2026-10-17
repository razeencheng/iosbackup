package app

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"iosbackup/internal/i18n"
)

func TestLogConfigurationHasStableEnglishEvent(t *testing.T) {
	previousLevel := currentLogLevel
	previousWriter := log.Writer()
	t.Cleanup(func() { currentLogLevel = previousLevel; log.SetOutput(previousWriter) })
	t.Setenv("LOG_LEVEL", "WARN")
	var output bytes.Buffer
	log.SetOutput(&output)
	initLogLevel()
	if !strings.Contains(output.String(), "event=logging.configured level=WARN") {
		t.Fatalf("missing stable log event: %s", output.String())
	}
	app := newApplication()
	app.addInfoLog("PHONE", "event=test.hidden")
	app.addErrorLog("PHONE", "event=test.failure")
	if strings.Contains(output.String(), "event=test.hidden") || !strings.Contains(output.String(), "PHONE: event=test.failure") {
		t.Fatalf("log filtering or device context changed: %s", output.String())
	}
}

func TestOperationalLogsKeepEnglishAndOriginalDiagnostics(t *testing.T) {
	previousLevel, previousWriter := currentLogLevel, log.Writer()
	t.Cleanup(func() { currentLogLevel = previousLevel; log.SetOutput(previousWriter) })
	currentLogLevel = logLevelInfo
	var output bytes.Buffer
	log.SetOutput(&output)
	app := newApplication()
	const diagnostic = "工具原始输出：/备份/我的设备"
	err := operationError("api.ip_connection_failed", nil, diagnostic)
	for _, locale := range []string{"zh-CN", "en"} {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Language", locale)
		payload := operationErrorPayload(w, err, "api.operation_failed")
		if payload["error"] != i18n.Text(locale, err.Code, nil) || payload["detail"] != diagnostic {
			t.Fatalf("localized response lost its message or original detail: %#v", payload)
		}
		output.Reset()
		app.addErrorLog("测试设备", "event=network.ip_unreachable "+err.Error())
		want := "测试设备: event=network.ip_unreachable " + i18n.Text("en", err.Code, nil) + ": " + diagnostic
		if !strings.Contains(output.String(), want) {
			t.Fatalf("log language or original content changed for %s: %s", locale, output.String())
		}
	}
}
