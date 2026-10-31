//go:build linux

package backupstore

import (
	"math"
	"os"
	"syscall"
)

// Linux UAPI: _IOW(0x94, 9, int)，支持的发布架构为 amd64 / arm64。
// https://man7.org/linux/man-pages/man2/FICLONE.2const.html
func cloneFile(dst, src *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), 0x40049409, src.Fd())
	if errno != 0 {
		return errno
	}
	return nil
}
func StorageCapacity(path string) (Capacity, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Capacity{}, err
	}
	return capacityFromStatfs(stat), nil
}
func capacityFromStatfs(stat syscall.Statfs_t) Capacity {
	unit := stat.Frsize
	if unit <= 0 {
		unit = stat.Bsize
	}
	// -1 等未知计数以无符号形式返回，不能解释成无限可用空间。
	c := Capacity{BytesKnown: unit > 0 && stat.Bavail <= math.MaxInt64, InodesKnown: stat.Files > 0 && stat.Files <= math.MaxInt64 && stat.Ffree <= math.MaxInt64, Inodes: stat.Ffree}
	if c.BytesKnown {
		c.Bytes = saturatedProduct(stat.Bavail, uint64(unit))
	}
	return c
}
func fileIdentity(info os.FileInfo) string {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return identityText(uint64(s.Dev), s.Ino, s.Ctim.Sec, s.Ctim.Nsec)
}
