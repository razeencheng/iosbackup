package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHandleTestDeviceIPReachableConnection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		err    error
		listed bool
	}{
		{name: "registered", output: "Success\n", listed: true},
		{name: "registration rejected", output: "Failure\n"},
		{name: "helper failed", err: errors.New("helper unavailable")},
		{name: "unknown helper response", output: "unexpected response\n"},
		{name: "registration not visible", output: "Success\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApplication()
			const udid = "IP-DEVICE"
			app.devices[udid] = &device{UDID: udid, Name: "iPhone"}
			app.configs[udid] = &backupConfig{UDID: udid, MinBatteryLevel: 20, OnlyWhenCharging: true}
			app.reachProbe = func(string) (bool, string) { return true, "" }
			var added atomic.Bool
			runner := &mockRunner{outputFn: func(name string, args, env []string) ([]byte, error) {
				if name == cmdAddDevice {
					added.Store(true)
					if envGet(env, "USBMUXD_SOCKET_ADDRESS") != netmuxdAddr {
						return []byte("Failure\n"), nil
					}
					return []byte(tc.output), tc.err
				}
				if name == cmdIdeviceID && envGet(env, "USBMUXD_SOCKET_ADDRESS") == netmuxdAddr && added.Load() && tc.listed {
					return []byte(udid + " (Network)\n"), nil
				}
				if name == cmdIdeviceInfo && argsHasPair(args, "-q", "com.apple.mobile.battery") {
					if argsHasPair(args, "-k", "BatteryCurrentCapacity") {
						return []byte("80"), nil
					}
					if argsHasPair(args, "-k", "ExternalConnected") {
						return []byte("true"), nil
					}
				}
				return nil, nil
			}}
			app.cmdRunner = runner.run
			req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/"+udid, strings.NewReader(`{"ip":"192.0.2.1"}`)))
			rr := httptest.NewRecorder()
			app.handleTestDeviceIP(rr, req)
			var resp struct {
				OK     bool            `json:"ok"`
				Reason string          `json:"reason"`
				Status *statusSnapshot `json:"status"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.OK != tc.listed {
				t.Fatalf("端口可达不代表设备已连接：listed=%v, response=%s", tc.listed, rr.Body.String())
			}
			if !tc.listed {
				if resp.Reason == "" {
					t.Error("未连接应返回具体原因")
				}
				return
			}
			if resp.Status == nil || len(resp.Status.Devices) != 1 || resp.Status.Devices[0].Conn != "wifi" {
				t.Errorf("响应应携带在线状态，自动刷新关闭时也能恢复备份按钮：%s", rr.Body.String())
			}
			if code, _, _ := app.backupGateReason(udid); code != "" {
				t.Errorf("测试连接成功后备份仍被阻止：%s", code)
			}
			if app.configs[udid].NetworkAddress != "" {
				t.Error("测试连接不应自动保存未提交的 IP 设置")
			}
			snap := app.buildStatusSnapshot()
			if len(snap.Devices) != 1 || snap.Devices[0].Conn != "wifi" {
				t.Errorf("测试连接成功后页面仍将禁用备份按钮：%+v", snap.Devices)
			}
		})
	}
}

func TestCallAddDeviceUsesNetworkSocket(t *testing.T) {
	app := newApplication()
	app.reachProbe = func(string) (bool, string) { return true, "" }
	runner := &mockRunner{outputFn: func(string, []string, []string) ([]byte, error) {
		return []byte("Success\n"), nil
	}}
	app.cmdRunner = runner.run
	app.callAddDevice("IP-DEVICE", "192.0.2.1")
	call, ok := runner.lastCall()
	if !ok || call.name != cmdAddDevice || envGet(call.env, "USBMUXD_SOCKET_ADDRESS") != netmuxdAddr {
		t.Fatalf("手动 IP 必须注册到 netmuxd：command=%q socket=%q", call.name, envGet(call.env, "USBMUXD_SOCKET_ADDRESS"))
	}
}

func TestHandleTestDeviceIPUnsupportedBackend(t *testing.T) {
	app := newApplication()
	app.runtimeConfig.WiFiBackend = wifiBackendUSBMuxd2
	app.reachProbe = func(string) (bool, string) { return true, "" }
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	rr := httptest.NewRecorder()
	app.handleTestDeviceIP(rr, withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/IP-DEVICE", strings.NewReader(`{"ip":"192.0.2.1"}`))))
	var resp struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Reason, "不支持手动 IP") {
		t.Fatalf("不支持手动 IP 的后端不应误报成功：%s", rr.Body.String())
	}
	if len(runner.allCalls()) != 0 {
		t.Fatal("不支持手动 IP 的后端不应执行 add_device 或查询 netmuxd")
	}
}

func TestHandleTestDeviceIPAlreadyRegistered(t *testing.T) {
	for _, usb := range []bool{false, true} {
		t.Run(map[bool]string{false: "wifi", true: "usb preferred"}[usb], func(t *testing.T) {
			app := newApplication()
			const udid = "IP-DEVICE"
			app.devices[udid] = &device{UDID: udid, IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
			app.configs[udid] = &backupConfig{UDID: udid}
			app.backupInProgress[udid] = true
			app.reachProbe = func(string) (bool, string) { return true, "" }
			runner := &mockRunner{outputFn: func(name string, args, env []string) ([]byte, error) {
				if name == cmdIdeviceID {
					if envGet(env, "USBMUXD_SOCKET_ADDRESS") == netmuxdAddr {
						return []byte(udid + " (Network)\n"), nil
					}
					if usb {
						return []byte(udid + "\n"), nil
					}
				}
				return nil, nil
			}}
			app.cmdRunner = runner.run
			rr := httptest.NewRecorder()
			app.handleTestDeviceIP(rr, withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/"+udid, strings.NewReader(`{"ip":"192.0.2.1"}`))))
			var resp struct {
				OK     bool           `json:"ok"`
				Status statusSnapshot `json:"status"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if !resp.OK || len(resp.Status.Devices) != 1 {
				t.Fatalf("已注册设备重复测试应成功：%s", rr.Body.String())
			}
			wantConn := "wifi"
			if usb {
				wantConn = "usb"
			}
			if dev := resp.Status.Devices[0]; dev.Conn != wantConn || !dev.BackingUp {
				t.Fatalf("测试连接必须保留 USB 优先和备份互斥：%+v", dev)
			}
			for _, call := range runner.allCalls() {
				if call.name == cmdAddDevice {
					t.Error("已注册设备不应重复调用 add_device")
				}
			}
		})
	}
}

