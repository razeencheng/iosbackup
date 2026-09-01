package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func p4App(t *testing.T, udid, devName string) (*application, *mockRunner, chan struct{}) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	store, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = store
	app.mu.Lock()
	app.devices[udid] = &device{UDID: udid, Name: devName, Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	app.configs[udid] = &backupConfig{UDID: udid, Name: devName, BackupDirectory: "/backups"}
	app.mu.Unlock()
	done := make(chan struct{}, 1)
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		select {
		case done <- struct{}{}:
		default:
		}
		return nil, nil
	}
	return app, runner, done
}

func withCSRF(req *http.Request) *http.Request {
	req.Header.Set(csrfHeader, "1")
	return req
}

// waitOpFinished 等后台 P4 操作彻底结束（backupInProgress 清空），避免其 secretStore 写入
// 与 t.TempDir() 清理相竞争（纯测试侧时序，非生产问题：生产 secrets.enc 在固定 /configs）。
func waitOpFinished(t *testing.T, app *application, udid string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.RLock()
		busy := app.backupInProgress[udid]
		app.mu.RUnlock()
		if !busy {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("后台操作未在超时内完成（backupInProgress 未清）")
}

func TestHandleEncryptionRequiresCSRF(t *testing.T) {
	app, _, _ := p4App(t, "ENC-EP", "Dev")
	req := httptest.NewRequest(http.MethodPost, "/api/encryption/ENC-EP", strings.NewReader(`{"enable":true,"password":"pw"}`))
	rr := httptest.NewRecorder()
	app.handleEncryption(rr, req) // 无 CSRF 头
	if rr.Code != http.StatusForbidden {
		t.Errorf("缺 CSRF 头应 403，得到 %d", rr.Code)
	}
}

func TestHandleEncryptionOK(t *testing.T) {
	app, _, done := p4App(t, "ENC-EP2", "Dev")
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/encryption/ENC-EP2", strings.NewReader(`{"enable":true,"password":"pw"}`)))
	rr := httptest.NewRecorder()
	app.handleEncryption(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d，body=%s", rr.Code, rr.Body.String())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("加密操作后台未执行")
	}
	waitOpFinished(t, app, "ENC-EP2")
}

func TestHandleEncryptionBoundedContext(t *testing.T) {
	app := newApplication()
	store, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = store
	app.mu.Lock()
	app.devices["BCTX"] = &device{UDID: "BCTX", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: true}
	app.configs["BCTX"] = &backupConfig{UDID: "BCTX", BackupDirectory: "/backups"}
	app.mu.Unlock()

	done := make(chan bool, 1)
	app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
		_, hasDeadline := ctx.Deadline()
		select {
		case done <- hasDeadline:
		default:
		}
		return nil, nil
	}

	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/encryption/BCTX", strings.NewReader(`{"enable":true,"password":"pw"}`)))
	rr := httptest.NewRecorder()
	app.handleEncryption(rr, req)

	select {
	case hasDeadline := <-done:
		if !hasDeadline {
			t.Error("P4 加密操作的 context 应有 deadline（防挂死锁定设备槽）")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("加密操作未在超时内执行")
	}
	waitOpFinished(t, app, "BCTX")
}

func TestHandleChangePasswordOK(t *testing.T) {
	app, _, done := p4App(t, "CHG-EP", "Dev")
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/backup-changepw/CHG-EP", strings.NewReader(`{"old":"o","new":"n"}`)))
	rr := httptest.NewRecorder()
	app.handleChangePassword(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d", rr.Code)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("改密后台未执行")
	}
	waitOpFinished(t, app, "CHG-EP")
}

func TestHandleRestoreDisabledByDefault(t *testing.T) {
	app, _, _ := p4App(t, "RST-EP", "MyiPad")
	// RestoreEnabled 默认 false
	body := `{"confirmations":{"understand":true,"device_name":"MyiPad"}}`
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/restore/RST-EP", strings.NewReader(body)))
	rr := httptest.NewRecorder()
	app.handleRestore(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("RestoreEnabled=false 应 403，得到 %d", rr.Code)
	}
}

func TestHandleRestoreRequiresCSRF(t *testing.T) {
	app, _, _ := p4App(t, "RST-CSRF", "MyiPad")
	req := httptest.NewRequest(http.MethodPost, "/api/restore/RST-CSRF", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	app.handleRestore(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("缺 CSRF 应 403，得到 %d", rr.Code)
	}
}

func TestHandleRestoreWrongDeviceName(t *testing.T) {
	app, _, _ := p4App(t, "RST-WN", "MyiPad")
	app.mu.Lock()
	app.configs["RST-WN"].RestoreEnabled = true
	app.mu.Unlock()
	body := `{"confirmations":{"understand":true,"device_name":"WrongName"}}`
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/restore/RST-WN", strings.NewReader(body)))
	rr := httptest.NewRecorder()
	app.handleRestore(rr, req)
	if rr.Code == http.StatusOK {
		t.Error("设备名不匹配不应放行")
	}
}

func TestHandleRestoreNeedsUnderstand(t *testing.T) {
	app, _, _ := p4App(t, "RST-U", "MyiPad")
	app.mu.Lock()
	app.configs["RST-U"].RestoreEnabled = true
	app.mu.Unlock()
	body := `{"confirmations":{"understand":false,"device_name":"MyiPad"}}`
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/restore/RST-U", strings.NewReader(body)))
	rr := httptest.NewRecorder()
	app.handleRestore(rr, req)
	if rr.Code == http.StatusOK {
		t.Error("未确认 understand 不应放行")
	}
}

func TestHandleRestoreOK(t *testing.T) {
	app, _, done := p4App(t, "RST-OK", "MyiPad")
	app.mu.Lock()
	app.configs["RST-OK"].RestoreEnabled = true
	app.mu.Unlock()
	// device_name 带空白，应 trim 后精确匹配
	body := `{"confirmations":{"understand":true,"device_name":"  MyiPad  "},"password":"pw","options":{"reboot":true}}`
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/restore/RST-OK", strings.NewReader(body)))
	rr := httptest.NewRecorder()
	app.handleRestore(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("多重确认齐全应 200，得到 %d，body=%s", rr.Code, rr.Body.String())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("恢复后台未执行")
	}
}
