package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 可注入 runner 的 mock ----

// capturedCmd 记录一次 exec 调用的参数
type capturedCmd struct {
	name string
	args []string
	env  []string
}

// mockRunner 记录调用并返回预设输出
type mockRunner struct {
	mu       sync.Mutex
	calls    []capturedCmd
	outputFn func(name string, args []string, env []string) ([]byte, error)
}

func (m *mockRunner) run(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, capturedCmd{name: name, args: args, env: env})
	fn := m.outputFn
	m.mu.Unlock()
	if fn != nil {
		return fn(name, args, env)
	}
	return nil, nil
}

func (m *mockRunner) lastCall() (capturedCmd, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return capturedCmd{}, false
	}
	return m.calls[len(m.calls)-1], true
}

func (m *mockRunner) allCalls() []capturedCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]capturedCmd, len(m.calls))
	copy(cp, m.calls)
	return cp
}

func (m *mockRunner) reset() {
	m.mu.Lock()
	m.calls = nil
	m.mu.Unlock()
}

// envHas 检查 env 切片中是否含有给定键
func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// envGet 从 env 切片中取得键对应的值
func envGet(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}

// argsHas 检查 args 中是否包含某个值
func argsHas(args []string, val string) bool {
	for _, a := range args {
		if a == val {
			return true
		}
	}
	return false
}

// argsHasPair 检查 args 中是否有 key val 相邻对
func argsHasPair(args []string, key, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}

// ---- 设备辅助 ----

func usbDevice(udid string) *device {
	return &device{
		UDID:       udid,
		Connection: connectionTypeDesc(connectTypeUSB),
	}
}

func networkDevice(udid string) *device {
	return &device{
		UDID:       udid,
		Connection: connectionTypeDesc(connectTypeNetwork),
	}
}

// ---- TestNetmuxdConstants ----

func TestNetmuxdConstants(t *testing.T) {
	if cmdNetmuxd != "/usr/local/bin/netmuxd" {
		t.Errorf("cmdNetmuxd 应为 /usr/local/bin/netmuxd，得到: %q", cmdNetmuxd)
	}
	if netmuxdAddr != "127.0.0.1:27015" {
		t.Errorf("netmuxdAddr 应为 127.0.0.1:27015，得到: %q", netmuxdAddr)
	}
	if cmdAddDevice != "/usr/local/bin/add_device" {
		t.Errorf("cmdAddDevice 应为 /usr/local/bin/add_device，得到: %q", cmdAddDevice)
	}
}

// ---- TestMuxSocketFor ----

func TestMuxSocketFor(t *testing.T) {
	t.Run("USB设备返回空地址", func(t *testing.T) {
		dev := usbDevice("USB-UDID-001")
		addr := muxSocketFor(dev)
		if addr != "" {
			t.Errorf("USB 设备不应注入 socket 地址，得到: %q", addr)
		}
	})

	t.Run("网络设备返回netmuxdAddr", func(t *testing.T) {
		dev := networkDevice("NET-UDID-001")
		addr := muxSocketFor(dev)
		if addr != netmuxdAddr {
			t.Errorf("网络设备应返回 %q，得到: %q", netmuxdAddr, addr)
		}
	})
}

// ---- TestRunIdeviceCmdSocketInjection ----

func TestRunIdeviceCmdSocketInjection(t *testing.T) {
	t.Run("USB设备不注入USBMUXD_SOCKET_ADDRESS", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := usbDevice("USB-001")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceID, "-l")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
			t.Error("USB 设备不应注入 USBMUXD_SOCKET_ADDRESS")
		}
	})

	t.Run("网络设备注入USBMUXD_SOCKET_ADDRESS", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := networkDevice("NET-001")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceID, "-l", "-n")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		val := envGet(call.env, "USBMUXD_SOCKET_ADDRESS")
		if val != netmuxdAddr {
			t.Errorf("网络设备应注入 USBMUXD_SOCKET_ADDRESS=%s，得到: %q", netmuxdAddr, val)
		}
	})

	t.Run("网络设备参数包含-n", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := networkDevice("NET-002")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdevicePair, "validate")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if !argsHas(call.args, "-n") {
			t.Errorf("网络设备命令应包含 -n，实际 args: %v", call.args)
		}
	})

	t.Run("USB设备参数不含-n", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := usbDevice("USB-003")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdevicePair, "validate")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if argsHas(call.args, "-n") {
			t.Errorf("USB 设备命令不应包含 -n，实际 args: %v", call.args)
		}
	})

	t.Run("网络设备同时有-n和socket环境变量", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := networkDevice("NET-003")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if !argsHas(call.args, "-n") {
			t.Errorf("网络设备应有 -n 参数，args: %v", call.args)
		}
		if !envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
			t.Errorf("网络设备应注入 USBMUXD_SOCKET_ADDRESS，env: %v", call.env)
		}
	})
}

