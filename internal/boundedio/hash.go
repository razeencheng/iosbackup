package boundedio

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// FileSHA256 在固定缓冲区中读取已打开文件的指定长度，各块之间响应取消。
// 调用者持有文件句柄，并负责核对文件身份及读取前后的版本。
func FileSHA256(ctx context.Context, file *os.File, size int64) (string, error) {
	digest := sha256.New()
	buf := make([]byte, 32<<10)
	for size > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		chunk := buf[:min(int64(len(buf)), size)]
		if _, err := io.ReadFull(file, chunk); err != nil {
			return "", err
		}
		digest.Write(chunk)
		size -= int64(len(chunk))
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
