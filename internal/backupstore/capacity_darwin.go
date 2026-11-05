//go:build darwin

package backupstore

import (
	"os"
	"syscall"
)

func StorageCapacity(path string) (Capacity, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Capacity{}, err
	}
	return Capacity{Bytes: saturatedProduct(stat.Bavail, uint64(stat.Bsize)), BytesKnown: stat.Bsize > 0, Inodes: stat.Ffree, InodesKnown: stat.Files > 0}, nil
}
func fileIdentity(info os.FileInfo) string {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return identityText(uint64(s.Dev), s.Ino, s.Ctimespec.Sec, s.Ctimespec.Nsec)
}