func TestUsbmuxd2WiFiBackendNetworkListUsesDefaultSocket(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_WIFI_BACKEND": "usbmuxd2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app := newApplicationWithRuntime(context.Background(), cfg)
	runner := &mockRunner{outputFn: func(name string, args []string, env []string) ([]byte, error) {
		return nil, nil
	}}
	app.cmdRunner = runner.run

	app.listDevicesFromBothSockets()

	foundNetworkQuery := false
	for _, call := range runner.allCalls() {
		if !strings.HasSuffix(call.name, "idevice_id") || !argsHas(call.args, "-n") {
			continue
		}
		foundNetworkQuery = true
		if envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
			t.Fatalf("usbmuxd2 Wi-Fi 网络列表必须走默认 socket，env=%v", call.env)
		}
	}
	if !foundNetworkQuery {
		t.Fatal("缺少 idevice_id -l -n 网络列表查询")
	}
}

func TestUsbmuxd2WiFiBackendDoesNotLaunchNetmuxd(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_WIFI_BACKEND": "usbmuxd2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app := newApplicationWithRuntime(context.Background(), cfg)
	launched := make(chan string, 1)
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		launched <- name
		return &fakeMuxProc{ctx: ctx, released: make(chan struct{})}
	}
	defer app.StopNetmuxd()

	if err := app.StartNetmuxd(); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-launched:
		t.Fatalf("usbmuxd2 Wi-Fi 模式不应启动 netmuxd，实际启动 %s", name)
	case <-time.After(20 * time.Millisecond):
	}
}

// ---- TestRunIdeviceCmdOpenSSLConf ----

func TestRunIdeviceCmdOpenSSLConf(t *testing.T) {
	t.Run("网络设备注入OPENSSL_CONF", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := networkDevice("NET-SSL")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if got := envGet(call.env, "OPENSSL_CONF"); got != opensslConfPath {
			t.Errorf("网络设备应注入 OPENSSL_CONF=%s，得到: %q", opensslConfPath, got)
		}
	})

	t.Run("USB设备不注入OPENSSL_CONF", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := usbDevice("USB-SSL")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if envHas(call.env, "OPENSSL_CONF") {
			t.Error("USB 设备不应注入 OPENSSL_CONF（保持 USB 路径现状）")
		}
	})
}

// ---- TestRunIdeviceCmdEnvPasswordChannel ----

func TestRunIdeviceCmdEnvPasswordChannel(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run

	dev := usbDevice("PW-DEV")
	_, _ = app.runIdeviceCmdEnv(context.Background(), cmdKindShort, dev,
		[]string{"BACKUP_PASSWORD=s3cr3t"}, cmdIdevicebackup2, "encryption", "on")

	call, ok := runner.lastCall()
	if !ok {
		t.Fatal("无调用记录")
	}
	if envGet(call.env, "BACKUP_PASSWORD") != "s3cr3t" {
		t.Errorf("BACKUP_PASSWORD 应注入 env，得到 %q", envGet(call.env, "BACKUP_PASSWORD"))
	}
	// 关键：密码绝不出现在 argv
	for _, a := range call.args {
		if strings.Contains(a, "s3cr3t") {
			t.Errorf("密码绝不应出现在 argv，实际 args: %v", call.args)
		}
	}
	if !argsHas(call.args, "encryption") || !argsHas(call.args, "on") {
		t.Errorf("args 应含 encryption on，得到 %v", call.args)
	}
}

func TestRunIdeviceCmdStillWorksUnchanged(t *testing.T) {
	// 确保旧入口 runIdeviceCmd 行为不变（无 extraEnv）
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	dev := networkDevice("NET-X")
	_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")
	call, _ := runner.lastCall()
	if !argsHas(call.args, "-n") || !envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
		t.Errorf("runIdeviceCmd 网络注入应不变，args=%v", call.args)
	}
}

// ---- TestRunIdeviceCmdUDIDInjection ----

func TestRunIdeviceCmdUDIDInjection(t *testing.T) {
	t.Run("USB设备有-u UDID参数对", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := usbDevice("MY-USB-UDID")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if !argsHasPair(call.args, "-u", "MY-USB-UDID") {
			t.Errorf("USB 设备应有 -u MY-USB-UDID，args: %v", call.args)
		}
	})

	t.Run("网络设备有-n和-u UDID", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		dev := networkDevice("MY-NET-UDID")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceInfo, "-k", "DeviceName")

		call, ok := runner.lastCall()
		if !ok {
			t.Fatal("无调用记录")
		}
		if !argsHas(call.args, "-n") {
			t.Errorf("网络设备应有 -n，args: %v", call.args)
		}
		if !argsHasPair(call.args, "-u", "MY-NET-UDID") {
			t.Errorf("网络设备应有 -u MY-NET-UDID，args: %v", call.args)
		}
	})
}

