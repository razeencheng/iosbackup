package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPagesRenderSelectedLanguageBeforeJavaScript(t *testing.T) {
	app := newLoginTestApp(t)
	app.networkIPLookup = func(context.Context) (map[string]string, error) { return map[string]string{}, nil }
	app.devices["SSR-PHONE"] = &device{UDID: "SSR-PHONE", Name: "用户命名的 iPhone", Connection: connectTypeUSB, IsOnline: true}
	handler := app.setupRoutes()
	for _, tc := range []struct {
		path, locale string
		want, reject []string
	}{
		{"/login", "en", []string{`>Home again.</span>`, `>Sign in</h2>`, `placeholder="Enter administrator password"`, `aria-label="Product features"`}, []string{"回到家。", "输入管理员密码"}},
		{"/login", "zh-CN", []string{`>回到家。</span>`, `>身份验证</h2>`, `placeholder="输入管理员密码"`, `aria-label="产品特点"`}, []string{">Home again.</span>"}},
		{"/", "en", []string{`>iOS Device Backup</div>`, `>Device settings</span>`, `>Minimum battery</div>`, "用户命名的 iPhone"}, nil},
		{"/", "zh-CN", []string{`>iOS 设备备份</div>`, `>设备设置</span>`, `>最低电量</div>`, "用户命名的 iPhone"}, nil},
		{"/notifications", "en", []string{`>Notifications</div>`, `>Notification language</label>`, `aria-label="Console"`}, nil},
		{"/notifications", "zh-CN", []string{`>通知设置</div>`, `>通知语言</label>`, `aria-label="备份控制台"`}, nil},
		{"/onboarding", "en", []string{`>Get this iPhone backing up at home</h1>`, `>0 of 4 steps completed</strong>`, `alt="Connect the phone to a computer and enable Wi-Fi visibility"`, `>Waiting for a USB device</b>`}, []string{"让这台 iPhone 开始在家自动备份", "准备以后通过 Wi-Fi 备份"}},
		{"/onboarding", "zh-CN", []string{`>让这台 iPhone 开始在家自动备份</h1>`, `>已完成 0 / 4 步</strong>`, `alt="手机连接电脑并开启 Wi-Fi 显示"`, `>等待 USB 设备</b>`}, nil},
	} {
		t.Run(tc.path+"/"+tc.locale, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.Header.Set("Accept-Language", tc.locale)
			if tc.path != "/login" {
				r.SetBasicAuth("iosbackup", "correct-password")
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("page status = %d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, button := range regexp.MustCompile(`<button[^>]*data-lang="(en|zh-CN)"[^>]*>`).FindAllStringSubmatch(body, -1) {
				selected := button[1] == tc.locale
				want := `aria-pressed="false"`
				if selected {
					want = `aria-pressed="true"`
				}
				if !strings.Contains(button[0], want) {
					t.Errorf("server-rendered language selection is incorrect: %s", button[0])
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("server-rendered %s %s is missing %q", tc.path, tc.locale, want)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(body, reject) {
					t.Errorf("server-rendered %s %s contains wrong-language copy %q", tc.path, tc.locale, reject)
				}
			}
		})
	}
}
