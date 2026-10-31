package backupstore

import (
	"fmt"
	"math"
	"os"
	"strings"
	"syscall"
	"time"
)

func saturatedProduct(a, b uint64) uint64 {
	if b != 0 && a > math.MaxUint64/b {
		return math.MaxUint64
	}
	return a * b
}
func identityText(dev, ino uint64, sec, nsec int64) string {
	return fmt.Sprintf("%d:%d:%d:%d", dev, ino, sec, nsec)
}

// 设备号由挂载分配，不能跨重启持久化；保留 inode 和变更时间识别实体替换。
func persistentFileIdentity(info os.FileInfo) string {
	identity := fileIdentity(info)
	_, rest, ok := strings.Cut(identity, ":")
	if !ok {
		return identity
	}
	return rest
}
func allocatedBytes(info os.FileInfo) int64 {
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		return s.Blocks * 512
	}
	return 0
}
func setFileTime(file *os.File, t time.Time) error {
	tv := syscall.NsecToTimeval(t.UnixNano())
	return syscall.Futimes(int(file.Fd()), []syscall.Timeval{tv, tv})
}