// ---- TestRunIdeviceCmdTimeout ----

func TestRunIdeviceCmdTimeout(t *testing.T) {
	t.Run("短命令context设置deadline", func(t *testing.T) {
		app := newApplication()
		var deadlineSet bool
		app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			_, deadlineSet = ctx.Deadline()
			return nil, nil
		}
		dev := usbDevice("USB-TIMEOUT-001")
		_, _ = app.runIdeviceCmd(context.Background(), cmdKindShort, dev, cmdIdeviceID, "-l")
		if !deadlineSet {
			t.Error("短命令应设置 context deadline")
		}
	})

	t.Run("长命令context不设固定deadline", func(t *testing.T) {
		app := newApplication()
		var deadlineSet bool
		app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			_, deadlineSet = ctx.Deadline()
			return nil, nil
		}
		dev := usbDevice("USB-LONG-001")
		parentCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _ = app.runIdeviceCmd(parentCtx, cmdKindLong, dev, cmdIdevicebackup2, "backup", "/tmp")
		if deadlineSet {
			t.Error("长命令不应设置固定 deadline，应可取消但无超时")
		}
	})
}

// ---- TestAddDeviceReplay ----

func TestAddDeviceReplay(t *testing.T) {
	t.Run("NetworkAddress为空不调用add_device", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		app.mu.Lock()
		app.configs["NO-IP-UDID"] = &backupConfig{
			UDID:           "NO-IP-UDID",
			NetworkAddress: "",
		}
		app.mu.Unlock()

		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			return []byte(""), nil
		}

		app.replayAddDevice()

		for _, call := range runner.allCalls() {
			if strings.HasSuffix(call.name, "add_device") {
				t.Errorf("NetworkAddress 为空不应调用 add_device，实际: %v", call)
			}
		}
	})

	t.Run("已在netmuxd列表中的设备不重复add_device", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		udid := "EXISTING-NET-UDID"
		app.mu.Lock()
		app.configs[udid] = &backupConfig{
			UDID:           udid,
			NetworkAddress: "192.168.1.100",
		}
		app.mu.Unlock()

		// idevice_id -l -n 返回该设备（已在 netmuxd 列表）
		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			if strings.HasSuffix(name, "idevice_id") {
				return []byte(udid + " (Network)\n"), nil
			}
			return []byte(""), nil
		}

		app.replayAddDevice()

		for _, call := range runner.allCalls() {
			if strings.HasSuffix(call.name, "add_device") {
				t.Errorf("设备已在 netmuxd 列表，不应调用 add_device，实际: %v", call)
			}
		}
	})

	t.Run("不在netmuxd列表中调用add_device", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		app.reachProbe = func(string) (bool, string) { return true, "" } // 探针注入：视为可达，专测 add_device 调用

		udid := "NEW-NET-UDID"
		ip := "10.0.0.50"
		app.mu.Lock()
		app.configs[udid] = &backupConfig{
			UDID:           udid,
			NetworkAddress: ip,
		}
		app.mu.Unlock()

		// idevice_id -l -n 返回空（不在 netmuxd 列表）
		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			return []byte(""), nil
		}

		app.replayAddDevice()

		found := false
		for _, call := range runner.allCalls() {
			if strings.HasSuffix(call.name, "add_device") {
				argsStr := strings.Join(call.args, " ")
				if strings.Contains(argsStr, udid) && strings.Contains(argsStr, ip) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("应调用 add_device 带 udid=%s ip=%s，实际: %v", udid, ip, runner.allCalls())
		}
	})

	t.Run("add_device失败时不panic", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		app.reachProbe = func(string) (bool, string) { return true, "" } // 探针注入：可达，专测 add_device 返回 Failure 时不 panic

		udid := "DEAD-IP-UDID"
		app.mu.Lock()
		app.configs[udid] = &backupConfig{
			UDID:           udid,
			NetworkAddress: "192.168.99.254",
		}
		app.mu.Unlock()

		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			if strings.HasSuffix(name, "add_device") {
				return []byte("Failure\n"), nil
			}
			return []byte(""), nil
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("replayAddDevice 不应 panic: %v", r)
			}
		}()
		app.replayAddDevice()
	})
}

