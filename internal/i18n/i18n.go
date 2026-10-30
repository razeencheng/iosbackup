// Package i18n owns language-neutral message identifiers and the shared UI/API catalog.
package i18n

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:generate go run ./cmd/gencodes
//go:embed messages.json
var messagesJSON []byte

var catalog map[string]map[string]string
var parameterPattern = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
var languageTagPattern = regexp.MustCompile(`^[a-z]{2,8}(?:-[a-z0-9]{1,8})*$`)

func init() {
	if err := json.Unmarshal(messagesJSON, &catalog); err != nil {
		panic(err)
	}
}

// NormalizeLocale returns the supported locale or an empty string for an unsupported value.
func NormalizeLocale(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	if !languageTagPattern.MatchString(value) {
		return ""
	}
	switch {
	case value == "en", strings.HasPrefix(value, "en-"):
		return "en"
	case value == "zh", value == "zh-cn", value == "zh-hans", value == "zh-sg", strings.HasPrefix(value, "zh-hans-"):
		return "zh-CN"
	default:
		return ""
	}
}

// ResolveRequest honors an explicit supported preference before weighted browser preferences.
func ResolveRequest(r *http.Request) string {
	if cookie, err := r.Cookie("iosbk_lang"); err == nil {
		if locale := NormalizeLocale(cookie.Value); locale != "" {
			return locale
		}
	}
	type preference struct {
		locale  string
		quality float64
	}
	var preferences []preference
	for _, item := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		locale := NormalizeLocale(parts[0])
		if locale == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && key == "q" {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > 0 {
			preferences = append(preferences, preference{locale, quality})
		}
	}
	sort.SliceStable(preferences, func(i, j int) bool { return preferences[i].quality > preferences[j].quality })
	if len(preferences) > 0 {
		return preferences[0].locale
	}
	return "en"
}

// Text interpolates named parameters once; parameter values are never interpreted as templates.
func Text(locale, code string, params map[string]any) string {
	locale = NormalizeLocale(locale)
	if locale == "" {
		locale = "en"
	}
	message := catalog[locale][code]
	if message == "" {
		message = catalog["en"][code]
	}
	if message == "" {
		message = catalog[locale][OperationFailed]
	}
	return parameterPattern.ReplaceAllStringFunc(message, func(token string) string {
		if value, ok := params[token[1:len(token)-1]]; ok {
			return fmt.Sprint(value)
		}
		return token
	})
}

func HasCode(code string) bool { _, ok := catalog["en"][code]; return ok }

// CatalogJavaScript makes the same complete catalog available to the browser for live switching.
func CatalogJavaScript() []byte {
	data, _ := json.Marshal(catalog)
	return append(append([]byte("IOSBKI18n.register("), data...), []byte(");\n")...)
}

// MessageContext 保留可安全展示的业务上下文，不包含凭据或原始诊断。
type MessageContext struct {
	Kind    string `json:"kind"`
	Channel string `json:"channel"`
	Name    string `json:"name"`
}

// Error carries a stable code and parameters independently of operator diagnostics.
type Error struct {
	Code    string
	Params  map[string]any
	Detail  string
	Cause   error
	Context *MessageContext
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(code string, params map[string]any) *Error { return &Error{Code: code, Params: params} }

func (e *Error) Error() string {
	message := e.Message("en")
	if e.Detail != "" {
		return message + ": " + e.Detail
	}
	return message
}

// WithNotificationChannel 包装已知原因，保留原始错误链且不修改原错误。
func WithNotificationChannel(err error, channel, name string) error {
	var translated *Error
	if !errors.As(err, &translated) {
		return err
	}
	copied := *translated
	copied.Cause = err
	if translated.Params != nil {
		copied.Params = make(map[string]any, len(translated.Params))
		for key, value := range translated.Params {
			copied.Params[key] = value
		}
	}
	copied.Context = &MessageContext{Kind: "notification_channel", Channel: channel, Name: name}
	return &copied
}

// Message 只生成用户消息；机器码、参数和诊断分别保留。
func (e *Error) Message(locale string) string {
	reason := Text(locale, e.Code, e.Params)
	if e.Context == nil || e.Context.Kind != "notification_channel" {
		return reason
	}
	return Text(locale, NotificationChannelContext, map[string]any{"channel": e.Context.Channel, "name": e.Context.Name, "reason": reason})
}
