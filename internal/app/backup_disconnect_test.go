package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNetworkBackupCancelledAfterContinuousOfflineGrace(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.DeviceDisconnectGrace = 30 * time.Second
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices["U-WIFI"] = &device{
		UDID:       "U-WIFI",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeNetwork),
	}

	cmdCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	release := app.registerActiveDeviceCommand("U-WIFI", connectTypeNetwork, cancel)
	defer release()

	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	app.applyPresenceSnapshot(map[string]*device{}, t0)
	select {
	case <-cmdCtx.Done():
		t.Fatal("一次缺失不能立刻取消 Wi-Fi 备份")
	default:
	}

	app.applyPresenceSnapshot(map[string]*device{}, t0.Add(29*time.Second))
	select {
	case <-cmdCtx.Done():
		t.Fatal("宽限期内不能取消 Wi-Fi 备份")
	default:
	}

	app.applyPresenceSnapshot(map[string]*device{}, t0.Add(30*time.Second))
	select {
	case <-cmdCtx.Done():
		if !errors.Is(context.Cause(cmdCtx), errDeviceDisconnected) {
			t.Fatalf("取消原因错误: %v", context.Cause(cmdCtx))
		}
	case <-time.After(time.Second):
		t.Fatal("连续离线达到宽限后应取消 Wi-Fi 备份")
	}
}

func TestNetworkBackupOfflineGraceResetsWhenDeviceReturns(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.DeviceDisconnectGrace = 30 * time.Second
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices["U-WIFI"] = &device{
		UDID:       "U-WIFI",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeNetwork),
	}

	cmdCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	release := app.registerActiveDeviceCommand("U-WIFI", connectTypeNetwork, cancel)
	defer release()

	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	app.applyPresenceSnapshot(map[string]*device{}, t0)
	app.applyPresenceSnapshot(map[string]*device{
		"U-WIFI": {UDID: "U-WIFI", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)},
	}, t0.Add(20*time.Second))
	app.applyPresenceSnapshot(map[string]*device{}, t0.Add(40*time.Second))
	app.applyPresenceSnapshot(map[string]*device{}, t0.Add(69*time.Second))

	select {
	case <-cmdCtx.Done():
		t.Fatal("设备恢复后必须重新计算连续离线宽限")
	default:
	}
}

func TestNetworkBackupStaysHealthyWhenUSBAlsoAppears(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.DeviceDisconnectGrace = 30 * time.Second
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.devices["U-WIFI"] = &device{
		UDID:       "U-WIFI",
		IsOnline:   true,
		Connection: connectionTypeDesc(connectTypeNetwork),
	}

	cmdCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	release := app.registerActiveDeviceCommand("U-WIFI", connectTypeNetwork, cancel)
	defer release()

	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	merged := map[string]*device{
		"U-WIFI": {UDID: "U-WIFI", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)},
	}
	network := map[string]*device{
		"U-WIFI": {UDID: "U-WIFI", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)},
	}
	app.applyPresenceSnapshotWithNetwork(merged, network, t0)
	app.applyPresenceSnapshotWithNetwork(merged, network, t0.Add(time.Minute))

	select {
	case <-cmdCtx.Done():
		t.Fatal("同一设备仍在 netmuxd 在线时，插入 USB 不得取消既有 Wi-Fi 任务")
	default:
	}
}
