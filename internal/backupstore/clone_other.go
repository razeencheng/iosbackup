//go:build !linux

package backupstore

import (
	"os"
	"syscall"
)

func cloneFile(dst, src *os.File) error { return syscall.ENOTSUP }
