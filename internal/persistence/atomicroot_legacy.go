//go:build !go1.25

package persistence

import (
	"errors"
	"os"
)

// 发布使用 go.mod 指定工具链；旧工具链不能退回不受根句柄约束的路径写入。
func WriteFileAtomicRoot(*os.Root, string, []byte, os.FileMode) error {
	return errors.New("history storage requires Go 1.25 or newer")
}
func RenameWithinRoot(*os.Root, string, string) error {
	return errors.New("history storage requires Go 1.25 or newer")
}
