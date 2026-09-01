package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
