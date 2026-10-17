package app

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestUICatalogServerTemplateTranslation(t *testing.T) {
	app := newApplication()
	app.setupRoutes()
	for _, tc := range []struct{ locale, wantTitle, wantProgress string }{
		{"en", "Sign in", "1 of 4 steps completed"},
		{"zh-CN", "身份验证", "已完成 1 / 4 步"},
		{"zh", "身份验证", "已完成 1 / 4 步"},
		{"unsupported", "Sign in", "1 of 4 steps completed"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			clone, err := app.htmlTemplate.Clone()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := clone.New("ssr-language-contract").Parse(`{{t . "login.title"}}|{{t . "guide.progressCount" "completed" 1 "total" 4}}|{{t . "bmgmt.total" "n" "<script>{completed}</script>" "completed" "replaced"}}`)
			if err != nil {
				t.Fatalf("shared translation must be available to server templates: %v", err)
			}
			var output bytes.Buffer
			if err := parsed.Execute(&output, tc.locale); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output.String(), tc.wantTitle+"|"+tc.wantProgress+"|") {
				t.Fatalf("locale %s: %s", tc.locale, output.String())
			}
			if !strings.Contains(output.String(), template.HTMLEscapeString("<script>{completed}</script>")) || strings.Contains(output.String(), "replaced") {
				t.Fatalf("parameters must be escaped and interpolated only once: %s", output.String())
			}
		})
	}
}

func TestUICatalogPublicReadOnlyScript(t *testing.T) {
	handler := newLoginTestApp(t).setupRoutes()
	var body string
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rr := performRequest(handler, method, "/static/ui-messages.js", nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("unauthenticated %s: %d", method, rr.Code)
		}
		if rr.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("unsafe catalog response headers: %v", rr.Header())
		}
		if rr.Header().Get("Content-Language") != "" || rr.Header().Get("Vary") != "" {
			t.Fatalf("whole catalog must not vary by request language or cookies: %v", rr.Header())
		}
		if method == http.MethodHead && rr.Body.Len() != 0 {
			t.Fatal("HEAD must not send script content")
		}
		if method == http.MethodGet {
			body = rr.Body.String()
		}
	}
	if !strings.HasPrefix(body, "IOSBKI18n.register(") || !strings.HasSuffix(strings.TrimSpace(body), ");") {
		t.Fatalf("invalid register wrapper: %.100s", body)
	}
	var delivered map[string]map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "IOSBKI18n.register("), ");")), &delivered); err != nil {
		t.Fatal(err)
	}
	if delivered["en"]["login.title"] != "Sign in" || delivered["zh-CN"]["login.title"] != "身份验证" {
		t.Fatal("both UI catalogs must be delivered together")
	}
	if !reflect.DeepEqual(delivered, uiMessageCatalog(t)) {
		t.Fatal("browser and server must use the same complete JSON catalog")
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rr := performRequest(handler, method, "/static/ui-messages.js", nil, nil)
		if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s must be rejected: %d %v", method, rr.Code, rr.Header())
		}
	}
}

func uiMessageCatalog(t *testing.T) map[string]map[string]string {
	t.Helper()
	source, err := templateFS.ReadFile("static/ui-messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]map[string]string
	if err := json.Unmarshal(source, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func frontendI18nProgram(t *testing.T) string {
	t.Helper()
	runtime, err := templateFS.ReadFile("static/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	app := newApplication()
	handler := app.setupRoutes()
	messages := httptest.NewRecorder()
	handler.ServeHTTP(messages, httptest.NewRequest(http.MethodGet, "/static/ui-messages.js", nil))
	if messages.Code != http.StatusOK {
		t.Fatalf("UI catalog status: %d", messages.Code)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/api-messages.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("API catalog status: %d", response.Code)
	}
	return `const window={document:{cookie:'',documentElement:{}},navigator:{languages:['zh-CN']},localStorage:{getItem(){return null;},setItem(){}},dispatchEvent(){},fetch(...args){return fetch(...args);}};class CustomEvent {constructor(type,options){this.type=type;this.detail=options.detail;}};` + string(runtime) + `const IOSBKI18n=window.IOSBKI18n;function tr(key,params){return IOSBKI18n.t(key,params);}function apiMessage(payload,fallback){return IOSBKI18n.message(payload,fallback);}` + messages.Body.String() + response.Body.String() + "\n"
}

func TestSharedI18nBehaviors(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 不可用")
	}
	cmd := exec.Command(node, "testdata/i18n.test.js")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("国际化行为回归: %v\n%s", err, out)
	}
}
