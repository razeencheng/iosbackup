package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const removalTestCSRFToken = "device-removal-test-token"

func newDeviceRemovalHTTPTestApp(t *testing.T, udid string) *application {
	t.Helper()
	app, _ := newDeviceRemovalTestApp(t, udid)
	app.csrfManager = &csrfManager{token: removalTestCSRFToken}
	return app
}

func deviceRemovalRequest(handler http.Handler, method, path string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if csrf {
		req.Header.Set(csrfHeader, removalTestCSRFToken)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeRemovalResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是有效 JSON: %v, body=%q", err, rr.Body.String())
	}
	return body
}

func TestHandleRemovedDevicesListsOnlySafeFieldsInNewestFirstOrder(t *testing.T) {
	const firstUDID = "REMOVE-LIST-OLDER"
	app := newDeviceRemovalHTTPTestApp(t, firstUDID)
	app.mu.Lock()
	app.devices[firstUDID].DeviceType = "iPhone14,4"
	app.configs[firstUDID].DeviceType = "iPhone14,4"
	app.mu.Unlock()
	older, err := app.removeDevice(firstUDID)
	if err != nil {
		t.Fatal(err)
	}

	const secondUDID = "REMOVE-LIST-NEWER"
	second := *app.defaultBackupConfig(secondUDID, "家庭 iPad")
	second.BackupDirectory = app.paths.BackupsRoot
	second.NetworkAddress = "192.0.2.99"
	second.RestoreEnabled = true
	second.LastBackup = time.Date(2026, 8, 14, 8, 30, 0, 0, beijingLocation)
	second.DeviceType = "iPad14,1"
	app.mu.Lock()
	app.configs[secondUDID] = cloneBackupConfig(&second)
	app.devices[secondUDID] = &device{UDID: secondUDID, Name: second.Name, DeviceType: "iPad14,1", IsOnline: true}
	app.mu.Unlock()
	newer, err := app.removeDevice(secondUDID)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Before(older) {
		t.Fatalf("测试前提错误: newer=%s older=%s", newer, older)
	}

	rr := deviceRemovalRequest(app.setupRoutes(), http.MethodGet, "/api/removed-devices", false)
	if rr.Code != http.StatusOK {
		t.Fatalf("列表应返回 200，得到 %d: %s", rr.Code, rr.Body.String())
	}
	var response struct {
		Count   int `json:"count"`
		Devices []struct {
			UDID            string `json:"udid"`
			Name            string `json:"name"`
			DeviceType      string `json:"device_type"`
			RemovedAt       string `json:"removed_at"`
			LastBackup      string `json:"last_backup"`
			BackupPreserved bool   `json:"backup_preserved"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 2 || len(response.Devices) != 2 {
		t.Fatalf("移除列表数量错误: %+v", response)
	}
	if response.Devices[0].UDID != secondUDID || response.Devices[0].DeviceType != "iPad14,1" {
		t.Fatalf("列表应按移除时间倒序并保留型号: %+v", response.Devices)
	}
	if !response.Devices[0].BackupPreserved || response.Devices[0].RemovedAt == "" || response.Devices[0].LastBackup == "" {
		t.Fatalf("列表展示字段不完整: %+v", response.Devices[0])
	}
	for _, secretField := range []string{"backup_directory", "network_address", "restore_enabled", "password", "secret"} {
		if strings.Contains(strings.ToLower(rr.Body.String()), secretField) {
			t.Fatalf("移除列表不得泄露字段 %q: %s", secretField, rr.Body.String())
		}
	}
}

func TestHandleRemoveAndRestoreDeviceAreIdempotent(t *testing.T) {
	const udid = "REMOVE-HTTP-IDEMPOTENT"
	app := newDeviceRemovalHTTPTestApp(t, udid)
	handler := app.setupRoutes()

	first := deviceRemovalRequest(handler, http.MethodPost, "/api/remove-device/"+udid, true)
	if first.Code != http.StatusOK {
		t.Fatalf("首次移除应返回 200，得到 %d: %s", first.Code, first.Body.String())
	}
	firstBody := decodeRemovalResponse(t, first)
	if firstBody["removed_at"] == "" || firstBody["message"] == "" {
		t.Fatalf("移除成功响应字段不完整: %+v", firstBody)
	}
	second := deviceRemovalRequest(handler, http.MethodPost, "/api/remove-device/"+udid, true)
	if second.Code != http.StatusOK {
		t.Fatalf("重复移除应保持 200，得到 %d: %s", second.Code, second.Body.String())
	}
	if got := decodeRemovalResponse(t, second)["removed_at"]; got != firstBody["removed_at"] {
		t.Fatalf("重复移除不得改写时间: first=%v second=%v", firstBody["removed_at"], got)
	}

	for i := 0; i < 2; i++ {
		rr := deviceRemovalRequest(handler, http.MethodPost, "/api/restore-device/"+udid, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("第 %d 次恢复应返回 200，得到 %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}
	app.mu.RLock()
	device := cloneDevice(app.devices[udid])
	config := cloneBackupConfig(app.configs[udid])
	app.mu.RUnlock()
	if device == nil || device.IsOnline || config == nil || config.RemovedAt != nil {
		t.Fatalf("恢复后应仅恢复离线卡片与原配置: device=%+v config=%+v", device, config)
	}
}

func TestDeviceRemovalHTTPContractRejectsInvalidRequests(t *testing.T) {
	const udid = "REMOVE-HTTP-ERRORS"
	app := newDeviceRemovalHTTPTestApp(t, udid)
	handler := app.setupRoutes()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		csrf   bool
		want   int
	}{
		{"列表错误方法", http.MethodPost, "/api/removed-devices", true, http.StatusMethodNotAllowed},
		{"移除错误方法", http.MethodGet, "/api/remove-device/" + udid, false, http.StatusMethodNotAllowed},
		{"恢复错误方法", http.MethodGet, "/api/restore-device/" + udid, false, http.StatusMethodNotAllowed},
		{"移除缺少 CSRF", http.MethodPost, "/api/remove-device/" + udid, false, http.StatusForbidden},
		{"恢复缺少 CSRF", http.MethodPost, "/api/restore-device/" + udid, false, http.StatusForbidden},
		{"非法 UDID", http.MethodPost, "/api/remove-device/not%20valid", true, http.StatusBadRequest},
		{"不存在设备", http.MethodPost, "/api/remove-device/VALID-NOT-FOUND", true, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := deviceRemovalRequest(handler, tc.method, tc.path, tc.csrf)
			if rr.Code != tc.want {
				t.Fatalf("得到 %d，期望 %d: %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestHandleRemoveDeviceReturnsStableConflictAndRollsBackPersistenceFailure(t *testing.T) {
	const udid = "REMOVE-HTTP-CONFLICT"
	app := newDeviceRemovalHTTPTestApp(t, udid)
	handler := app.setupRoutes()
	app.mu.Lock()
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	rr := deviceRemovalRequest(handler, http.MethodPost, "/api/remove-device/"+udid, true)
	if rr.Code != http.StatusConflict || decodeRemovalResponse(t, rr)["code"] != "busy" {
		t.Fatalf("忙碌设备应返回稳定冲突码: %d %s", rr.Code, rr.Body.String())
	}
	app.mu.Lock()
	delete(app.backupInProgress, udid)
	app.mu.Unlock()

	app.configStore.lastGoodPath = t.TempDir()
	rr = deviceRemovalRequest(handler, http.MethodPost, "/api/remove-device/"+udid, true)
	if rr.Code != http.StatusInternalServerError || decodeRemovalResponse(t, rr)["code"] != "persistence_failed" {
		t.Fatalf("落盘失败应返回稳定服务端错误码: %d %s", rr.Code, rr.Body.String())
	}
	app.mu.RLock()
	_, visible := app.devices[udid]
	config := cloneBackupConfig(app.configs[udid])
	pending := app.deviceRemovalPending[udid]
	app.mu.RUnlock()
	if !visible || config == nil || config.RemovedAt != nil || pending {
		t.Fatalf("落盘失败后不得部分发布: visible=%v pending=%v config=%+v", visible, pending, config)
	}
}

func TestDeviceRemovalRoutesRemainBehindAuthentication(t *testing.T) {
	app := newLoginTestApp(t)
	handler := app.setupRoutes()
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/removed-devices"},
		{http.MethodPost, "/api/remove-device/AUTH-DEVICE"},
		{http.MethodPost, "/api/restore-device/AUTH-DEVICE"},
	} {
		rr := deviceRemovalRequest(handler, request.method, request.path, request.method == http.MethodPost)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s 未认证应返回 401，得到 %d: %s", request.method, request.path, rr.Code, rr.Body.String())
		}
	}
}

func TestBuildStatusSnapshotRemovedCount(t *testing.T) {
	const udid = "REMOVE-SNAPSHOT-COUNT"
	app, _ := newDeviceRemovalTestApp(t, udid)
	removedAt := nowBeijing()
	app.mu.Lock()
	app.configs[udid].RemovedAt = &removedAt
	app.mu.Unlock()

	snapshot := app.buildStatusSnapshot()
	if snapshot.RemovedDeviceCount != 1 {
		t.Fatalf("SSE 快照应发布已移除设备数量，得到 %+v", snapshot)
	}
	if len(snapshot.Devices) != 0 {
		t.Fatalf("SSE 活动设备不得包含已移除设备: %+v", snapshot.Devices)
	}
}
