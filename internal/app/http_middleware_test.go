package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func performRequest(handler http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func testAuthManager(password string) *authManager {
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	manager := newBootstrapAuthManager(cfg)
	manager.passwordHash = sha256.Sum256([]byte(password))
	return manager
}

func TestAuthProtectsCompleteRouteTreeExceptHealth(t *testing.T) {
	protected := testAuthManager("correct-password").Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	if got := performRequest(protected, "GET", "/", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("无凭据应返回 401，得到 %d", got)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("iosbackup", "correct-password")
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("正确凭据应通过，得到 %d", rr.Code)
	}
}

func TestRouteTreeAuthAndCSRFWiring(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.authManager = testAuthManager("correct-password")
	app.csrfManager = &csrfManager{token: "installation-token"}
	handler := app.setupRoutes()

	if got := performRequest(handler, "GET", "/healthz", nil, nil).Code; got != http.StatusOK {
		t.Fatalf("healthz 必须免认证，得到 %d", got)
	}
	if got := performRequest(handler, "GET", "/", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("首页必须受保护，得到 %d", got)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("iosbackup", "correct-password")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("正确认证应访问首页，得到 %d", rr.Code)
	}

	req = httptest.NewRequest("POST", "/api/notifications/test", strings.NewReader(`{"message_type":"backup_success"}`))
	req.SetBasicAuth("iosbackup", "correct-password")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("真实路由缺少 CSRF token 应返回 403，得到 %d", rr.Code)
	}
}

func TestCSRFMiddlewareRequiresExactTokenAndRejectsCrossSite(t *testing.T) {
	csrf := &csrfManager{token: "installation-token"}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := csrf.Middleware(next)
	for name, headers := range map[string]map[string]string{
		"missing": nil,
		"wrong":   {csrfHeader: "1"},
		"mismatched-origin": {
			csrfHeader: "installation-token", "Origin": "https://evil.example",
		},
		"cross-site": {
			csrfHeader: "installation-token", "Sec-Fetch-Site": "cross-site",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := performRequest(handler, "POST", "/api/action", nil, headers).Code; got != http.StatusForbidden {
				t.Fatalf("应返回 403，得到 %d", got)
			}
		})
	}
	if got := performRequest(handler, "POST", "/api/action", nil, map[string]string{csrfHeader: "installation-token", "Sec-Fetch-Site": "same-origin"}).Code; got != http.StatusNoContent {
		t.Fatalf("正确 token 应通过，得到 %d", got)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost:4312/api/action", nil)
	req.Host = "localhost:4312"
	req.Header.Set(csrfHeader, "installation-token")
	req.Header.Set("Origin", "http://127.0.0.1:4312")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("浏览器已确认 same-origin 时，代理改写 Host 不应阻断请求，得到 %d: %s", rr.Code, rr.Body.String())
	}
	if got := performRequest(handler, "GET", "/api/read", nil, nil).Code; got != http.StatusNoContent {
		t.Fatalf("GET 不要求 CSRF，得到 %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	rr := performRequest(handler, "GET", "/", nil, nil)
	for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
		if rr.Header().Get(header) == "" {
			t.Fatalf("缺少安全响应头 %s", header)
		}
	}
	if strings.Contains(strings.ToLower(rr.Header().Get("Access-Control-Allow-Origin")), "*") {
		t.Fatal("不得启用宽松 CORS")
	}
}

func TestBodyLimitRejectsOversizedRequests(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := bodyLimitMiddleware(next)
	bigConfig := bytes.NewReader(make([]byte, configBodyLimit+1))
	if got := performRequest(handler, "POST", "/api/save-config/U", bigConfig, nil).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大配置请求应返回 413，得到 %d", got)
	}
	bigAction := bytes.NewReader(make([]byte, actionBodyLimit+1))
	if got := performRequest(handler, "POST", "/api/backup/U", bigAction, nil).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大动作请求应返回 413，得到 %d", got)
	}
}