// 真实场景：idevice_id -l（USB 默认 socket，不加 -n）输出的是【裸 UDID，无连接后缀】，
// 而 idevice_id -l -n（netmuxd）输出带 " (Network)"。裸 UDID 必须被识别为 USB，否则
// 同时 USB+Wi-Fi 在线的设备永远显示 Wi-Fi（USB 优先失效）。
func TestParseDeviceListBareUDIDIsUSB(t *testing.T) {
	t.Run("裸UDID直接解析为USB", func(t *testing.T) {
		result := parseDeviceList("TEST-DEVICE-ID\n")
		dev, ok := result["TEST-DEVICE-ID"]
		if !ok {
			t.Fatalf("裸 UDID 应被解析为设备，实际: %+v", result)
		}
		if dev.Connection != connectionTypeDesc(connectTypeUSB) {
			t.Errorf("裸 UDID 应识别为 USB，得到 %q", dev.Connection)
		}
	})

	t.Run("裸UDID(USB)与网络同UDID时USB优先", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		udid := "TEST-DEVICE-ID"
		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			if !strings.HasSuffix(name, "idevice_id") {
				return []byte(""), nil
			}
			if envHas(env, "USBMUXD_SOCKET_ADDRESS") {
				return []byte(udid + " (Network)\n"), nil // netmuxd：带后缀
			}
			return []byte(udid + "\n"), nil // USB 默认 socket：真实裸 UDID
		}

		usbList, netList := app.listDevicesFromBothSockets()
		merged := mergeDeviceLists(usbList, netList)

		dev, ok := merged[udid]
		if !ok {
			t.Fatalf("合并后应包含 UDID %s", udid)
		}
		if dev.Connection != connectionTypeDesc(connectTypeUSB) {
			t.Errorf("USB 裸 UDID + 网络同 UDID 应 USB 优先，得到: %q", dev.Connection)
		}
	})
}

// ---- TestDeviceMerge: listDevicesFromBothSockets + mergeDeviceLists ----

func TestDeviceMerge(t *testing.T) {
	t.Run("同UDID同时在USB和网络时USB优先", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		udid := "BOTH-UDID"
		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			if !strings.HasSuffix(name, "idevice_id") {
				return []byte(""), nil
			}
			isNetmuxd := envHas(env, "USBMUXD_SOCKET_ADDRESS")
			if isNetmuxd {
				return []byte(udid + " (Network)\n"), nil
			}
			return []byte(udid + " (USB)\n"), nil
		}

		usbList, netList := app.listDevicesFromBothSockets()
		merged := mergeDeviceLists(usbList, netList)

		dev, ok := merged[udid]
		if !ok {
			t.Fatalf("合并后应包含 UDID %s", udid)
		}
		if dev.Connection != connectionTypeDesc(connectTypeUSB) {
			t.Errorf("同 UDID USB+网络时应 USB 优先，得到: %q", dev.Connection)
		}
	})

	t.Run("仅在网络的设备保留网络连接", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		usbUDID := "USB-ONLY"
		netUDID := "NET-ONLY"

		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			if !strings.HasSuffix(name, "idevice_id") {
				return []byte(""), nil
			}
			if envHas(env, "USBMUXD_SOCKET_ADDRESS") {
				return []byte(netUDID + " (Network)\n"), nil
			}
			return []byte(usbUDID + " (USB)\n"), nil
		}

		usbList, netList := app.listDevicesFromBothSockets()
		merged := mergeDeviceLists(usbList, netList)

		if dev, ok := merged[usbUDID]; !ok || dev.Connection != connectionTypeDesc(connectTypeUSB) {
			t.Errorf("USB-only 设备应为 USB 连接，实际: %+v", merged[usbUDID])
		}
		if dev, ok := merged[netUDID]; !ok || dev.Connection != connectionTypeDesc(connectTypeNetwork) {
			t.Errorf("NET-only 设备应为网络连接，实际: %+v", merged[netUDID])
		}
	})

	t.Run("USB列表查询不带-n参数", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			return []byte(""), nil
		}

		app.listDevicesFromBothSockets()

		for _, call := range runner.allCalls() {
			if !strings.HasSuffix(call.name, "idevice_id") {
				continue
			}
			// usbmuxd2 的调用（无 USBMUXD_SOCKET_ADDRESS）
			if envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
				continue
			}
			if argsHas(call.args, "-n") {
				t.Errorf("USB 列表查询不应带 -n，args: %v", call.args)
			}
		}
	})

	t.Run("网络列表查询使用netmuxd socket和-n", func(t *testing.T) {
		app := newApplication()
		runner := &mockRunner{}
		app.cmdRunner = runner.run

		runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
			return []byte(""), nil
		}

		app.listDevicesFromBothSockets()

		foundNetmuxdQuery := false
		for _, call := range runner.allCalls() {
			if !strings.HasSuffix(call.name, "idevice_id") {
				continue
			}
			if !envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
				continue
			}
			if argsHas(call.args, "-n") {
				foundNetmuxdQuery = true
				break
			}
		}
		if !foundNetmuxdQuery {
			t.Error("应有带 USBMUXD_SOCKET_ADDRESS 且带 -n 的 idevice_id 调用")
		}
	})
}

