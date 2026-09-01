package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// 通过 usbmux 协议向 netmuxd 查询 ListDevices，解出每台网络设备「自动发现的 IP」。
// idevice_id 只给 UDID，不给地址；地址在 usbmux 设备记录的 NetworkAddress(sockaddr) 里。

// sockaddrToIP 从 usbmux NetworkAddress 的 sockaddr 字节解出 IP 字符串。
//   - family(前 2 字节 LE) = 2(AF_INET)：IPv4 在字节 4-7
//   - family = 10/30(AF_INET6)：IPv6 在字节 8-23
func sockaddrToIP(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	family := uint16(b[0]) | uint16(b[1])<<8
	switch family {
	case 2: // AF_INET
		if len(b) < 8 {
			return ""
		}
		return fmt.Sprintf("%d.%d.%d.%d", b[4], b[5], b[6], b[7])
	case 10, 30: // AF_INET6（Linux 10 / BSD 30）
		if len(b) < 24 {
			return ""
		}
		return net.IP(b[8:24]).String()
	}
	return ""
}

// parseNetworkIPs 解析 usbmux ListDevices 的 XML plist，返回 UDID→发现 IP（仅含有 NetworkAddress 的设备）。
func parseNetworkIPs(plistXML []byte) map[string]string {
	result := map[string]string{}
	root, err := decodePlist(plistXML)
	if err != nil {
		return result
	}
	m, ok := root.(map[string]interface{})
	if !ok {
		return result
	}
	list, ok := m["DeviceList"].([]interface{})
	if !ok {
		return result
	}
	for _, item := range list {
		dev, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		props, ok := dev["Properties"].(map[string]interface{})
		if !ok {
			continue
		}
		udid, _ := props["SerialNumber"].(string)
		naB64, _ := props["NetworkAddress"].(string)
		if udid == "" || naB64 == "" {
			continue
		}
		// plist 的 <data> base64 常带换行/缩进，去掉所有空白再解码
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(naB64), ""))
		if err != nil {
			continue
		}
		if ip := sockaddrToIP(raw); ip != "" {
			result[udid] = ip
		}
	}
	return result
}

// ---- 极简 XML plist 解码（dict/array/string/data/integer/bool），零三方依赖 ----

func decodePlist(data []byte) (interface{}, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "plist" {
			return plistValue(dec)
		}
	}
}

// plistValue 读取下一个值元素；遇到容器结束(EndElement)返回 (nil, nil)。
func plistValue(dec *xml.Decoder) (interface{}, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "dict":
				return plistDict(dec)
			case "array":
				return plistArray(dec)
			case "string", "data", "integer", "real", "date":
				return plistText(dec)
			case "true":
				_ = dec.Skip()
				return true, nil
			case "false":
				_ = dec.Skip()
				return false, nil
			default:
				return plistText(dec)
			}
		case xml.EndElement:
			return nil, nil // 容器结束
		}
	}
}

func plistText(dec *xml.Decoder) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.EndElement:
			return strings.TrimSpace(sb.String()), nil
		}
	}
}

func plistDict(dec *xml.Decoder) (map[string]interface{}, error) {
	m := map[string]interface{}{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "key" {
				key, err := plistText(dec)
				if err != nil {
					return nil, err
				}
				val, err := plistValue(dec)
				if err != nil {
					return nil, err
				}
				m[key] = val
			}
		case xml.EndElement: // </dict>
			return m, nil
		}
	}
}

func plistArray(dec *xml.Decoder) ([]interface{}, error) {
	var arr []interface{}
	for {
		v, err := plistValue(dec)
		if err != nil {
			return nil, err
		}
		if v == nil { // </array>
			return arr, nil
		}
		arr = append(arr, v)
	}
}

// ---- usbmux ListDevices 查询 ----

// discoveredNetworkIPs 连 netmuxd，发 usbmux ListDevices，返回 UDID→发现 IP。失败返回空 map。
func (app *application) discoveredNetworkIPs() map[string]string {
	conn, err := net.DialTimeout("tcp", netmuxdAddr, 2*time.Second)
	if err != nil {
		return map[string]string{}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	payload := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>MessageType</key><string>ListDevices</string></dict></plist>`)
	hdr := make([]byte, 16)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(16+len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:], 1)  // version
	binary.LittleEndian.PutUint32(hdr[8:], 8)  // message = plist
	binary.LittleEndian.PutUint32(hdr[12:], 1) // tag
	if _, err := conn.Write(append(hdr, payload...)); err != nil {
		return map[string]string{}
	}

	respHdr := make([]byte, 16)
	if _, err := io.ReadFull(conn, respHdr); err != nil {
		return map[string]string{}
	}
	total := binary.LittleEndian.Uint32(respHdr[0:])
	if total < 16 || total > 10*1024*1024 {
		return map[string]string{}
	}
	body := make([]byte, total-16)
	if _, err := io.ReadFull(conn, body); err != nil {
		return map[string]string{}
	}
	return parseNetworkIPs(body)
}
