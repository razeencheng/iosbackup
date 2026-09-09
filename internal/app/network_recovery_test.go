package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fixture 模拟外部 netmuxd：心跳失败会清空注册表，但 IP 仍可访问，
// 且不再发送新的 mDNS 事件。只有正确 socket 上的 add_device 才恢复注册。
type recoveryFixture struct {
	app        *application
	registered map[string]string
	attempts   []string
	reachable  bool
	lookupErr  error
	helperErr  error
}

func TestNetworkRegistrationCancelledWithApplication(t *testing.T) {
	f := newRecoveryFixture()
	ctx, cancel := context.WithCancel(context.Background())
	f.app.rootCtx = ctx
	cancel()
	if err := f.app.callAddDevice("PHONE", "192.0.2.11"); err == nil || len(f.attempts) != 0 {
		t.Fatalf("应用关闭后仍注册设备：err=%v attempts=%v", err, f.attempts)
	}
}

func TestNetworkRegistrationReconcilesHelperTimeout(t *testing.T) {
	f := newRecoveryFixture()
	base := f.app.cmdRunner
	f.app.cmdRunner = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		out, err := base(ctx, name, args, env)
		if name == cmdAddDevice {
			return nil, context.DeadlineExceeded
		}
		return out, err
	}
	if err := f.app.callAddDevice("PHONE", "192.0.2.11"); err != nil {
		t.Fatalf("helper 超时但注册表已确认在线，不应报连接失败：%v", err)
	}
}

func TestNetworkRegistrationDoesNotOverlapReplayAndManualRequest(t *testing.T) {
	app := newApplication()
	app.configs["PHONE"] = &backupConfig{UDID: "PHONE", NetworkAddress: "192.0.2.11"}
	app.reachProbe = func(string) (bool, string) { return true, "" }
	var calls atomic.Int32
	started, release, replayDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	app.cmdRunner = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		if name == cmdAddDevice {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return []byte("Success"), nil
		}
		return nil, nil
	}
	go func() { app.replayAddDevice(); close(replayDone) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("replay 未发起注册")
	}
	manualDone := make(chan struct{})
	go func() { _ = app.callAddDevice("PHONE", "192.0.2.11"); close(manualDone) }()
	select {
	case <-manualDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-replayDone
	<-manualDone
	if calls.Load() != 1 {
		t.Fatalf("重放与手动测试重复注册同一设备：%d 次", calls.Load())
	}
}

func newRecoveryFixture() *recoveryFixture {
	f := &recoveryFixture{app: newApplication(), registered: map[string]string{}, reachable: true}
	f.app.configs["PHONE"] = &backupConfig{UDID: "PHONE"}
	f.app.devices["PHONE"] = &device{UDID: "PHONE"}
	f.app.networkIPLookup = func(context.Context) (map[string]string, error) {
		result := make(map[string]string)
		for udid, ip := range f.registered {
			result[udid] = ip
		}
		return result, f.lookupErr
	}
	f.app.reachProbe = func(string) (bool, string) { return f.reachable, "测试：设备不可达" }
	f.app.cmdRunner = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		if name == cmdAddDevice {
			if envGet(env, "USBMUXD_SOCKET_ADDRESS") != netmuxdAddr {
				return nil, errors.New("注册请求送到了 USB socket")
			}
			f.attempts = append(f.attempts, args[1])
			if f.helperErr != nil {
				return nil, f.helperErr
			}
			f.registered[args[0]] = args[1]
			return []byte("Success"), nil
		}
		if name == cmdIdeviceID && envGet(env, "USBMUXD_SOCKET_ADDRESS") == netmuxdAddr {
			var listed string
			for udid := range f.registered {
				listed += udid + " (Network)\n"
			}
			return []byte(listed), nil
		}
		return nil, nil
	}
	return f
}

func TestNetworkRecoveryWithoutManualIP(t *testing.T) {
	f := newRecoveryFixture()
	now := time.Now()
	f.registered["PHONE"] = "192.0.2.11"
	f.app.recoverNetworkDevices(context.Background(), now)
	delete(f.registered, "PHONE") // 心跳超时，mDNS 没有新的 ServiceResolved 事件。
	f.app.recoverNetworkDevices(context.Background(), now.Add(30*time.Second))
	f.app.refreshPresence()
	if len(f.attempts) != 1 || f.registered["PHONE"] != "192.0.2.11" || !f.app.devices["PHONE"].IsOnline {
		t.Fatalf("手机恢复可访问后仍未自动上线：attempts=%v registered=%v online=%v", f.attempts, f.registered, f.app.devices["PHONE"].IsOnline)
	}
	if f.app.configs["PHONE"].NetworkAddress != "" {
		t.Fatal("自动恢复不能把 mDNS 缓存写入用户的手动 IP 配置")
	}
	f.app.recoverNetworkDevices(context.Background(), now.Add(time.Minute))
	if len(f.attempts) != 1 {
		t.Fatal("已恢复的设备被重复注册")
	}
}