// ---- TestWatchdogBackoff ----

func TestWatchdogBackoff(t *testing.T) {
	t.Run("指数退避序列正确1s→2s→4s→8s→16s", func(t *testing.T) {
		expected := []time.Duration{
			1 * time.Second, 2 * time.Second, 4 * time.Second,
			8 * time.Second, 16 * time.Second,
		}
		delay := watchdogInitDelay
		for i, want := range expected {
			if delay != want {
				t.Errorf("第 %d 次退避应为 %v，得到 %v", i, want, delay)
			}
			delay = nextWatchdogDelay(delay)
		}
	})

	t.Run("退避不超过最大值", func(t *testing.T) {
		delay := watchdogInitDelay
		for i := 0; i < 100; i++ {
			delay = nextWatchdogDelay(delay)
		}
		if delay != watchdogMaxDelay {
			t.Errorf("退避应封顶于 %v，得到 %v", watchdogMaxDelay, delay)
		}
	})
}

// ---- TestSaveConfigMerge ----

// handleSaveConfigMerge 是 handleSaveConfig 的可测试提取（测试用辅助函数签名）
// 实际实现在 routes.go 中通过 handleSaveConfig 调用

func newConfigPersistenceTestApp(t *testing.T) *application {
	t.Helper()
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot = t.TempDir()
	cfg.BackupsRoot = t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.reachProbe = func(string) (bool, string) { return false, "测试不拨号" }
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		return &fakeMuxProc{ctx: ctx}
	}
	return app
}

