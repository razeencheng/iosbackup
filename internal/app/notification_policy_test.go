package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func automaticPairingTestApp(t *testing.T) (*application, *device, func()) {
	t.Helper()
	app := newApplication()
	d := &device{UDID: "PHONE", Name: "测试手机", IsOnline: true, BatteryLevel: 10, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.devices[d.UDID] = d
	cfg := app.defaultBackupConfig(d.UDID, d.Name)
	cfg.AutoBackupEnabled = true
	cfg.StartTime, cfg.EndTime = "00:00", "00:00"
	cfg.OnlyWhenCharging = false
	cfg.MinBatteryLevel = 50
	app.configs[d.UDID] = cfg
	return app, d, func() {
		app.checkAndBackup(d.UDID, *cfg, deviceStatus{UDID: d.UDID, Connection: connectionTypeDesc(connectTypeNetwork), IsOnline: true})
	}
}

func TestAutomaticBackupValidatesPairingBeforeDetails(t *testing.T) {
	app, d, check := automaticPairingTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	var commands []string
	app.cmdRunner = func(_ context.Context, bin string, args, _ []string) ([]byte, error) {
		commands = append(commands, bin+" "+strings.Join(args, " "))
		if bin == cmdIdevicePair {
			return []byte("ERROR: Device PHONE is not paired with this host"), errors.New("exit status 1")
		}
		// 旧实现会直接查电量；返回低电量防止它启动真实备份。
		return []byte("10"), nil
	}
	check()
	flush()
	if len(commands) != 1 || !strings.Contains(commands[0], "-n -u PHONE validate") {
		t.Errorf("到期检查应先校验网络配对，失败后不执行明细或备份命令: %v", commands)
	}
	if state := operationStateForTest(app, d.UDID); state.PairingErrorCode != "pairing_required" {
		t.Errorf("应保留需要配对的页面提示: %+v", state)
	}
	if sink.Count() != 0 {
		t.Fatal("首次校验失败不应立即推送")
	}
}

func TestAutomaticBackupValidPairingContinuesOnActualConnection(t *testing.T) {
	for _, connection := range []string{connectTypeUSB, connectTypeNetwork} {
		t.Run(connection, func(t *testing.T) {
			app, d, _ := automaticPairingTestApp(t)
			d.Connection = connectionTypeDesc(connection)
			var commands []string
			app.cmdRunner = func(_ context.Context, bin string, args, _ []string) ([]byte, error) {
				commands = append(commands, bin)
				if argsHas(args, "-n") != (connection == connectTypeNetwork) {
					t.Errorf("应沿用真实连接类型: %s %v", bin, args)
				}
				return []byte("10"), nil
			}
			app.checkAndBackup(d.UDID, *app.configs[d.UDID], deviceStatus{UDID: d.UDID, Connection: d.Connection, IsOnline: true})
			if len(commands) < 2 || commands[0] != cmdIdevicePair || commands[1] != cmdIdeviceInfo {
				t.Fatalf("配对通过后必须继续备份条件检查: %v", commands)
			}
		})
	}
}

func TestStalePairingFailureCannotNotifyAfterNewerSuccess(t *testing.T) {
	app, d, _ := automaticPairingTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	app.deviceOperationStates[d.UDID] = deviceOperationState{
		pairingAlert: pairingAlertState{since: nowBeijing().Add(-time.Hour), failures: 2},
	}
	old := app.pairingPublisher(d, true)
	newer := app.pairingStatePublisher(d)
	newer(d.UDID, pairingStatePaired, "", "")
	old(d.UDID, pairingStateFailed, "pairing_required", "旧错误")
	flush()
	if state := operationStateForTest(app, d.UDID); state.PairingState != pairingStatePaired || state.PairingErrorCode != "" {
		t.Fatalf("较旧检查不能覆盖新配对成功结果: %+v", state)
	}
	if sink.Count() != 0 {
		t.Fatal("过期检查结果不能发送配对告警")
	}
}

func TestStalePairingSuccessCannotApproveAutomaticBackup(t *testing.T) {
	app, d, _ := automaticPairingTestApp(t)
	started, finish := make(chan struct{}), make(chan struct{})
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		close(started)
		<-finish
		return nil, nil
	}
	result := make(chan bool, 1)
	go func() { result <- app.checkAutomaticBackupPairing(d) }()
	<-started
	newer := app.pairingStatePublisher(d)
	newer(d.UDID, pairingStateFailed, "pairing_required", "需要重新配对")
	close(finish)
	if <-result {
		t.Fatal("被新结果取代的旧成功不能允许自动备份继续")
	}
}

func TestStalePairingFailureCannotNotifyAfterConnectionChange(t *testing.T) {
	for _, change := range []string{"restart", "switch", "remove", "offline"} {
		t.Run(change, func(t *testing.T) {
			app, d, _ := automaticPairingTestApp(t)
			sink, flush := capturePolicyNotifications(t, app)
			app.deviceOperationStates[d.UDID] = deviceOperationState{
				pairingAlert: pairingAlertState{since: nowBeijing().Add(-time.Hour), failures: 2},
			}
			publish := app.pairingPublisher(cloneDevice(d), true)
			switch change {
			case "restart":
				app.connectionProcessStarted(connectionWiFi)
			case "switch":
				d.Connection = connectionTypeDesc(connectTypeUSB)
			case "remove":
				delete(app.devices, d.UDID)
			case "offline":
				d.IsOnline = false
			}
			publish(d.UDID, pairingStateFailed, "pairing_required", "旧错误")
			flush()
			if sink.Count() != 0 {
				t.Fatal("连接或设备已变化，旧检查不能告警")
			}
		})
	}
}

