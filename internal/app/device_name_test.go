package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func deviceNameRunner(name string, lookupErr error) cmdRunner {
	return func(_ context.Context, bin string, args, _ []string) ([]byte, error) {
		if bin == cmdIdeviceInfo {
			switch {
			case argsHasPair(args, "-k", "DeviceName"):
				return []byte(name), lookupErr
			case argsHasPair(args, "-k", "BatteryCurrentCapacity"):
				return []byte("90"), nil
			case argsHasPair(args, "-k", "ExternalConnected"):
				return []byte("true"), nil
			}
		}
		return nil, nil
	}
}

func TestDeviceNameRediscoveryPreservesStoredName(t *testing.T) {
	const udid = "NAME-PHONE"
	for _, tc := range []struct {
		name, reported, want string
		lookupErr            error
	}{
		{"failed lookup", "", "家庭 iPhone", errors.New("metadata unavailable")},
		{"empty lookup", " \n", "家庭 iPhone", nil},
		{"renamed device", "旅行 iPhone", "旅行 iPhone", nil},
		{"name matches old placeholder", "Unknown Device", "Unknown Device", nil},
		{"name matches identifier", udid, udid, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, cfg := newDeviceRemovalTestApp(t, udid)
			delete(app.devices, udid)
			app.applyPresenceSnapshot(map[string]*device{udid: {UDID: udid, Connection: connectTypeUSB}}, nowBeijing())
			if got := app.devices[udid].Name; got != cfg.Name {
				t.Errorf("rediscovery should display stored name before lookup: got %q, want %q", got, cfg.Name)
			}
			app.cmdRunner = deviceNameRunner(tc.reported, tc.lookupErr)
			app.pairDeviceAndUpdateInfoInTask(*app.devices[udid], app.deviceRefreshGeneration)
			if got := app.devices[udid].Name; got != tc.want {
				t.Errorf("display name = %q, want %q", got, tc.want)
			}
			stored, err := app.configStore.Load()
			if err != nil {
				t.Fatal(err)
			}
			if stored[udid].Name != tc.want {
				t.Fatalf("persisted name = %q, want %q", stored[udid].Name, tc.want)
			}
		})
	}
}

func TestDeviceNameFallbackIsNotPersisted(t *testing.T) {
	const udid = "NEW-NAME-PHONE"
	app := newConfigPersistenceTestApp(t)
	app.cmdRunner = deviceNameRunner("", errors.New("metadata unavailable"))
	app.applyPresenceSnapshot(map[string]*device{udid: {UDID: udid, Connection: connectTypeUSB}}, nowBeijing())
	if app.devices[udid].Name != udid {
		t.Fatal("unknown devices must still have an identifier for display")
	}
	if app.configs[udid].Name != "" {
		t.Error("discovery must not treat the display identifier as a known name")
	}
	app.pairDeviceAndUpdateInfoInTask(*app.devices[udid], app.deviceRefreshGeneration)
	stored, err := app.configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored[udid].Name != "" || app.devices[udid].Name != udid {
		t.Fatalf("fallback leaked into persistence: stored=%q display=%q", stored[udid].Name, app.devices[udid].Name)
	}

	// 即使真实名称与当前显示的 UDID 相同，也必须记录此次成功查询。
	app.cmdRunner = deviceNameRunner(udid, nil)
	app.pairDeviceAndUpdateInfoInTask(*app.devices[udid], app.deviceRefreshGeneration)
	stored, err = app.configStore.Load()
	if err != nil || stored[udid].Name != udid {
		t.Fatalf("real identifier-shaped name must be persisted: %v, %q", err, stored[udid].Name)
	}
}

func TestDeviceNameFailedLookupDoesNotSaveCachedName(t *testing.T) {
	const udid = "CACHED-NAME-PHONE"
	app, cfg := newDeviceRemovalTestApp(t, udid)
	app.devices[udid].Name = "过期缓存名称"
	app.cmdRunner = deviceNameRunner("", errors.New("metadata unavailable"))
	app.pairDeviceAndUpdateInfoInTask(*app.devices[udid], app.deviceRefreshGeneration)
	stored, err := app.configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored[udid].Name != cfg.Name {
		t.Fatalf("failed lookup replaced persisted name with cached value: got %q, want %q", stored[udid].Name, cfg.Name)
	}
}

func TestDeviceNameBackupCompletionPersistsOnlyKnownNames(t *testing.T) {
	const udid = "BACKUP-NAME-PHONE"
	for _, automatic := range []bool{false, true} {
		mode := "manual"
		if automatic {
			mode = "automatic"
		}
		for _, update := range []string{"fallback", "details", "scheduler"} {
			t.Run(mode+"/"+update, func(t *testing.T) {
				app, cfg := newDeviceRemovalTestApp(t, udid)
				app.runtimeConfig.WiFiPowerAssertion = false
				app.cmdRunner = deviceNameRunner("", errors.New("metadata unavailable"))
				// 模拟尚未拿到已保存配置的明细副本，随后恢复原配置。
				saved := app.configs[udid]
				delete(app.configs, udid)
				d := &device{UDID: udid, Connection: connectTypeUSB, IsOnline: true}
				app.getDeviceInfoInTask(d)
				saved.LastBackup = time.Time{}
				app.configs[udid] = saved
				app.devices[udid] = d
				if err := app.saveConfigs(); err != nil {
					t.Fatal(err)
				}
				want := cfg.Name
				switch update {
				case "details":
					want = udid
					app.cmdRunner = deviceNameRunner(want, nil)
					app.refreshDetails()
				case "scheduler":
					want = "Unknown Device"
					app.cmdRunner = deviceNameRunner(want, nil)
					if app.updateDeviceStatusForBackup(cloneDevice(d), deviceStatus{UDID: udid, Connection: connectTypeUSB, IsOnline: true}) == nil {
						t.Fatal("metadata refresh unexpectedly rejected the device")
					}
				}
				app.backupCommand = func(ctx context.Context, _ string, _ ...string) *execCmd {
					return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
				}
				var err error
				if automatic {
					err = app.PerformBackupWithConnection(udid, connectTypeUSB)
				} else {
					err = app.PerformBackup(udid)
				}
				if err != nil {
					t.Fatal(err)
				}
				stored, err := app.configStore.Load()
				if err != nil {
					t.Fatal(err)
				}
				if stored[udid].LastBackup.IsZero() || stored[udid].Name != want {
					t.Fatalf("backup must update its timestamp without persisting a fallback name: %+v, want name %q", stored[udid], want)
				}
			})
		}
	}
}