func TestSaveConfigMerge(t *testing.T) {
	t.Run("旧前端不带NetworkAddress时保留已存值", func(t *testing.T) {
		app := newConfigPersistenceTestApp(t)
		app.mu.Lock()
		app.configs["MERGE-UDID"] = &backupConfig{
			UDID:           "MERGE-UDID",
			Name:           "我的设备",
			NetworkAddress: "10.0.0.1",
			BackupInterval: 24,
		}
		app.mu.Unlock()

		// 旧前端不含 network_address 字段
		body := `{"start_time":"18:00","end_time":"06:00","backup_interval":12,"min_battery_level":20}`
		req := httptest.NewRequest(http.MethodPost, "/api/save-config/MERGE-UDID", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		app.handleSaveConfig(rr, req)

		app.mu.RLock()
		cfg := app.configs["MERGE-UDID"]
		app.mu.RUnlock()

		if cfg == nil {
			t.Fatal("配置不应为 nil")
		}
		if cfg.NetworkAddress != "10.0.0.1" {
			t.Errorf("NetworkAddress 应保留 10.0.0.1，得到: %q", cfg.NetworkAddress)
		}
		if cfg.BackupInterval != 12 {
			t.Errorf("BackupInterval 应更新为 12，得到: %d", cfg.BackupInterval)
		}
	})

	t.Run("显式传入空NetworkAddress时清空", func(t *testing.T) {
		app := newConfigPersistenceTestApp(t)
		app.mu.Lock()
		app.configs["CLEAR-UDID"] = &backupConfig{
			UDID:           "CLEAR-UDID",
			NetworkAddress: "10.0.0.2",
		}
		app.mu.Unlock()

		// 前端显式传空字符串
		body := `{"network_address":"","backup_interval":6}`
		req := httptest.NewRequest(http.MethodPost, "/api/save-config/CLEAR-UDID", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		app.handleSaveConfig(rr, req)

		app.mu.RLock()
		cfg := app.configs["CLEAR-UDID"]
		app.mu.RUnlock()

		if cfg.NetworkAddress != "" {
			t.Errorf("显式清空时 NetworkAddress 应为空，得到: %q", cfg.NetworkAddress)
		}
	})

	t.Run("更新BackupInterval不影响NetworkAddress", func(t *testing.T) {
		app := newConfigPersistenceTestApp(t)
		app.mu.Lock()
		app.configs["PARTIAL-UDID"] = &backupConfig{
			UDID:              "PARTIAL-UDID",
			Name:              "设备名",
			StartTime:         "09:00",
			EndTime:           "22:00",
			BackupInterval:    48,
			MinBatteryLevel:   30,
			OnlyWhenCharging:  true,
			BackupDirectory:   app.paths.BackupsRoot,
			AutoBackupEnabled: true,
			NetworkAddress:    "172.16.0.1",
		}
		app.mu.Unlock()

		body := `{"backup_interval":24}`
		req := httptest.NewRequest(http.MethodPost, "/api/save-config/PARTIAL-UDID", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		app.handleSaveConfig(rr, req)

		app.mu.RLock()
		cfg := app.configs["PARTIAL-UDID"]
		app.mu.RUnlock()

		if cfg.BackupInterval != 24 {
			t.Errorf("BackupInterval 应为 24，得到: %d", cfg.BackupInterval)
		}
		if cfg.NetworkAddress != "172.16.0.1" {
			t.Errorf("NetworkAddress 应保留，得到: %q", cfg.NetworkAddress)
		}
		if cfg.StartTime != "09:00" {
			t.Errorf("StartTime 应保留，得到: %q", cfg.StartTime)
		}
	})

	t.Run("新设备无旧配置直接创建", func(t *testing.T) {
		app := newConfigPersistenceTestApp(t)

		body := `{"start_time":"10:00","backup_interval":12,"network_address":"192.168.1.1"}`
		req := httptest.NewRequest(http.MethodPost, "/api/save-config/NEW-DEVICE", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		app.handleSaveConfig(rr, req)

		app.mu.RLock()
		cfg := app.configs["NEW-DEVICE"]
		app.mu.RUnlock()

		if cfg == nil {
			t.Fatal("应创建新配置")
		}
		if cfg.NetworkAddress != "192.168.1.1" {
			t.Errorf("应设置 NetworkAddress，得到: %q", cfg.NetworkAddress)
		}
	})
}

// ---- TestLastBackupConnectionField ----

func TestLastBackupConnectionField(t *testing.T) {
	t.Run("BackupConfig包含LastBackupConnection字段", func(t *testing.T) {
		cfg := &backupConfig{
			UDID:                 "TEST-UDID",
			LastBackupConnection: connectTypeUSB,
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if !bytes.Contains(data, []byte("last_backup_connection")) {
			t.Errorf("JSON 应包含 last_backup_connection 字段，实际: %s", data)
		}
	})

	t.Run("旧JSON缺字段时零值安全", func(t *testing.T) {
		oldJSON := `{"udid":"OLD-UDID","name":"设备","backup_interval":24}`
		var cfg backupConfig
		if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
			t.Fatalf("反序列化失败: %v", err)
		}
		if cfg.LastBackupConnection != "" {
			t.Errorf("旧 JSON 缺字段时应为零值，得到: %q", cfg.LastBackupConnection)
		}
	})
}

// ---- TestNetworkAddressFieldZeroValue ----

func TestNetworkAddressFieldZeroValue(t *testing.T) {
	t.Run("旧JSON缺NetworkAddress时零值安全", func(t *testing.T) {
		oldJSON := `{"udid":"OLD","name":"设备","backup_interval":24}`
		var cfg backupConfig
		if err := json.Unmarshal([]byte(oldJSON), &cfg); err != nil {
			t.Fatalf("反序列化失败: %v", err)
		}
		if cfg.NetworkAddress != "" {
			t.Errorf("旧 JSON 缺 NetworkAddress 时应为空，得到: %q", cfg.NetworkAddress)
		}
	})

	t.Run("BackupConfig序列化包含network_address", func(t *testing.T) {
		cfg := backupConfig{UDID: "U", NetworkAddress: "1.2.3.4"}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if !bytes.Contains(data, []byte("network_address")) {
			t.Errorf("JSON 应包含 network_address，实际: %s", data)
		}
	})
}

// ---- 辅助：http.Request 辅助 ----

// newTestRequest 构造简单测试 HTTP 请求（内部测试辅助）
func newTestRequestWithPath(method, path, body string) *http.Request {
	return httptest.NewRequest(method, path, bytes.NewBufferString(body))
}

// ---- supervisor（统一看门狗）测试 ----

// fakeMuxProc 是受控的假进程，供 superviseMux 测试。
//   - exitNow=true：Wait 立即返回（模拟崩溃）
//   - 否则 Wait 阻塞直到 ctx 取消或 released 关闭
type fakeMuxProc struct {
	ctx      context.Context
	exitNow  bool
	released chan struct{}
}

type envCaptureMuxProc struct {
	fakeMuxProc
	env []string
}

func (f *envCaptureMuxProc) SetEnv(env []string) {
	f.env = append([]string(nil), env...)
}

func (f *fakeMuxProc) Start() error { return nil }

func (f *fakeMuxProc) Wait() error {
	if f.exitNow {
		return nil
	}
	select {
	case <-f.ctx.Done():
	case <-f.released:
	}
	return nil
}

func TestLaunchMuxInjectsRustLogOnlyIntoNetmuxd(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.NetmuxdLogLevel = "debug"
	app := newApplicationWithRuntime(context.Background(), cfg)

	var launched []*envCaptureMuxProc
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		proc := &envCaptureMuxProc{fakeMuxProc: fakeMuxProc{ctx: ctx, released: make(chan struct{})}}
		launched = append(launched, proc)
		return proc
	}

	app.launchMux(context.Background(), cmdNetmuxd)
	app.launchMux(context.Background(), cmdUSBMuxd)

	if got := envGet(launched[0].env, "RUST_LOG"); got != "netmuxd=debug" {
		t.Fatalf("netmuxd RUST_LOG=%q", got)
	}
	if envHas(launched[1].env, "RUST_LOG") {
		t.Fatalf("usbmuxd 不应注入 RUST_LOG: %v", launched[1].env)
	}
}

// TestNetmuxdLaunchIncludesDisableUSB 验证 netmuxd 启动参数包含 --disable-usb
func TestNetmuxdLaunchIncludesDisableUSB(t *testing.T) {
	app := newApplication()

	argsCh := make(chan []string, 1)
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		if name == cmdNetmuxd {
			argsCh <- append([]string(nil), args...)
		}
		return &fakeMuxProc{ctx: ctx}
	}

	if err := app.StartNetmuxd(); err != nil {
		t.Fatalf("StartNetmuxd 失败: %v", err)
	}
	defer app.StopNetmuxd()

	var capturedArgs []string
	select {
	case capturedArgs = <-argsCh:
	case <-time.After(time.Second):
		t.Fatal("等待 netmuxd 启动参数超时")
	}

	expectedArgs := []string{"--disable-unix", "--disable-usb", "--host", "127.0.0.1"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("netmuxd args 长度不匹配: got %v, want %v", capturedArgs, expectedArgs)
	}
	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("netmuxd args[%d]: got %q, want %q", i, capturedArgs[i], arg)
		}
	}

	// 验证 --disable-usb 参数存在，确保 USB 职责由 usbmuxd2 独占
	hasDisableUSB := false
	for _, arg := range capturedArgs {
		if arg == "--disable-usb" {
			hasDisableUSB = true
			break
		}
	}
	if !hasDisableUSB {
		t.Error("netmuxd 启动参数缺少 --disable-usb，违反 USB/Wi-Fi 职责分离原则")
	}
}

