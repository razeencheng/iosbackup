package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"sort"
)

// 只识别有界的备份密钥包格式。未知字段/重复标签/截断不推断等价，
// 不能因宽松解析而把不同密码或不同类别的包装密钥归入同一保留代。
type backupKeybag struct {
	header map[string][]byte
	keys   []map[string][]byte
}

func parseBackupKeybag(data []byte) (backupKeybag, bool) {
	bag := backupKeybag{header: map[string][]byte{}}
	if len(data) == 0 || len(data) > 64<<10 {
		return bag, false
	}
	group := bag.header
	uuids, classes := map[string]bool{}, map[uint32]bool{}
	for len(data) != 0 {
		if len(data) < 8 {
			return bag, false
		}
		tag, size := string(data[:4]), uint64(binary.BigEndian.Uint32(data[4:8]))
		if size > uint64(len(data)-8) {
			return bag, false
		}
		value := data[8 : 8+int(size)]
		data = data[8+int(size):]
		if tag == "UUID" {
			if len(value) != 16 || uuids[string(value)] {
				return bag, false
			}
			uuids[string(value)] = true
			if bag.header["UUID"] != nil {
				if len(bag.keys) >= 32 {
					return bag, false
				}
				group = map[string][]byte{}
				bag.keys = append(bag.keys, group)
			}
		}
		if _, exists := group[tag]; exists {
			return bag, false
		}
		allowed := false
		if len(bag.keys) == 0 {
			switch tag {
			case "VERS", "TYPE", "WRAP", "ITER", "DPWT", "DPIC":
				allowed = len(value) == 4
			case "UUID":
				allowed = len(value) == 16
			case "HMCK", "SALT", "DPSL":
				allowed = len(value) >= 16 && len(value) <= 64
			}
		} else {
			switch tag {
			case "UUID":
				allowed = len(value) == 16
			case "CLAS", "WRAP", "KTYP":
				allowed = len(value) == 4
			case "WPKY":
				allowed = len(value) == 40
			}
		}
		if !allowed {
			return bag, false
		}
		group[tag] = value
	}
	for _, tag := range []string{"VERS", "TYPE", "UUID", "HMCK", "WRAP", "SALT", "ITER"} {
		if bag.header[tag] == nil {
			return bag, false
		}
	}
	if binary.BigEndian.Uint32(bag.header["TYPE"]) != 1 || len(bag.keys) == 0 {
		return bag, false
	}
	version := binary.BigEndian.Uint32(bag.header["VERS"])
	if version < 2 || version > 5 {
		return bag, false
	}
	if (bag.header["DPIC"] == nil) != (bag.header["DPSL"] == nil) {
		return bag, false
	}
	for _, key := range bag.keys {
		for _, tag := range []string{"UUID", "CLAS", "WRAP", "KTYP", "WPKY"} {
			if key[tag] == nil {
				return bag, false
			}
		}
		class := binary.BigEndian.Uint32(key["CLAS"])
		if class == 0 || classes[class] {
			return bag, false
		}
		classes[class] = true
	}
	return bag, true
}

// 仅规范化字段与完整类别组的顺序，类别与密钥始终成组，所有字节均参与摘要。
func (b backupKeybag) identity() [32]byte {
	encode := func(group map[string][]byte) []byte {
		tags := make([]string, 0, len(group))
		for tag := range group {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		var out []byte
		for _, tag := range tags {
			out = append(out, tag...)
			out = binary.BigEndian.AppendUint32(out, uint32(len(group[tag])))
			out = append(out, group[tag]...)
		}
		return out
	}
	encoded := append([]byte("iosbackup-keybag-v1\x00"), encode(b.header)...)
	groups := make([][]byte, 0, len(b.keys))
	for _, key := range b.keys {
		groups = append(groups, encode(key))
	}
	sort.Slice(groups, func(i, j int) bool { return bytes.Compare(groups[i], groups[j]) < 0 })
	for _, group := range groups {
		encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(group)))
		encoded = append(encoded, group...)
	}
	return sha256.Sum256(encoded)
}

// 此校验只证明给定密码能解开包装密钥，不读取清单/文件，不标记真机恢复已验证。
// 密钥派生在应用及代际锁外执行；迭代次数有上限，取消前后均检查上下文。
func backupKeybagPasswordMatches(ctx context.Context, data []byte, password string) bool {
	bag, ok := parseBackupKeybag(data)
	if !ok || password == "" || ctx.Err() != nil {
		return false
	}
	iterations := binary.BigEndian.Uint32(bag.header["ITER"])
	if iterations == 0 || iterations > 100_000 {
		return false
	}
	key := []byte(password)
	defer func() { clear(key) }()
	if value := bag.header["DPIC"]; value != nil {
		aux := binary.BigEndian.Uint32(value)
		if aux == 0 || aux > 20_000_000 {
			return false
		}
		derived, err := pbkdf2.Key(sha256.New, string(key), bag.header["DPSL"], int(aux), 32)
		clear(key)
		if err != nil || ctx.Err() != nil {
			clear(derived)
			return false
		}
		key = derived
	}
	derived, err := pbkdf2.Key(sha1.New, string(key), bag.header["SALT"], int(iterations), 32)
	clear(key)
	if err != nil || ctx.Err() != nil {
		clear(derived)
		return false
	}
	key = derived
	verified := 0
	for _, class := range bag.keys {
		wrap := binary.BigEndian.Uint32(class["WRAP"])
		// WRAP=3 的外层仍是密码包装，只验证其完整性，不解开内部设备密钥层。
		if (wrap != 2 && wrap != 3) || binary.BigEndian.Uint32(class["KTYP"]) != 0 {
			return false
		}
		if !backupWrappedKeyMatches(key, class["WPKY"]) {
			return false
		}
		verified++
	}
	return verified > 0 && ctx.Err() == nil
}

// RFC 3394 §2.2.2 / §2.2.3.1。只返回完整性结论，不将解包后的密钥交给调用方。
func backupWrappedKeyMatches(key, wrapped []byte) bool {
	if len(wrapped) != 40 {
		return false
	}
	cipher, err := aes.NewCipher(key)
	if err != nil {
		return false
	}
	r := bytes.Clone(wrapped)
	defer clear(r)
	var block [16]byte
	defer clear(block[:])
	for j := 5; j >= 0; j-- {
		for i := 4; i >= 1; i-- {
			binary.BigEndian.PutUint64(block[:8], binary.BigEndian.Uint64(r[:8])^uint64(4*j+i))
			copy(block[8:], r[i*8:(i+1)*8])
			cipher.Decrypt(block[:], block[:])
			copy(r[:8], block[:8])
			copy(r[i*8:(i+1)*8], block[8:])
		}
	}
	return subtle.ConstantTimeCompare(r[:8], []byte{0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6}) == 1
}
