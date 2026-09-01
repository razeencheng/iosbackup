package app

import (
	"net"
	"strings"
	"testing"
	"time"
)

// reachableTCP：活监听可达；已关闭端口不可达且给出原因。
func TestReachableTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ok, reason := reachableTCP(ln.Addr().String(), 2*time.Second); !ok {
		t.Errorf("活监听应可达，得 reason=%q", reason)
	}

	// 取一个空闲端口再关掉 → 连接会被拒
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln2.Addr().String()
	ln2.Close()
	ok, reason := reachableTCP(addr, time.Second)
	if ok {
		t.Error("已关闭端口不应可达")
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("不可达应附带原因说明")
	}
}

// wifiReachable 探测设备的 lockdown-over-Wi-Fi 端口（62078）。
// 用 127.0.0.1（本机 62078 无监听 → 连接被拒，确定性）验证不可达分支。
func TestWifiReachableUnreachableIP(t *testing.T) {
	if ok, reason := wifiReachable("127.0.0.1"); ok || reason == "" {
		t.Errorf("本机 62078 无监听应不可达且有原因，得 ok=%v reason=%q", ok, reason)
	}
}