// waitFor 轮询等待 cond 为真，超时则 fail。
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("条件未在超时内满足")
}

func TestSuperviseMuxRestartsOnUnexpectedExit(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var launches int32
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		atomic.AddInt32(&launches, 1)
		// 首次崩溃，之后阻塞（稳定运行）
		return &fakeMuxProc{ctx: ctx, exitNow: atomic.LoadInt32(&launches) == 1}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.superviseMux(ctx, "test", "/bin/x", nil, nil)

	waitFor(t, func() bool { return atomic.LoadInt32(&launches) >= 2 }, time.Second)
}

func TestSuperviseMuxStopAfterRestartDoesNotResurrect(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var launches int32
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		n := atomic.AddInt32(&launches, 1)
		// 前两次崩溃触发重启，第三次起稳定阻塞
		return &fakeMuxProc{ctx: ctx, exitNow: n <= 2}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.superviseMux(ctx, "test", "/bin/x", nil, nil)
		close(done)
	}()

	// 等待至少 3 次启动（两次崩溃重启 + 一次稳定）
	waitFor(t, func() bool { return atomic.LoadInt32(&launches) >= 3 }, time.Second)

	// 停止：cancel 后 supervisor 必须退出，且不再启动新进程（旧 bug：重启后泄漏/复活）
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor 未在 cancel 后退出（看门狗泄漏/复活 bug）")
	}

	before := atomic.LoadInt32(&launches)
	time.Sleep(30 * time.Millisecond)
	if after := atomic.LoadInt32(&launches); after != before {
		t.Errorf("cancel 后不应再启动进程（复活 bug），launches 从 %d 增到 %d", before, after)
	}
}

func TestSuperviseMuxOnRestartCallback(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var launches int32
	var restarts int32
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		n := atomic.AddInt32(&launches, 1)
		return &fakeMuxProc{ctx: ctx, exitNow: n == 1} // 首次崩溃，第二次稳定
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onRestart := func() { atomic.AddInt32(&restarts, 1) }
	go app.superviseMux(ctx, "test", "/bin/x", nil, onRestart)

	waitFor(t, func() bool { return atomic.LoadInt32(&restarts) >= 1 }, time.Second)

	// onRestart 只应在重启时触发，不应为初始启动触发
	time.Sleep(30 * time.Millisecond)
	if r := atomic.LoadInt32(&restarts); r != 1 {
		t.Errorf("onRestart 应恰好触发 1 次（仅重启），得到 %d", r)
	}
}

