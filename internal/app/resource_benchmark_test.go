package app

import (
	"bytes"
	"io"
	"testing"
)

// 审计基线：旧路径 100,000 行约 82,173,744 B/op；32 MiB CombinedOutput 约
// 134,235,784 B/op；32 MiB 无换行日志还会额外保留约 40 MiB。
func BenchmarkBackupList100K(b *testing.B) {
	row := []byte("file.db,AppDomain,1024\n")
	data := bytes.Repeat(row, 100000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		count := 0
		_ = forEachBackupListRow(bytes.NewReader(data), func([]string) error {
			count++
			return nil
		})
		if count != 100000 {
			b.Fatal(count)
		}
	}
}

func BenchmarkCommandOutput32MiB(b *testing.B) {
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for n := 0; n < 1024; n++ {
			_, _ = io.Discard.Write(chunk)
		}
	}
}
