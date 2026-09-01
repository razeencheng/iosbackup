package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newLoginTestApp(t *testing.T) *application {
	t.Helper()
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.authManager = testAuthManager("correct-password")
	app.csrfManager = &csrfManager{token: "installation-token"}
	return app
}

func passwordLoginRequest(handler http.Handler, password, csrfToken, next string) *httptest.ResponseRecorder {
	form := url.Values{
		"password":   {password},
		"csrf_token": {csrfToken},
		"next":       {next},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func findSessionCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == "iosbk_session" {
			return cookie
		}
	}
	t.Fatal("登录响应缺少 iosbk_session cookie")
	return nil
}

func TestBrowserNavigationRedirectsToApplicationLogin(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/", nil, map[string]string{"Accept": "text/html"})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("浏览器访问首页应跳转登录页，得到 %d", rr.Code)
	}
	if location := rr.Header().Get("Location"); location != "/login?next=%2F" {
		t.Fatalf("登录跳转地址错误: %q", location)
	}
	if challenge := rr.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Fatalf("应用登录页不得触发浏览器 Basic Auth 弹窗: %q", challenge)
	}
}

func TestUnauthenticatedAPIRemainsJSON401WithoutBrowserChallenge(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/api/version", nil, map[string]string{"Accept": "application/json"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("未认证 API 应返回 401，得到 %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("API 401 应返回 JSON，得到 %q", got)
	}
	if challenge := rr.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Fatalf("API 401 不应触发浏览器原生登录框: %q", challenge)
	}
}

func TestBasicAuthRemainsCompatibleForAPIClients(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.SetBasicAuth("iosbackup", "correct-password")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("现有 Basic Auth API 客户端必须继续可用，得到 %d", rr.Code)
	}
}

func TestLoginPageIsPublicAndUsesSelectedBrandCopy(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/login?next=%2F", nil, map[string]string{"Accept": "text/html"})
	if rr.Code != http.StatusOK {
		t.Fatalf("登录页应公开访问，得到 %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"回到家。", "连上 Wi‑Fi。", "备份自然发生。", "将 iPhone 和 iPad 自动备份到家里的 NAS", "/static/logo-mark.png"} {
		if !strings.Contains(body, want) {
			t.Fatalf("登录页缺少选定内容 %q", want)
		}
	}
	if strings.Contains(body, "correct-password") {
		t.Fatal("登录页不得回显管理员密码")
	}
	if strings.Contains(body, `name="username"`) || strings.Contains(body, `data-zh="用户名"`) || strings.Contains(body, `autocomplete="username"`) {
		t.Fatal("单管理员系统的登录页不应包含任何用户名字段")
	}
	if cache := rr.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("登录页必须禁止缓存，得到 %q", cache)
	}
}