func TestNetworkRecoveryManualIPAndBackoff(t *testing.T) {
	f := newRecoveryFixture()
	f.app.configs["PHONE"].NetworkAddress = "192.0.2.12"
	f.helperErr = errors.New("手机拒绝会话")
	now := time.Now()
	f.app.recoverNetworkDevices(context.Background(), now)
	f.app.recoverNetworkDevices(context.Background(), now.Add(time.Second))
	if len(f.attempts) != 1 || f.app.devices["PHONE"].IsOnline {
		t.Fatalf("失败应保持离线并限速：attempts=%v", f.attempts)
	}
	f.helperErr = nil
	f.app.recoverNetworkDevices(context.Background(), now.Add(3*time.Minute))
	if len(f.attempts) != 2 || f.registered["PHONE"] != "192.0.2.12" {
		t.Fatalf("退避后未重试手动 IP：%v", f.attempts)
	}
}

func TestNetworkRecoveryUsesNewlyDiscoveredAddress(t *testing.T) {
	f := newRecoveryFixture()
	now := time.Now()
	f.registered["PHONE"] = "192.0.2.11"
	f.app.recoverNetworkDevices(context.Background(), now)
	f.registered["PHONE"] = "192.0.2.22"
	f.app.recoverNetworkDevices(context.Background(), now.Add(time.Minute))
	delete(f.registered, "PHONE")
	f.app.recoverNetworkDevices(context.Background(), now.Add(2*time.Minute))
	if len(f.attempts) != 1 || f.attempts[0] != "192.0.2.22" {
		t.Fatalf("恢复必须跟随新发现的地址：%v", f.attempts)
	}
}

func TestNetworkRecoverySkipsUnsafeCandidates(t *testing.T) {
	for _, mode := range []string{"removed", "removing", "backup", "check", "active-command", "lookup-failed", "unreachable", "cancelled", "usbmuxd2", "expired", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryFixture()
			now := time.Now()
			f.registered["PHONE"] = "192.0.2.11"
			f.app.recoverNetworkDevices(context.Background(), now)
			delete(f.registered, "PHONE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "removed":
				f.app.configs["PHONE"].RemovedAt = &now
			case "removing":
				f.app.deviceRemovalPending["PHONE"] = true
			case "backup":
				f.app.backupInProgress["PHONE"] = true
			case "check":
				f.app.checkInProgress["PHONE"] = true
			case "active-command":
				f.app.activeDeviceCommands["PHONE"] = &activeDeviceCommand{}
			case "lookup-failed":
				f.lookupErr = errors.New("netmuxd unavailable")
			case "unreachable":
				f.reachable = false
			case "cancelled":
				cancel()
			case "usbmuxd2":
				f.app.runtimeConfig.WiFiBackend = wifiBackendUSBMuxd2
			case "expired":
				now = now.Add(25 * time.Hour)
			case "unknown":
				delete(f.app.configs, "PHONE")
			}
			f.app.recoverNetworkDevices(ctx, now.Add(time.Minute))
			if len(f.attempts) != 0 {
				t.Fatalf("不应自动连接 %s：%v", mode, f.attempts)
			}
		})
	}
}

func TestNetworkRecoveryLoopStopsDuringLookup(t *testing.T) {
	app := newApplication()
	started := make(chan struct{})
	app.networkIPLookup = func(ctx context.Context) (map[string]string, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { app.networkRecoveryLoop(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("后台恢复没有启动")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("关闭应用后恢复循环没有退出")
	}
}

func TestNetworkRecoveryClearingManualIPDiscardsOldAddress(t *testing.T) {
	f := newRecoveryFixture()
	now := time.Now()
	f.app.configs["PHONE"].NetworkAddress = "192.0.2.11"
	f.registered["PHONE"] = "192.0.2.11"
	f.app.recoverNetworkDevices(context.Background(), now)
	f.app.configs["PHONE"].NetworkAddress = ""
	delete(f.registered, "PHONE")
	f.app.recoverNetworkDevices(context.Background(), now.Add(time.Minute))
	if len(f.attempts) != 0 {
		t.Fatalf("清空手动地址后仍重放旧地址：%v", f.attempts)
	}
	f.registered["PHONE"] = "192.0.2.22"
	f.app.recoverNetworkDevices(context.Background(), now.Add(2*time.Minute))
	delete(f.registered, "PHONE")
	f.app.recoverNetworkDevices(context.Background(), now.Add(3*time.Minute))
	if f.registered["PHONE"] != "192.0.2.22" {
		t.Fatal("清空 IP 后未恢复 mDNS 地址学习")
	}
}
