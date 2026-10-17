package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"iosbackup/internal/i18n"
)

// 服务端首屏与浏览器切换语言共用这一份嵌入资源。
var uiCatalog = loadUICatalog()
var uiParameter = regexp.MustCompile(`\{([\w]+)\}`)
var uiCatalogScript = makeUICatalogScript()

func loadUICatalog() map[string]map[string]string {
	source, err := templateFS.ReadFile("static/ui-messages.json")
	if err != nil {
		panic(fmt.Errorf("read UI message catalog: %w", err))
	}
	var catalog map[string]map[string]string
	if err := json.Unmarshal(source, &catalog); err != nil {
		panic(fmt.Errorf("decode UI message catalog: %w", err))
	}
	return catalog
}

// 返回普通字符串，保留 html/template 对正文和属性的自动转义。
// 参数使用 name, value 对；替换后的用户文本不会再次被当作占位符处理。
func uiText(locale, key string, args ...any) string {
	text, ok := uiCatalog[i18n.NormalizeLocale(locale)][key]
	if !ok {
		text, ok = uiCatalog["en"][key]
	}
	if !ok {
		return key
	}
	params := make(map[string]any, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		if name, ok := args[i].(string); ok {
			params[name] = args[i+1]
		}
	}
	return uiParameter.ReplaceAllStringFunc(text, func(match string) string {
		if value, ok := params[match[1:len(match)-1]]; ok && value != nil {
			return fmt.Sprint(value)
		}
		return match
	})
}

func makeUICatalogScript() []byte {
	// encoding/json 默认转义 HTML 敏感字符，资源始终作为 JS 数据交付。
	data, err := json.Marshal(uiCatalog)
	if err != nil {
		panic(fmt.Errorf("encode UI message catalog: %w", err))
	}
	return append(append([]byte("IOSBKI18n.register("), data...), []byte(");\n")...)
}

func handleUIMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	w.Header().Del("Vary")
	w.Header().Del("Content-Language")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(uiCatalogScript)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(uiCatalogScript)
	}
}
