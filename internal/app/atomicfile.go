package app

import (
	"os"

	"iosbackup/internal/persistence"
)

// writeFileAtomic 保留应用内兼容入口；持久化语义由叶子包统一实现。
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	return persistence.WriteFileAtomic(path, data, mode)
}
