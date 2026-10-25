package backupstore

import (
	"fmt"
	"math"
	"os"
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