// 合法但不可达 IP → 200 + ok=false + 原因。
func TestHandleTestDeviceIPUnreachable(t *testing.T) {
	app := newApplication()
	app.reachProbe = func(ip string) (bool, string) {
		if ip != "192.0.2.1" {
			t.Fatalf("探针收到非规范地址 %q", ip)
		}
		return false, "测试地址不可达"
	}
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/U", strings.NewReader(`{"ip":"192.0.2.1"}`)))
	rr := httptest.NewRecorder()
	app.handleTestDeviceIP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("应 200，得 %d", rr.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != false {
		t.Errorf("不可达 IP 应 ok=false，得 %v", resp["ok"])
	}
	if s, _ := resp["reason"].(string); strings.TrimSpace(s) == "" {
		t.Error("应附带不可达原因")
	}
}

// 非法或禁止的地址必须在拨号/DNS 前拒绝，避免把端点变成内网与 DNS 探针。
func TestHandleTestDeviceIPRejectsUnsafeAddressBeforeProbe(t *testing.T) {
	tests := []string{
		"not-an-ip",
		"0.0.0.0",
		"127.0.0.1",
		"169.254.1.1",
		"224.0.0.1",
		"::",
		"::1",
		"fe80::1",
		"ff02::1",
	}
	for _, ip := range tests {
		t.Run(ip, func(t *testing.T) {
			app := newApplication()
			probes := 0
			app.reachProbe = func(string) (bool, string) {
				probes++
				return true, "不应执行"
			}
			body, err := json.Marshal(map[string]string{"ip": ip})
			if err != nil {
				t.Fatal(err)
			}
			req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/U", strings.NewReader(string(body))))
			rr := httptest.NewRecorder()
			app.handleTestDeviceIP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%q 应 400，得 %d: %s", ip, rr.Code, rr.Body.String())
			}
			if probes != 0 {
				t.Fatalf("%q 被拒绝前不应拨号/DNS，实际调用探针 %d 次", ip, probes)
			}
		})
	}
}

// 空 IP → 400。
func TestHandleTestDeviceIPEmpty(t *testing.T) {
	app := newApplication()
	req := withCSRF(httptest.NewRequest(http.MethodPost, "/api/test-ip/U", strings.NewReader(`{}`)))
	rr := httptest.NewRecorder()
	app.handleTestDeviceIP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("空 IP 应 400，得 %d", rr.Code)
	}
}

// 缺 CSRF → 403。
func TestHandleTestDeviceIPNoCSRF(t *testing.T) {
	app := newApplication()
	req := httptest.NewRequest(http.MethodPost, "/api/test-ip/U", strings.NewReader(`{"ip":"192.0.2.1"}`))
	rr := httptest.NewRecorder()
	app.handleTestDeviceIP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("无 CSRF 应 403，得 %d", rr.Code)
	}
}
