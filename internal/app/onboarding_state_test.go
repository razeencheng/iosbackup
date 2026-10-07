package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func operationStateForTest(app *application, udid string) deviceOperationState {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.deviceOperationStates[udid]
}

func TestPairingStatePublishesPaired(t *testing.T) {
	app := newApplication()
	app.cmdRunner = (&mockRunner{}).run
	device := &device{UDID: "PAIR-OK", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[device.UDID] = device

	app.pairDevice(device)

	state := operationStateForTest(app, device.UDID)
	if state.PairingState != pairingStatePaired || state.PairingErrorCode != "" || state.PairingError != "" {
		t.Fatalf("配对成功状态不正确: %+v", state)
	}
}

func TestPairingStatePublishesWaitingForTrust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newApplicationWithRuntime(ctx, defaultRuntimeConfig())
	runner := &mockRunner{outputFn: func(_ string, args []string, _ []string) ([]byte, error) {
		if argsHas(args, "validate") {
			return nil, errors.New("not paired")
		}
		return []byte("Please accept the trust dialog"), errors.New("trust required")
	}}
	app.cmdRunner = runner.run
	device := &device{UDID: "PAIR-WAIT", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[device.UDID] = device

	app.pairDevice(device)

	state := operationStateForTest(app, device.UDID)
	if state.PairingState != pairingStateWaitingForTrust || state.PairingErrorCode != "trust_required" {
		t.Fatalf("等待信任状态不正确: %+v", state)
	}
}

func TestPairingStatePublishesFailure(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{outputFn: func(_ string, args []string, _ []string) ([]byte, error) {
		if argsHas(args, "validate") {
			return nil, errors.New("not paired")
		}
		return []byte("pair transport failed"), errors.New("pair failed")
	}}
	app.cmdRunner = runner.run
	device := &device{UDID: "PAIR-FAIL", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[device.UDID] = device

	app.pairDevice(device)

	state := operationStateForTest(app, device.UDID)
	if state.PairingState != pairingStateFailed || state.PairingErrorCode != "pair_failed" || state.PairingError != "配对失败。请确认设备已解锁并保持 USB 连接，然后重试。" {
		t.Fatalf("配对失败状态不正确: %+v", state)
	}
}

func TestPairingFailureStateDoesNotExposeCommandOutput(t *testing.T) {
	rawUDID := strings.Repeat("A", 8) + "-" + strings.Repeat("B", 16)
	runner := &mockRunner{outputFn: func(_ string, args []string, _ []string) ([]byte, error) {
		if argsHas(args, "validate") {
			return nil, errors.New("not paired")
		}
		return []byte("ERROR: Could not validate with device " + rawUDID + " because a passcode is set. Please enter the passcode on the device and retry."), errors.New("pair failed")
	}}
	app := newApplication()
	app.cmdRunner = runner.run
	device := &device{UDID: rawUDID, IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[device.UDID] = device

	app.pairDevice(device)

	state := operationStateForTest(app, device.UDID)
	if strings.Contains(state.PairingError, rawUDID) {
		t.Fatalf("用户可见配对错误不得包含完整设备标识: %q", state.PairingError)
	}
	if state.PairingError != "设备已锁定。请解锁设备并输入锁屏密码，然后重试。" {
		t.Fatalf("锁屏错误提示不正确: %q", state.PairingError)
	}
}

func TestPairingRetryPublishesTimeout(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{outputFn: func(_ string, args []string, _ []string) ([]byte, error) {
		if argsHas(args, "validate") {
			return nil, errors.New("not paired")
		}
		return []byte("Please accept the trust dialog"), errors.New("trust required")
	}}
	app.cmdRunner = runner.run
	device := &device{UDID: "PAIR-TIMEOUT", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[device.UDID] = device

	app.retryPairAfterTrustWithSchedule(device.UDID, time.Millisecond, 2)

	state := operationStateForTest(app, device.UDID)
	if state.PairingState != pairingStateFailed || state.PairingErrorCode != "pair_timeout" {
		t.Fatalf("配对超时状态不正确: %+v", state)
	}
}

func TestHandlePairDeviceReturnsTrackableState(t *testing.T) {
	app := newApplication()
	app.cmdRunner = (&mockRunner{}).run
	app.devices["PAIR-HTTP"] = &device{UDID: "PAIR-HTTP", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	req := httptest.NewRequest(http.MethodPost, "/api/pair/PAIR-HTTP", nil)
	rr := httptest.NewRecorder()

	app.handlePairDevice(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("配对启动应返回 202，得到 %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["pairing_state"] != pairingStateChecking {
		t.Fatalf("响应应提供可跟踪状态，得到: %v", body)
	}
}

func TestBackupRuntimeStateClassifiesResult(t *testing.T) {
	app := newApplication()
	app.markBackupStarting("B-STATE")
	if got := operationStateForTest(app, "B-STATE"); got.BackupState != backupStateStarting || got.BackupErrorCode != "" {
		t.Fatalf("启动状态不正确: %+v", got)
	}

	app.markBackupRunning("B-STATE")
	if got := operationStateForTest(app, "B-STATE"); got.BackupState != backupStateRunning {
		t.Fatalf("运行状态不正确: %+v", got)
	}

	app.finishBackupState("B-STATE", nil)
	if got := operationStateForTest(app, "B-STATE"); got.BackupState != backupStateSucceeded || got.LastBackupError != "" {
		t.Fatalf("成功状态不正确: %+v", got)
	}

	app.finishBackupState("B-STATE", errDeviceDisconnected)
	if got := operationStateForTest(app, "B-STATE"); got.BackupState != backupStateInterrupted || got.BackupErrorCode != "device_disconnected" {
		t.Fatalf("中断状态不正确: %+v", got)
	}

	app.finishBackupState("B-STATE", errors.New("no space left on device"))
	if got := operationStateForTest(app, "B-STATE"); got.BackupState != backupStateFailed || got.BackupErrorCode != "storage_unavailable" {
		t.Fatalf("存储失败状态不正确: %+v", got)
	}
}

func TestPerformBackupMissingDevicePublishesFailure(t *testing.T) {
	app := newApplication()
	err := app.PerformBackup("MISSING-BACKUP")
	if err == nil {
		t.Fatal("不存在的设备应失败")
	}
	state := operationStateForTest(app, "MISSING-BACKUP")
	if state.BackupState != backupStateFailed || state.BackupErrorCode != "backup_failed" || state.LastBackupError == "" {
		t.Fatalf("异步失败必须保留结构化结果: %+v", state)
	}
}

func TestHandleBackupReturnsTrackableStartingState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newApplicationWithRuntime(ctx, defaultRuntimeConfig())
	app.devices["BACKUP-HTTP"] = &device{
		UDID:       "BACKUP-HTTP",
		IsOnline:   true,
		IsCharging: true,
		Connection: connectionTypeDesc(connectTypeUSB),
	}
	app.configs["BACKUP-HTTP"] = &backupConfig{
		UDID:             "BACKUP-HTTP",
		BackupDirectory:  t.TempDir(),
		MinBatteryLevel:  0,
		OnlyWhenCharging: false,
	}
	// handleBackup 同步占用并发槽，后台任务退出时才释放；先等它结束再清理临时目录。
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		for app.backupSem.inUse() != 0 {
			if time.Now().After(deadline) {
				t.Fatal("备份后台任务未在清理临时目录前退出")
			}
			time.Sleep(time.Millisecond)
		}
	})
	req := httptest.NewRequest(http.MethodPost, "/api/backup/BACKUP-HTTP", nil)
	rr := httptest.NewRecorder()

	app.handleBackup(rr, req)
	cancel()

	if rr.Code != http.StatusAccepted {
		t.Fatalf("备份启动应返回 202，得到 %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["backup_state"] != backupStateStarting {
		t.Fatalf("响应应提供 starting，得到: %v", body)
	}
}
