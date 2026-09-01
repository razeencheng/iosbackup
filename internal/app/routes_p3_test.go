package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandleStatusEncryptionFields(t *testing.T) {
	app := newApplication() // secretStore 为 nil（未配置密钥）
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()
	app.handleStatus(rr, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	v, ok := resp["encryption_available"]
	if !ok {
		t.Fatal("status 应包含 encryption_available 字段")
	}
	if v != false {
		t.Errorf("无密钥时 encryption_available 应为 false，得到 %v", v)
	}
	if _, ok := resp["encryption_key_configured"]; !ok {
		t.Error("status 应包含 encryption_key_configured 字段")
	}
}

func TestHandleStatusEncryptionAvailableTrue(t *testing.T) {
	app := newApplication()
	store, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = store

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()
	app.handleStatus(rr, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["encryption_available"] != true {
		t.Errorf("配置有效密钥后 encryption_available 应为 true，得到 %v", resp["encryption_available"])
	}
}

func TestHandleBackupInfoEndpoint(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		return []byte("Backup version 3.3\nIsEncrypted: No"), nil
	}
	addOnlineDevice(app, "EP-INFO", connectionTypeDesc(connectTypeUSB))

	req := httptest.NewRequest(http.MethodGet, "/api/backup-info/EP-INFO", nil)
	rr := httptest.NewRecorder()
	app.handleBackupInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d，body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	info, _ := resp["info"].(string)
	if !strings.Contains(info, "Backup version") {
		t.Errorf("响应应含 info 输出，得到 %v", resp)
	}
}

func TestHandleBackupListEndpoint(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		return []byte("a.db,AppDomain,100\nb.plist,HomeDomain,20\n"), nil
	}
	addOnlineDevice(app, "EP-LIST", connectionTypeDesc(connectTypeUSB))

	req := httptest.NewRequest(http.MethodGet, "/api/backup-list/EP-LIST", nil)
	rr := httptest.NewRecorder()
	app.handleBackupList(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d，body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["count"].(float64) != 2 {
		t.Errorf("count 应为 2，得到 %v", resp["count"])
	}
}

func TestHandleBackupInfoWrongMethod(t *testing.T) {
	app := newApplication()
	req := httptest.NewRequest(http.MethodPost, "/api/backup-info/X", nil)
	rr := httptest.NewRecorder()
	app.handleBackupInfo(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 到只读端点应 405，得到 %d", rr.Code)
	}
}

func TestHandleBackupUnbackEndpoint(t *testing.T) {
	app, backupPath := newLocalUnbackTestApp(t, "EP-UNBACK")
	app.enableExperimentalOperations = true
	started := make(chan struct{})
	releaseStreamer := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseStreamer) }) }
	defer release()
	app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error {
		close(started)
		<-releaseStreamer
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/backup-unback/EP-UNBACK", nil)
	req.Header.Set("X-IOSBK-CSRF", "1") // 端点现要求 CSRF 头
	rr := httptest.NewRecorder()
	app.handleBackupUnback(rr, req)

	// 长操作后台执行，端点应立即 200
	if rr.Code != http.StatusOK {
		t.Fatalf("unback 端点应立即 200，得到 %d", rr.Code)
	}
	// 后台确应触发本地 Manifest.db 解包
	startTimeout := time.NewTimer(time.Second)
	defer startTimeout.Stop()
	select {
	case <-started:
	case <-startTimeout.C:
		t.Fatal("unback 后台命令未在超时内执行")
	}
	if !backupOperationInProgress(app, "EP-UNBACK") {
		t.Fatal("Manifest 流处理期间应保持 unback 操作状态")
	}
	release()
	waitForBackupOperationIdle(t, app, "EP-UNBACK")

	markerPath := filepath.Join(backupPath, "_unback_", ".iosbackup-unback.json")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("unback 后台任务完成后应发布完成标记: %v", err)
	}
	var summary unbackSummary
	if err := json.Unmarshal(marker, &summary); err != nil {
		t.Fatalf("解析 unback 完成标记失败: %v", err)
	}
	if summary.CompletedAt == "" || summary.Files != 0 || summary.Directories != 0 || summary.SkippedSymlinks != 0 {
		t.Fatalf("unback 完成标记内容错误: %+v", summary)
	}
	entries, err := os.ReadDir(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".iosbackup-unback-") {
			t.Fatalf("unback 完成后不应残留临时目录 %s", entry.Name())
		}
	}
}

func backupOperationInProgress(app *application, udid string) bool {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.backupInProgress[udid]
}

// waitForBackupOperationIdle 等待 Unback 的 defer release 执行；这是后台任务返回前的最后一个可观测状态变化。
func waitForBackupOperationIdle(t *testing.T, app *application, udid string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		if !backupOperationInProgress(app, udid) {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("unback 后台任务未在超时内完成")
		}
	}
}
