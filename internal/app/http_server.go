package app

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultSSEGlobalLimit = 32
	defaultSSEPerIPLimit  = 4
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
}

type sseLimiter struct {
	mu        sync.Mutex
	globalMax int
	perIPMax  int
	total     int
	perIP     map[string]int
}

func newSSELimiter(globalMax, perIPMax int) *sseLimiter {
	return &sseLimiter{globalMax: globalMax, perIPMax: perIPMax, perIP: make(map[string]int)}
}

func (l *sseLimiter) Acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.globalMax || l.perIP[ip] >= l.perIPMax {
		return false
	}
	l.total++
	l.perIP[ip]++
	return true
}

func (l *sseLimiter) Release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perIP[ip] <= 0 {
		return
	}
	l.total--
	l.perIP[ip]--
	if l.perIP[ip] == 0 {
		delete(l.perIP, ip)
	}
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr == "" {
		return "unknown"
	}
	return r.RemoteAddr
}
