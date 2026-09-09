package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestSockaddrToIPv4(t *testing.T) {
	// 真机实测格式：family(LE)=0x0002(AF_INET) + port(2) + IPv4(4)
	b := []byte{0x02, 0x00, 0x00, 0x00, 192, 0, 2, 249, 0, 0, 0, 0}
	if got := sockaddrToIP(b); got != "192.0.2.249" {
		t.Errorf("应解出 192.0.2.249，得到 %q", got)
	}
}

func TestQueryNetworkIPsDistinguishesEmptyFromFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"empty", `<plist><dict><key>DeviceList</key><array></array></dict></plist>`, false},
		{"missing list", `<plist><dict><key>Result</key><integer>1</integer></dict></plist>`, true},
		{"malformed", `not plist`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				header := make([]byte, 16)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				body := make([]byte, int(binary.LittleEndian.Uint32(header))-16)
				if _, err := io.ReadFull(conn, body); err != nil {
					return
				}
				binary.LittleEndian.PutUint32(header, uint32(16+len(tc.body)))
				_, _ = conn.Write(append(header, []byte(tc.body)...))
			}()
			ips, err := queryNetworkIPs(context.Background(), ln.Addr().String())
			<-done
			if (err != nil) != tc.wantErr || len(ips) != 0 {
				t.Fatalf("不能把查询失败解释为全部掉线：ips=%v err=%v", ips, err)
			}
		})
	}
}

func TestQueryNetworkIPsCancellationInterruptsRead(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		cancel() // 对端不发送响应，取消必须中断 ReadFull。
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, conn)
	}()
	started := time.Now()
	_, err = queryNetworkIPs(ctx, ln.Addr().String())
	<-done
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("取消未及时中断查询：err=%v elapsed=%s", err, time.Since(started))
	}
}

func TestSockaddrToIPTooShort(t *testing.T) {
	if got := sockaddrToIP([]byte{0x02}); got != "" {
		t.Errorf("过短应返回空，得到 %q", got)
	}
}

func TestSockaddrToIPNonInet(t *testing.T) {
	// 未知 family → 空
	b := []byte{0xff, 0x00, 0, 0, 1, 2, 3, 4}
	if got := sockaddrToIP(b); got != "" {
		t.Errorf("未知 family 应返回空，得到 %q", got)
	}
}

func TestParseNetworkIPs(t *testing.T) {
	addr := base64.StdEncoding.EncodeToString([]byte{0x02, 0x00, 0x00, 0x00, 192, 0, 2, 249})
	xml := `<?xml version="1.0"?><plist version="1.0"><dict>` +
		`<key>DeviceList</key><array>` +
		`<dict><key>DeviceID</key><integer>5</integer>` +
		`<key>Properties</key><dict>` +
		`<key>ConnectionType</key><string>Network</string>` +
		`<key>SerialNumber</key><string>UDID-1</string>` +
		`<key>NetworkAddress</key><data>` + addr + `</data>` +
		`</dict></dict>` +
		`</array></dict></plist>`
	m := parseNetworkIPs([]byte(xml))
	if m["UDID-1"] != "192.0.2.249" {
		t.Errorf("应解出 UDID-1 -> 192.0.2.249，得到 %v", m)
	}
}

func TestParseNetworkIPsMultipleAndUSBSkipped(t *testing.T) {
	addr := base64.StdEncoding.EncodeToString([]byte{0x02, 0x00, 0x00, 0x00, 192, 168, 1, 50})
	xml := `<?xml version="1.0"?><plist version="1.0"><dict>` +
		`<key>DeviceList</key><array>` +
		// 网络设备（有 NetworkAddress）
		`<dict><key>Properties</key><dict>` +
		`<key>SerialNumber</key><string>NET-DEV</string>` +
		`<key>NetworkAddress</key><data>` + addr + `</data>` +
		`</dict></dict>` +
		// USB 设备（无 NetworkAddress）→ 应跳过
		`<dict><key>Properties</key><dict>` +
		`<key>ConnectionType</key><string>USB</string>` +
		`<key>SerialNumber</key><string>USB-DEV</string>` +
		`</dict></dict>` +
		`</array></dict></plist>`
	m := parseNetworkIPs([]byte(xml))
	if m["NET-DEV"] != "192.168.1.50" {
		t.Errorf("网络设备应解出 IP，得到 %v", m)
	}
	if _, ok := m["USB-DEV"]; ok {
		t.Errorf("无 NetworkAddress 的设备不应出现，得到 %v", m)
	}
}

func TestParseNetworkIPsWrappedDataAndDoctype(t *testing.T) {
	// 真机响应：带 DOCTYPE，且 <data> 的 base64 带换行/缩进(plist 常见)
	addr := base64.StdEncoding.EncodeToString([]byte{0x02, 0x00, 0x00, 0x00, 192, 0, 2, 249})
	wrapped := "\n\t\t\t\t" + addr[:4] + "\n\t\t\t\t" + addr[4:] + "\n\t\t\t" // 内部换行+缩进
	xml := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` +
		`<plist version="1.0"><dict><key>DeviceList</key><array><dict>` +
		`<key>MessageType</key><string>Attached</string>` +
		`<key>Properties</key><dict>` +
		`<key>ConnectionType</key><string>Network</string>` +
		`<key>SerialNumber</key><string>UDID-W</string>` +
		`<key>NetworkAddress</key><data>` + wrapped + `</data>` +
		`</dict></dict></array></dict></plist>`
	m := parseNetworkIPs([]byte(xml))
	if m["UDID-W"] != "192.0.2.249" {
		t.Errorf("带 DOCTYPE + 换行 base64 应正常解析，得到 %v", m)
	}
}

func TestParseNetworkIPsGarbage(t *testing.T) {
	if m := parseNetworkIPs([]byte("not a plist")); len(m) != 0 {
		t.Errorf("非法输入应返回空 map，得到 %v", m)
	}
}
