package app

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	configBodyLimit int64 = 1 << 20
	actionBodyLimit int64 = 64 << 10
)

func (a *authManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		if !a.authenticateRequest(r) {
			w.Header().Set("Cache-Control", "no-store")
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") && !strings.HasPrefix(r.URL.Path, "/api/") {
				nextURL := r.URL.RequestURI()
				if nextURL == "" {
					nextURL = "/"
				}
				http.Redirect(w, r, "/login?"+url.Values{"next": {nextURL}}.Encode(), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusUnauthorized, "未认证")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *authManager) authenticateRequest(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	if cookie, err := r.Cookie(authSessionCookieName); err == nil && a.validateSession(cookie.Value, time.Now()) {
		return true
	}
	username, password, ok := r.BasicAuth()
	return ok && a.Authenticate(username, password)
}

func (c *csrfManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
		if fetchSite == "cross-site" {
			writeError(w, http.StatusForbidden, "跨站请求已拒绝")
			return
		}
		if !requestOriginAllowed(r) {
			writeError(w, http.StatusForbidden, "Origin 不匹配")
			return
		}
		if !constantTokenEqual(r.Header.Get(csrfHeader), c.Token()) {
			writeError(w, http.StatusForbidden, "CSRF token 无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTokenEqual(provided, expected string) bool {
	providedBytes, expectedBytes := []byte(provided), []byte(expected)
	return len(providedBytes) == len(expectedBytes) && subtle.ConstantTimeCompare(providedBytes, expectedBytes) == 1
}

func sameOriginHost(origin, host string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, host)
}

func requestOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || sameOriginHost(origin, r.Host) {
		return true
	}
	// Sec-Fetch-Site 由浏览器生成，页面脚本无法伪造；same-origin 可在反向代理
	// 改写 Host 时提供可靠的同源信号。CSRF token 仍需单独精确匹配。
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		limit := actionBodyLimit
		if strings.HasPrefix(r.URL.Path, "/api/save-config/") || r.URL.Path == "/api/notifications/config" {
			limit = configBodyLimit
		}
		if r.ContentLength > limit {
			writeError(w, http.StatusRequestEntityTooLarge, "请求体过大")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "读取请求体失败")
			return
		}
		if int64(len(body)) > limit {
			writeError(w, http.StatusRequestEntityTooLarge, "请求体过大")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; img-src 'self' data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func decodeRequestJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON 只能包含一个值")
		}
		return err
	}
	return nil
}
