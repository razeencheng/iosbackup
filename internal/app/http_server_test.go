package app

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPServerHasProductionTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:8080", http.NewServeMux())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("HTTP server 缺少资源边界: %+v", server)
	}
	if server.ReadHeaderTimeout > 30*time.Second {
		t.Fatal("ReadHeaderTimeout 不应过长")
	}
}

func TestSSELimit(t *testing.T) {
	limiter := newSSELimiter(2, 1)
	if !limiter.Acquire("192.0.2.10") {
		t.Fatal("首个连接应通过")
	}
	if limiter.Acquire("192.0.2.10") {
		t.Fatal("同一 IP 第二个连接应被限制")
	}
	if !limiter.Acquire("192.0.2.11") {
		t.Fatal("不同 IP 且全局未满应通过")
	}
	if limiter.Acquire("192.0.2.12") {
		t.Fatal("超过全局上限应被限制")
	}
	limiter.Release("192.0.2.10")
	if !limiter.Acquire("192.0.2.12") {
		t.Fatal("释放后应允许新连接")
	}
}