func TestLoginUsesNativeFormSubmission(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/login", nil, map[string]string{"Accept": "text/html"})
	if rr.Code != http.StatusOK {
		t.Fatalf("登录页应正常打开，得到 %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, `addEventListener('submit'`) {
		t.Fatal("登录应使用浏览器原生表单提交，避免客户端加载状态干扰导航")
	}
	if !strings.Contains(body, `form method="post" action="/login"`) {
		t.Fatal("登录页必须保留可无 JavaScript 工作的原生 POST 表单")
	}
}

func TestLoginRequiresCSRFAndValidCredentials(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	if got := passwordLoginRequest(handler, "correct-password", "wrong-token", "/").Code; got != http.StatusForbidden {
		t.Fatalf("错误 CSRF token 应返回 403，得到 %d", got)
	}
	if got := passwordLoginRequest(handler, "wrong-password", "installation-token", "/").Code; got != http.StatusUnauthorized {
		t.Fatalf("错误凭据应返回 401，得到 %d", got)
	}
}

func TestLoginAcceptsPasswordWithoutUsername(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := passwordLoginRequest(handler, "correct-password", "installation-token", "/")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("唯一管理员应只用密码登录，得到 %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLoginAcceptsBrowserVerifiedSameOriginBehindLocalProxy(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	form := url.Values{
		"password":   {"correct-password"},
		"csrf_token": {"installation-token"},
		"next":       {"/"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost:4312/login", strings.NewReader(form.Encode()))
	req.Host = "localhost:4312"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:4312")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("浏览器已确认 same-origin 时，代理改写 Host 不应阻断登录，得到 %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLoginAcceptsValidCSRFWhenProxyRewritesHostAndFetchMetadataIsUnavailable(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	form := url.Values{
		"password":   {"correct-password"},
		"csrf_token": {"installation-token"},
		"next":       {"/"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://internal:9000/login", strings.NewReader(form.Encode()))
	req.Host = "internal:9000"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://10.10.0.65:9000")
	// Chromium may omit Sec-Fetch-Site in the embedded browser. The per-installation
	// form token must remain the authoritative same-page proof for the login route.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("有效登录 CSRF token 不应因代理改写 Host 而被拒绝，得到 %d: %s", rr.Code, rr.Body.String())
	}
}

func TestLoginStillRejectsCrossSiteFetchWithValidCSRF(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	form := url.Values{
		"password":   {"correct-password"},
		"csrf_token": {"installation-token"},
		"next":       {"/"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("明确的跨站登录请求必须继续拒绝，得到 %d", rr.Code)
	}
}

func TestLoginSessionUnlocksBrowserAndLogoutRevokesIt(t *testing.T) {
	app := newLoginTestApp(t)
	handler := app.setupRoutes()
	login := passwordLoginRequest(handler, "correct-password", "installation-token", "/")
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" {
		t.Fatalf("登录成功应跳转首页，得到 %d %q", login.Code, login.Header().Get("Location"))
	}
	cookie := findSessionCookie(t, login)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("session cookie 属性不安全: %#v", cookie)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("有效 session 应访问首页，得到 %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set(csrfHeader, "installation-token")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("登出应返回登录页，得到 %d %q", rr.Code, rr.Header().Get("Location"))
	}
	cleared := findSessionCookie(t, rr)
	if cleared.MaxAge >= 0 {
		t.Fatalf("登出必须清除 session cookie: %#v", cleared)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("登出后的旧 session 不得继续访问，得到 %d", rr.Code)
	}
}

func TestLoginRejectsExternalRedirectTarget(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	rr := passwordLoginRequest(handler, "correct-password", "installation-token", "https://evil.example/")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("外部 next 必须回退首页，得到 %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestAuthSessionExpiresAndCanBeRevoked(t *testing.T) {
	manager := testAuthManager("correct-password")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	token, err := manager.createSession(now)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.validateSession(token, now.Add(time.Hour)) {
		t.Fatal("未过期 session 应有效")
	}
	if manager.validateSession(token, now.Add(13*time.Hour)) {
		t.Fatal("超过 12 小时的 session 必须失效")
	}

	token, err = manager.createSession(now)
	if err != nil {
		t.Fatal(err)
	}
	manager.revokeSession(token)
	if manager.validateSession(token, now) {
		t.Fatal("登出撤销后的 session 必须失效")
	}
}

func TestAuthenticatedPagesExposeLogoutControl(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("iosbackup", "correct-password")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ 应正常打开，得到 %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `data-logout-control`) || !strings.Contains(rr.Body.String(), `fetch('/logout'`) {
		t.Fatal("/ 缺少可用的退出登录入口")
	}
}

func TestNotificationsPageUsesBackControlInsteadOfLogout(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.SetBasicAuth("iosbackup", "correct-password")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/notifications 应正常打开，得到 %d", rr.Code)
	}
	html := rr.Body.String()
	for _, want := range []string{
		`data-back-control`,
		`href="/"`,
		`data-i18n="notif.back"`,
		`#icon-arrow-left`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("通知设置页返回入口应包含 %q", want)
		}
	}
	for _, unwanted := range []string{`data-logout-control`, `logoutSession()`, `fetch('/logout'`} {
		if strings.Contains(html, unwanted) {
			t.Errorf("通知设置页不应包含退出登录实现 %q", unwanted)
		}
	}
}

func TestAuthDisabledPagesHideLogoutControl(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = false
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.csrfManager = &csrfManager{token: "installation-token"}
	handler := app.setupRoutes()

	for _, path := range []string{"/", "/notifications"} {
		rr := performRequest(handler, http.MethodGet, path, nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s 应正常打开，得到 %d", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), `data-logout-control`) {
			t.Fatalf("%s 在未启用登录保护时不应显示退出入口", path)
		}
	}
}
