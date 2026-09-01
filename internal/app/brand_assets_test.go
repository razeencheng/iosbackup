package app

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestSharedProductLogoMatchesApprovedOnboardingLogo(t *testing.T) {
	shared, err := fs.ReadFile(templateFS, "static/logo-mark.png")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := fs.ReadFile(templateFS, "static/onboarding-logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, approved) {
		t.Fatal("所有页面的共享 Logo 应使用已批准的手机与环形备份箭头版本")
	}
}

func readEmbeddedPage(t *testing.T, name string) string {
	t.Helper()
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAllPagesUseSharedFavicon(t *testing.T) {
	for _, name := range []string{"login.html", "index.html", "notifications.html", "onboarding.html"} {
		markup := readEmbeddedPage(t, name)
		if !strings.Contains(markup, `href="/static/favicon.ico?v={{.Version}}"`) {
			t.Errorf("%s 应引用带版本缓存参数的共享 favicon.ico", name)
		}
	}

	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/static/favicon.ico", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("favicon 静态资源应可访问，得到 %d", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Fatal("favicon 静态资源不能为空")
	}
}

func TestInnerPagesUseProductLogo(t *testing.T) {
	for _, name := range []string{"index.html", "notifications.html"} {
		markup := readEmbeddedPage(t, name)
		if !strings.Contains(markup, `class="iosbk-brand-logo"`) {
			t.Errorf("%s 缺少统一内页 Logo 容器", name)
		}
		if !strings.Contains(markup, `src="/static/logo-mark.png"`) {
			t.Errorf("%s 未使用正式产品 Logo", name)
		}
	}
}

func TestAllProductLogoContainersShowCompleteApprovedMark(t *testing.T) {
	for _, name := range []string{"login.html", "index.html", "notifications.html"} {
		markup := strings.ToLower(readEmbeddedPage(t, name))
		for _, want := range []string{"background:#eef5ff", "width:128%", "left:-14%", "top:-14%"} {
			if !strings.Contains(markup, want) {
				t.Errorf("%s 的 Logo 容器缺少完整展示规则 %q", name, want)
			}
		}
	}
}

func TestInnerPagesUseCoolNeutralPalette(t *testing.T) {
	warmNeutrals := []string{
		"#F4F3EE", "#E6E5DF", "#F1F0EA", "#E2E0D8", "#FAF9F5",
		"#FCFBF8", "#ECEBE4", "#EFEEE9", "#DEDCD4", "#E0DFD8",
		"#74767D", "#8A8C92", "#A7A8AD", "#B4B5BA", "#1B1C1E",
		"#2B2D31", "#5B5D63",
	}
	for _, name := range []string{"index.html", "notifications.html"} {
		markup := strings.ToUpper(readEmbeddedPage(t, name))
		if !strings.Contains(markup, "#EDF2F6") || !strings.Contains(markup, "#213041") {
			t.Errorf("%s 缺少已选冷色主题的页面背景或主文字色", name)
		}
		for _, color := range warmNeutrals {
			if strings.Contains(markup, color) {
				t.Errorf("%s 仍含暖灰色 %s", name, color)
			}
		}
	}
}

