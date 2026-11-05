//go:build !linux

package backupstore

import (
	"fmt"
	"os"
	"syscall"
)

func mountIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrUnsafe
	}
	return fmt.Sprint(stat.Dev), nil
}
