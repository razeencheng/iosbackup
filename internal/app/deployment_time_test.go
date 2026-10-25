package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func timeZoneApp(t *testing.T, zone string) *application {
	t.Helper()
	cfg, err := loadRuntimeConfig(func(key string) string {
		if key == "TZ" {
			return zone
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	return newApplicationWithRuntime(context.Background(), cfg)
}

func TestDeploymentTimezonePreservesInstantsAndLegacyFields(t *testing.T) {
	instant := time.Date(2026, 10, 24, 20, 30, 0, 0, time.UTC)
	for _, zone := range []string{"UTC", "Asia/Kolkata", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			app := timeZoneApp(t, zone)
			app.devices["PHONE"] = &device{UDID: "PHONE", LastBackup: instant, LastSeen: instant}
			got := app.buildStatusSnapshot()
			if got.Timezone != zone || got.Devices[0].LastBackup != "2026-10-25 04:30" {
				t.Fatalf("snapshot=%+v", got)
			}
			parsed, err := time.Parse(time.RFC3339, got.Devices[0].LastBackupAt)
			if err != nil || !parsed.Equal(instant) {
				t.Fatalf("instant changed: %v %v", parsed, err)
			}
			if got.Devices[0].LastBackupAt != instant.In(app.deploymentLocation()).Format(time.RFC3339) {
				t.Fatal("wrong display offset")
			}
			if app.formatTimestamp(time.Time{}) != "" || !app.inDeploymentTime(time.Time{}).IsZero() {
				t.Fatal("zero timestamp lost")
			}
		})
	}
}

func TestDeploymentWindowAndDST(t *testing.T) {
	app := timeZoneApp(t, "America/New_York")
	for _, tc := range []struct {
		utc, start, end string
		inside          bool
	}{
		{"2026-03-08T06:59:00Z", "22:00", "06:00", true},
		{"2026-03-08T07:00:00Z", "22:00", "06:00", true},
		{"2026-03-08T10:00:00Z", "22:00", "06:00", false},
		{"2026-11-01T05:30:00Z", "01:00", "02:00", true},
		{"2026-11-01T06:30:00Z", "01:00", "02:00", true},
		{"2026-11-01T07:00:00Z", "01:00", "02:00", false},
		{"2026-11-01T07:00:00Z", "bad", "02:00", false},
	} {
		at, err := time.Parse(time.RFC3339, tc.utc)
		if err != nil {
			t.Fatal(err)
		}
		if got := inBackupWindow(app.inDeploymentTime(at), tc.start, tc.end); got != tc.inside {
			t.Errorf("%+v got %v", tc, got)
		}
	}
	previous := time.Date(2026, 3, 7, 12, 0, 0, 0, app.deploymentLocation())
	next := time.Date(2026, 3, 8, 12, 0, 0, 0, app.deploymentLocation())
	if app.inDeploymentTime(next).Sub(app.inDeploymentTime(previous)) != 23*time.Hour {
		t.Fatal("interval must use elapsed hours")
	}
	instant := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	if !inBackupWindow(instant, "22:00", "06:00") || inBackupWindow(instant.In(beijingLocation), "22:00", "06:00") {
		t.Fatal("same clock window must move when deployment TZ changes")
	}
}

func TestDeploymentTimezoneOnPages(t *testing.T) {
	app := timeZoneApp(t, "Asia/Kolkata")
	app.setupRoutes()
	for _, handler := range []http.HandlerFunc{app.handleHome, app.handleOnboarding, app.handleNotifications} {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), `data-timezone="Asia/Kolkata"`) {
			t.Fatalf("missing page timezone: status=%d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "{timezone}") {
			t.Fatal("unresolved timezone placeholder")
		}
	}
}

func TestNotificationAndLoggingUseDeploymentTimezone(t *testing.T) {
	app := timeZoneApp(t, "Asia/Kolkata")
	manager, err := newNotificationManagerFromConfig(&notificationConfig{Enabled: true}, nil, app.nowTime)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	sink := &MockNotifier{name: "test", enabled: true}
	if err := manager.AddNotifier(sink); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(notificationBackupSuccess): {"test"}})
	manager.SendBackupSuccess("Phone", "PHONE")
	msg := sink.WaitForSends(t, 1)
	_, offset := msg.Timestamp.Zone()
	if offset != 19800 {
		t.Fatalf("notification offset=%d", offset)
	}
	instant := time.Date(2026, 10, 24, 20, 30, 0, 0, time.UTC)
	var output bytes.Buffer
	writer := deploymentLogWriter{output: &output, now: func() time.Time { return app.inDeploymentTime(instant) }}
	line := []byte("event=test.done\n")
	n, err := writer.Write(line)
	if err != nil || n != len(line) || output.String() != "2026-10-25T02:00:00+05:30 event=test.done\n" {
		t.Fatalf("log=%q n=%d err=%v", output.String(), n, err)
	}
}

func TestHTTPStatusIncludesDeploymentTimezone(t *testing.T) {
	app := timeZoneApp(t, "UTC")
	rr := httptest.NewRecorder()
	app.handleStatus(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["timezone"] != "UTC" {
		t.Fatalf("status timezone=%v", payload["timezone"])
	}
}
