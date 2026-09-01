package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var backupSessionFallback atomic.Uint64

// newBackupSessionID 为一次备份命令生成短标识，只用于关联应用与 netmuxd 诊断日志。
// 标识不包含 UDID、路径、密码或其他用户数据。
func newBackupSessionID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err == nil {
		return "bkp-" + hex.EncodeToString(raw)
	}
	fallback := uint64(time.Now().UnixNano()) ^ backupSessionFallback.Add(1)
	return fmt.Sprintf("bkp-%012x", fallback&0xffffffffffff)
}

func backupSessionLog(sessionID, message string) string {
	return fmt.Sprintf("[session=%s] %s", sessionID, message)
}
