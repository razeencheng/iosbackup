package app

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// handleHome 必须直接用内存缓存（app.devices）渲染，不在请求路径上同步探测设备。
// 这保证首页秒开；设备状态由后台 statusPoller + SSE 维护。
// 回归用：构造仅存在于缓存里的设备，断言它出现在首页 HTML 中。
func TestHandleHomeRendersFromCache(t *testing.T) {
	app := newApplication()
	tmplData, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	app.htmlTemplate = template.Must(template.New("index.html").Funcs(template.FuncMap{
		"contains": strings.Contains,
	}).Parse(string(tmplData)))

	// 仅写入内存缓存，不依赖任何 libimobiledevice 探测
	app.devices["U-CACHE"] = &device{UDID: "U-CACHE", Name: "缓存里的iPad", DeviceType: "iPad", IsOnline: true, BatteryLevel: 77}
	app.configs["U-CACHE"] = &backupConfig{UDID: "U-CACHE", StartTime: "22:00", EndTime: "06:00", BackupInterval: 24, MinBatteryLevel: 20, BackupDirectory: "/backups"}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	app.handleHome(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handleHome 应返回 200，得到 %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"U-CACHE", "缓存里的iPad"} {
		if !strings.Contains(body, want) {
			t.Errorf("首页应从缓存渲染并包含 %q", want)
		}
	}
}
