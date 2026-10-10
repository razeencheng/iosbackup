package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 单 worker 的另一事件充当队列屏障，确保断言覆盖异步通知而无需 sleep。
func capturePolicyNotifications(t *testing.T, app *application) (*MockNotifier, func()) {
	t.Helper()
	manager := newNotificationManagerWithLimits(1, 100)
	t.Cleanup(manager.Close)
	sink := &MockNotifier{name: "policy_events", enabled: true}
	barrier := &MockNotifier{name: "policy_barrier", enabled: true}
	for _, notifier := range []*MockNotifier{sink, barrier} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetNotificationRules(map[string][]string{
		string(notificationSystemError):   {sink.name},
		string(notificationBackupFailed):  {sink.name},
		string(notificationBackupSuccess): {barrier.name},
	})
	app.replaceNotificationManager(manager)
	return sink, func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result := manager.SendAndWait(ctx, &notificationMessage{Type: notificationBackupSuccess})
		if result.Succeeded != 1 {
			t.Fatalf("等待通知队列失败: %+v", result)
		}
	}
}

func TestPairingValidationFailureDoesNotRePair(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		err                error
	}{
		{"missing", "No device found with udid PHONE.", "device_unavailable", errors.New("exit status 1")},
		{"timeout", "", "connection_timeout", context.DeadlineExceeded},
		{"transport", "ERROR: Could not connect to lockdownd, error code -8", "connection_unavailable", errors.New("exit status 1")},
		{"locked", "ERROR: Could not validate with device PHONE because a passcode is set.", "device_locked", errors.New("exit status 1")},
		{"prohibited", "ERROR: Pairing is not possible over this connection.\nTo perform a wireless pairing use the -w command line switch.", "pairing_not_supported", errors.New("exit status 1")},
		{"unknown", "unhandled error code -99", "pair_validation_failed", errors.New("exit status 1")},
	} {
		for _, connection := range []string{connectTypeUSB, connectTypeNetwork} {
			t.Run(tc.name+"/"+connection, func(t *testing.T) {
				app := newApplication()
				d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connection)}
				app.devices[d.UDID] = d
				sink, flush := capturePolicyNotifications(t, app)
				var commands []string
				app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
					commands = append(commands, args[len(args)-1])
					return []byte(tc.output), tc.err
				}
				app.pairDevice(d)
				flush()
				if strings.Join(commands, ",") != "validate" {
					t.Errorf("校验失败不得盲目重新配对: %v", commands)
				}
				state := operationStateForTest(app, d.UDID)
				if state.PairingErrorCode != tc.code || state.PairingError == "" || strings.Contains(state.PairingError, "PHONE") {
					t.Errorf("错误分类或页面提示不正确: %+v", state)
				}
				if sink.Count() != 0 {
					t.Errorf("单次配对检查不得推送系统告警: %d", sink.Count())
				}
			})
		}
	}
}

func TestPairingNetworkRequiresUSBWithoutPairCommand(t *testing.T) {
	app := newApplication()
	d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	app.devices[d.UDID] = d
	var commands []string
	app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		commands = append(commands, args[len(args)-1])
		return []byte("ERROR: Device PHONE is not paired with this host"), errors.New("exit status 1")
	}
	app.pairDevice(d)
	state := operationStateForTest(app, d.UDID)
	if strings.Join(commands, ",") != "validate" || state.PairingErrorCode != "pairing_required" || !strings.Contains(state.PairingError, "USB") {
		t.Fatalf("网络设备应提示 USB 配对且不执行 pair: commands=%v state=%+v", commands, state)
	}
}

func TestPairingDeviceDisappearsBeforeUSBPair(t *testing.T) {
	app := newApplication()
	d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.devices[d.UDID] = d
	sink, flush := capturePolicyNotifications(t, app)
	app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		if argsHas(args, "validate") {
			return []byte("ERROR: Device PHONE is not paired with this host"), errors.New("exit status 1")
		}
		return []byte("No device found with udid PHONE."), errors.New("exit status 1")
	}
	app.pairDevice(d)
	flush()
	if state := operationStateForTest(app, d.UDID); state.PairingErrorCode != "device_unavailable" {
		t.Errorf("执行 pair 前离线应报告设备不可见: %+v", state)
	}
	if sink.Count() != 0 {
		t.Fatal("USB 配对期间掉线不应推送系统错误")
	}
}

func TestPairingTrustRetryDoesNotRePairUnavailableOrNetworkDevice(t *testing.T) {
	for _, connection := range []string{connectTypeUSB, connectTypeNetwork} {
		t.Run(connection, func(t *testing.T) {
			app := newApplication()
			d := &device{UDID: "PHONE", IsOnline: true, Connection: connectionTypeDesc(connection)}
			app.devices[d.UDID] = d
			pairCalls := 0
			app.cmdRunner = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
				if argsHas(args, "pair") {
					pairCalls++
				}
				if connection == connectTypeNetwork {
					return []byte("ERROR: Device PHONE is not paired with this host"), errors.New("exit status 1")
				}
				return []byte("No device found with udid PHONE."), errors.New("exit status 1")
			}
			app.retryPairAfterTrustWithSchedule(d.UDID, time.Millisecond, 2)
			if pairCalls != 0 {
				t.Fatalf("信任重试不能在离线或网络连接上重新配对: %d", pairCalls)
			}
		})
	}
}

func TestErrorLoggingDoesNotSendNotification(t *testing.T) {
	app := newApplication()
	sink, flush := capturePolicyNotifications(t, app)
	app.addErrorLog("PHONE", "普通操作错误")
	flush()
	if sink.Count() != 0 {
		t.Fatal("写错误日志不应隐式发送通知")
	}
}
