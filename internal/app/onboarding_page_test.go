package app

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iosbackup/internal/buildinfo"
)

func TestOnboardingPageUsesEmbeddedRuntimeState(t *testing.T) {
	app := newApplication()
	handler := app.setupRoutes()
	req := httptest.NewRequest(http.MethodGet, "/onboarding", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /onboarding 应返回 200，得到 %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-csrf=`,
		`/static/onboarding.css`,
		`/static/onboarding.js`,
		`/static/onboarding-logo.png`,
		`首次设置`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("向导页面缺少 %q", want)
		}
	}
	if strings.Contains(body, "unpkg.com") || strings.Contains(body, "text/babel") {
		t.Error("正式向导不得依赖 CDN 或浏览器 Babel")
	}
	if strings.Contains(body, "vv"+strings.TrimPrefix(buildinfo.Version, "v")) {
		t.Errorf("版本号不得重复 v 前缀: %q", buildinfo.Version)
	}
}

func TestOnboardingScriptUsesExistingAPIsWithoutFakeCompletion(t *testing.T) {
	script, err := fs.ReadFile(templateFS, "static/onboarding.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(script)
	for _, want := range []string{
		`new EventSource('/api/events')`,
		`'/api/refresh'`,
		`'/api/pair/'`,
		`'/api/backup/'`,
		`pairing_state`,
		`backup_state`,
		`last_backup_error_code`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("向导脚本缺少 %q", want)
		}
	}
	if strings.Contains(s, "演示：") || strings.Contains(s, "Demo:") {
		t.Error("正式向导不得包含模拟完成文案")
	}
}

func TestOnboardingLanguageSwitchRelocalizesRuntimeLabels(t *testing.T) {
	script, err := fs.ReadFile(templateFS, "static/onboarding.js")
	if err != nil {
		t.Fatal(err)
	}
	markup, err := fs.ReadFile(templateFS, "templates/onboarding.html")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`function renderConnectionCopy()`,
		`text('wifiFlowDiscover'`,
		`text('backupSuccessTitle'`,
		`text('footerLocal'`,
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("语言切换缺少运行时重绘 %q", want)
		}
	}
	for _, want := range []string{
		`<i></i><span id="usbState">`,
		`<i></i><span id="pairState">`,
	} {
		if !strings.Contains(string(markup), want) {
			t.Errorf("状态圆点与文案应使用独立节点: %q", want)
		}
	}
}

func TestOnboardingHiddenToastDoesNotRenderAnEmptyFloatingBox(t *testing.T) {
	markup, err := fs.ReadFile(templateFS, "templates/onboarding.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `.toast[hidden]{display:none!important}`) {
		t.Fatal("隐藏状态的操作提示必须彻底退出布局")
	}
}

func TestHomeLinksOnboarding(t *testing.T) {
	markup, err := fs.ReadFile(templateFS, "templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markup), `href="/onboarding"`) {
		t.Error("首页菜单应提供首次使用向导入口")
	}
}

func TestOnboardingRejectsWrongMethod(t *testing.T) {
	app := newApplication()
	handler := app.setupRoutes()
	req := httptest.NewRequest(http.MethodPost, "/onboarding", nil)
	req.Header.Set(csrfHeader, app.csrfManager.Token())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /onboarding 应返回 405，得到 %d", rr.Code)
	}
}