func TestEmptyStateUsesApprovedDeviceArtwork(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`data-empty-device-icon`,
		`src="/static/empty-device.png"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("空状态缺少已选设备图资源 %q", want)
		}
	}
	if strings.Contains(markup, `data-icon-part="wifi"`) {
		t.Fatal("空状态不应继续使用临时绘制的 Wi-Fi 图标")
	}

	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/static/empty-device.png", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("空状态设备图应可访问，得到 %d", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Fatal("空状态设备图不能为空")
	}
}

func TestPrimaryInterfaceIconsUseReadableVisualScale(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`.toolbar-icon-btn{width:46px;height:46px`,
		`.ui-icon{width:16px;height:16px`,
		`.toolbar-icon-btn .ui-icon{width:19px;height:19px`,
		`.device-icon-tile{width:54px;height:54px`,
		`.device-icon-svg{width:28px;height:28px`,
		`.snav .ui-icon{width:17px;height:17px`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("主界面缺少可读图标比例规则 %q", want)
		}
	}
}

func TestPrimaryPagesUseSemanticSVGIconSprite(t *testing.T) {
	wants := map[string][]string{
		"index.html": {
			`#icon-guide`, `#icon-bell`, `#icon-settings`, `#icon-backup-settings`,
			`#icon-backup-library`, `#icon-lock`, `#icon-restore`, `#icon-folder-check`,
			`#icon-network-check`, `#icon-pair`, `#icon-unpack`, `set('lock-open'`,
			`#icon-shield-unlock`,
		},
		"notifications.html": {
			`#icon-refresh`, `#icon-arrow-left`, `#icon-save`, `#icon-send`,
			`icon:'telegram'`, `icon:'mail'`, `icon:'message'`, `icon:'webhook'`,
		},
	}
	for name, symbols := range wants {
		markup := readEmbeddedPage(t, name)
		if strings.Contains(markup, `class="fa-`) || strings.Contains(markup, `icon:'fa-`) {
			t.Errorf("%s 不应继续使用会降级成字符的字体图标", name)
		}
		if strings.Contains(markup, `/static/icons.css`) {
			t.Errorf("%s 不应继续加载字符替代图标样式", name)
		}
		for _, symbol := range symbols {
			if !strings.Contains(markup, symbol) {
				t.Errorf("%s 缺少语义图标 %s", name, symbol)
			}
		}
	}

	sprite, err := fs.ReadFile(templateFS, "static/ui-icons.svg")
	if err != nil {
		t.Fatalf("读取 SVG 图标库失败: %v", err)
	}
	for _, id := range []string{
		"icon-guide", "icon-bell", "icon-settings", "icon-backup-settings",
		"icon-backup-library", "icon-lock", "icon-restore", "icon-folder-check",
		"icon-network-check", "icon-pair", "icon-unpack", "icon-lock-open",
		"icon-shield-unlock", "icon-refresh", "icon-log-out", "icon-save",
		"icon-send", "icon-telegram", "icon-mail", "icon-message", "icon-webhook",
	} {
		if !strings.Contains(string(sprite), `id="`+id+`"`) {
			t.Errorf("SVG 图标库缺少 %s", id)
		}
	}

	handler := newLoginTestApp(t).setupRoutes()
	rr := performRequest(handler, http.MethodGet, "/static/ui-icons.svg", nil, nil)
	if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
		t.Fatalf("SVG 图标库应可访问，状态 %d，长度 %d", rr.Code, rr.Body.Len())
	}
}

func TestOnboardingStatusUsesSVGInsteadOfTextGlyphs(t *testing.T) {
	markup := readEmbeddedPage(t, "onboarding.html")
	script, err := fs.ReadFile(templateFS, "static/onboarding.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"onboarding.html": markup, "onboarding.js": string(script)} {
		if strings.Contains(content, "✓") {
			t.Errorf("%s 不应把对勾字符当作图标", name)
		}
	}
	if !strings.Contains(markup, `#icon-check`) {
		t.Fatal("向导状态应使用共享 SVG 对勾图标")
	}
}

func TestDeviceSummaryControlsStayAlignedAndUnambiguous(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`:root{--iosbk-radius:9px}`,
		`.language-switch{height:46px`,
		`.device-heading-row{display:flex;align-items:center`,
		`.device-battery{display:inline-flex;align-items:center`,
		`.battery-icon{width:18px;height:10px`,
		`.device-action-row{display:flex;align-items:center`,
		`.connection-badge,.backup-action{height:42px;border-radius:var(--iosbk-radius)`,
		`.connection-icon{width:16px;height:16px`,
		`id="battery-{{$d.UDID}}" class="device-battery"`,
		`<svg class="device-icon-svg"`,
		`<svg class="battery-icon"`,
		`function batteryIconHTML(color)`,
		`function connectionIconHTML(conn)`,
		`var battery=document.getElementById('battery-'+u)`,
		`box.innerHTML = badgeHTML(d.conn);`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("设备摘要区缺少布局约束 %q", want)
		}
	}

	menuStart := strings.Index(markup, `id="menuBtn"`)
	if menuStart < 0 {
		t.Fatal("缺少设置菜单按钮")
	}
	menuEnd := strings.Index(markup[menuStart:], `</button>`)
	if menuEnd < 0 {
		t.Fatal("设置菜单按钮标记不完整")
	}
	if strings.Contains(markup[menuStart:menuStart+menuEnd], "fa-chevron-down") {
		t.Fatal("设置按钮不应再显示无必要的下拉箭头")
	}
	if strings.Contains(markup, "fa-cloud-arrow-up primary-action-icon") {
		t.Fatal("立即备份按钮不应显示容易被误解为向下箭头的图标")
	}
	if strings.Contains(markup, "fa-battery-") {
		t.Fatal("电量图标不应依赖可能缺字或偏移的字体图标")
	}
	if strings.Contains(markup, `id="bkpill-`) {
		t.Fatal("进度条已经表达备份状态，设备名称旁不应再显示重复的备份中标签")
	}
	if strings.Contains(markup, "fa-circle-notch spin primary-action-icon") {
		t.Fatal("备份中按钮不应再显示与进度条重复的旋转图标")
	}
	for _, oldIcon := range []string{"fa-solid fa-wifi", "fa-solid fa-bolt", "fa-solid fa-power-off"} {
		if strings.Contains(markup, oldIcon+`\"></i><span data-i18n="status.`) {
			t.Fatalf("连接状态不应继续依赖字体图标 %q", oldIcon)
		}
	}
}

func TestPrimaryPagesShareOneCornerRadiusToken(t *testing.T) {
	for _, name := range []string{"index.html", "login.html", "notifications.html"} {
		markup := readEmbeddedPage(t, name)
		if !strings.Contains(markup, `:root{--iosbk-radius:9px`) {
			t.Errorf("%s 未使用统一的 9px 圆角变量", name)
		}
	}

	css, err := fs.ReadFile(templateFS, "static/onboarding.css")
	if err != nil {
		t.Fatalf("读取向导样式失败: %v", err)
	}
	if !strings.Contains(string(css), `--iosbk-radius:9px`) {
		t.Fatal("向导页未使用统一的 9px 圆角变量")
	}
}