func TestPairingRequiredNotificationWaitsAndResetsAfterRecovery(t *testing.T) {
	app, d, check := automaticPairingTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	validation := "ERROR: Device PHONE is not paired with this host"
	app.cmdRunner = func(_ context.Context, bin string, _, _ []string) ([]byte, error) {
		if bin == cmdIdevicePair && validation != "" {
			return []byte(validation), errors.New("exit status 1")
		}
		return []byte("10"), nil
	}
	ageEvidence := func() {
		app.mu.Lock()
		state := app.deviceOperationStates[d.UDID]
		state.pairingAlert.since = nowBeijing().Add(-6 * time.Minute)
		app.deviceOperationStates[d.UDID] = state
		app.mu.Unlock()
	}
	for i := 0; i < 3; i++ {
		check()
	}
	flush()
	if sink.Count() != 0 {
		t.Fatal("短时间内连续检查不能绕过五分钟宽限")
	}
	ageEvidence()
	check()
	check()
	flush()
	if sink.Count() != 1 {
		t.Fatalf("持续缺少配对应且只应提醒一次: %d", sink.Count())
	}
	message := sink.LastMessage()
	if message.Title != "⚠️ 设备需要重新配对: 测试手机" || message.Level != notificationLevelWarning || !strings.Contains(message.Content, "USB") || message.DeviceName != d.Name {
		t.Fatalf("提醒必须说明设备和处理方式: %+v", message)
	}
	validation = "No device found with udid PHONE."
	check()
	validation = "ERROR: Device PHONE is not paired with this host"
	check()
	ageEvidence()
	check()
	check()
	flush()
	if sink.Count() != 1 {
		t.Fatal("瞬时离线不能重新武装同一配对告警")
	}
	validation = ""
	app.pairDevice(d)
	validation = "ERROR: Device PHONE is not paired with this host"
	check()
	ageEvidence()
	check()
	flush()
	if sink.Count() != 1 {
		t.Fatal("即使超过五分钟，也必须有至少三次失败观测")
	}
	check()
	flush()
	if sink.Count() != 2 {
		t.Fatalf("确认配对恢复后，新故障应允许再次提醒: %d", sink.Count())
	}
}

func TestManualPairingDoesNotEscalateAutomaticEvidence(t *testing.T) {
	app, d, check := automaticPairingTestApp(t)
	sink, flush := capturePolicyNotifications(t, app)
	app.cmdRunner = func(_ context.Context, _ string, _, _ []string) ([]byte, error) {
		return []byte("ERROR: Device PHONE is not paired with this host"), errors.New("exit status 1")
	}
	check()
	state := app.deviceOperationStates[d.UDID]
	state.pairingAlert.since = nowBeijing().Add(-time.Hour)
	app.deviceOperationStates[d.UDID] = state
	for i := 0; i < 3; i++ {
		app.pairDevice(d)
	}
	flush()
	if sink.Count() != 0 {
		t.Fatal("手动配对只通过页面反馈，不能触发后台告警")
	}
	app.configs[d.UDID].AutoBackupEnabled = false
	check()
	flush()
	if sink.Count() != 0 {
		t.Fatal("关闭自动备份后不能提醒自动备份受阻")
	}
}

func TestConnectionFailureNotificationsAreDeduplicated(t *testing.T) {
	app := newApplication()
	sink, flush := capturePolicyNotifications(t, app)
	base := nowBeijing()
	var sequence uint64
	observe := func(err error, after time.Duration) {
		sequence++
		app.publishConnectionScan(connectionWiFi, connectionScan{
			sequence: sequence, generation: app.connectionServices[connectionWiFi].generation,
			completed: base.Add(after), err: err, code: "query_failed",
		})
		flush()
	}
	failure := errors.New("query unavailable")
	observe(failure, 0)
	observe(failure, time.Second)
	if sink.Count() != 0 {
		t.Fatal("短暂连接故障不应推送")
	}
	observe(failure, 2*time.Second)
	if sink.Count() != 1 {
		t.Errorf("连续三次查询失败应提醒一次，实际 %d", sink.Count())
	}
	app.connectionProcessStarted(connectionWiFi)
	observe(failure, time.Minute)
	observe(failure, 61*time.Second)
	observe(failure, 62*time.Second)
	if sink.Count() != 1 {
		t.Errorf("重启未恢复不能重复提醒，实际 %d", sink.Count())
	}
	observe(nil, 2*time.Minute)
	observe(nil, 3*time.Minute)
	observe(failure, 181*time.Second)
	observe(failure, 182*time.Second)
	observe(failure, 183*time.Second)
	if sink.Count() != 2 {
		t.Errorf("稳定恢复后新的持续故障应重新提醒，实际 %d", sink.Count())
	}
}

type passwordWriteFailureStore struct{ secretStore }

func (passwordWriteFailureStore) SetBackupPassword(string, string) error {
	return errors.New("storage unavailable")
}

func TestPasswordStorageFailureStillNotifies(t *testing.T) {
	app, _ := newEncTestApp(t, "PHONE")
	app.secretStore = passwordWriteFailureStore{app.secretStore}
	sink, flush := capturePolicyNotifications(t, app)
	if err := app.SetBackupEncryption(context.Background(), "PHONE", true, "test-password"); err != nil {
		t.Fatal(err)
	}
	flush()
	if sink.Count() != 1 || !strings.Contains(sink.LastMessage().Content, "密码存储失败") {
		t.Fatal("已开启加密却无法保存密码必须保留重要告警")
	}
}