func TestStartStopNetmuxd(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var launches int32
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		atomic.AddInt32(&launches, 1)
		return &fakeMuxProc{ctx: ctx} // 稳定阻塞
	}

	if err := app.StartNetmuxd(); err != nil {
		t.Fatalf("StartNetmuxd 失败: %v", err)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&launches) >= 1 }, time.Second)

	// StopNetmuxd 应迅速返回（不持锁阻塞）
	stopped := make(chan struct{})
	go func() {
		app.StopNetmuxd()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopNetmuxd 未及时返回")
	}

	// 停止后不应继续重启
	before := atomic.LoadInt32(&launches)
	time.Sleep(30 * time.Millisecond)
	if after := atomic.LoadInt32(&launches); after != before {
		t.Errorf("StopNetmuxd 后不应再启动 netmuxd，launches 从 %d 增到 %d", before, after)
	}
}

func TestStartStopUSBMuxD(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var launches int32
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		atomic.AddInt32(&launches, 1)
		return &fakeMuxProc{ctx: ctx} // 稳定阻塞
	}

	if err := app.StartUSBMuxD(); err != nil {
		t.Fatalf("StartUSBMuxD 失败: %v", err)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&launches) >= 1 }, time.Second)

	stopped := make(chan struct{})
	go func() {
		app.StopUSBMuxD()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopUSBMuxD 未及时返回（疑似持锁阻塞）")
	}

	before := atomic.LoadInt32(&launches)
	time.Sleep(30 * time.Millisecond)
	if after := atomic.LoadInt32(&launches); after != before {
		t.Errorf("StopUSBMuxD 后不应再启动 usbmuxd，launches 从 %d 增到 %d", before, after)
	}
}

func TestHandleRestartRestartsBothDaemons(t *testing.T) {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond

	var lmu sync.Mutex
	launched := map[string]bool{}
	app.muxProcFactory = func(ctx context.Context, name string, args ...string) muxProcess {
		lmu.Lock()
		launched[name] = true
		lmu.Unlock()
		return &fakeMuxProc{ctx: ctx}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/restart-usbmuxd", nil)
	rr := httptest.NewRecorder()
	app.handleRestartUSBMuxD(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d", rr.Code)
	}

	// 等 usbmuxd2 与 netmuxd 两个守护都被（重新）拉起
	waitFor(t, func() bool {
		lmu.Lock()
		defer lmu.Unlock()
		return launched[cmdUSBMuxd] && launched[cmdNetmuxd]
	}, 3*time.Second)

	// 收尾：停掉两个 supervisor，避免遗留 goroutine
	app.StopUSBMuxD()
	app.StopNetmuxd()
}

// 确保 handleSaveConfig 的 URL 路由正确提取 UDID（集成测试辅助）
func TestHandleSaveConfigURLParsing(t *testing.T) {
	app := newConfigPersistenceTestApp(t)

	body := `{"backup_interval":8}`
	req := httptest.NewRequest(http.MethodPost, "/api/save-config/URL-UDID-TEST", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()

	app.handleSaveConfig(rr, req)

	app.mu.RLock()
	_, exists := app.configs["URL-UDID-TEST"]
	app.mu.RUnlock()

	if !exists {
		t.Error("handleSaveConfig 应从 URL 路径提取 UDID 并创建配置")
	}
}

func TestHandleSaveConfigRejectsUnknownField(t *testing.T) {
	app := newConfigPersistenceTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/api/save-config/STRICT-UDID", bytes.NewBufferString(`{"backup_interval":8,"surprise":true}`))
	rr := httptest.NewRecorder()
	app.handleSaveConfig(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("未知字段应返回 400，得到 %d", rr.Code)
	}
}

func TestHandleSaveConfigDoesNotPublishOnDiskFailure(t *testing.T) {
	app := newConfigPersistenceTestApp(t)
	old := app.defaultBackupConfig("DISK-FAIL", "旧配置")
	app.mu.Lock()
	app.configs[old.UDID] = old
	app.mu.Unlock()

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	app.configStore = newBackupConfigStore(filepath.Join(blocker, "backup_configs.json"), []string{app.paths.BackupsRoot})
	req := httptest.NewRequest(http.MethodPost, "/api/save-config/DISK-FAIL", bytes.NewBufferString(`{"backup_interval":8}`))
	rr := httptest.NewRecorder()
	app.handleSaveConfig(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("落盘失败应返回 500，得到 %d", rr.Code)
	}
	app.mu.RLock()
	got := app.configs[old.UDID].BackupInterval
	app.mu.RUnlock()
	if got != old.BackupInterval {
		t.Fatalf("落盘失败不得发布内存状态，原值 %d，得到 %d", old.BackupInterval, got)
	}
}
