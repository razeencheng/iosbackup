package app

import (
	"io"

	"iosbackup/internal/boundedio"
)

const (
	maxCommandErrorBytes     = 64 << 10
	maxBackupLogLineBytes    = 64 << 10
	backupLogTruncatedMarker = " [line truncated]"
)

// tailBuffer 是固定容量的尾部缓冲；别名保留应用层现有调用方式。
type tailBuffer = boundedio.TailBuffer

func newTailBuffer(limit int) *tailBuffer { return boundedio.NewTailBuffer(limit) }

// consumeBackupOutput 按分隔符消费日志，单行超过 64 KiB 后丢弃到下一个分隔符。
// 每次 emit 都创建新的小切片，不保留异常大行的 backing array。
func consumeBackupOutput(reader io.Reader, emit func(line string, carriageReturn bool)) {
	_ = consumeBoundedLines(reader, func(line string, carriageReturn bool) error {
		emit(line, carriageReturn)
		return nil
	})
}

func consumeBoundedLines(reader io.Reader, emit func(line string, carriageReturn bool) error) error {
	return boundedio.ConsumeLines(reader, maxBackupLogLineBytes, backupLogTruncatedMarker, emit)
}

// headBuffer 保留输出开头并记录截断，供体积较小但不可信的 info 响应使用。
type headBuffer = boundedio.HeadBuffer

func newHeadBuffer(limit int) *headBuffer { return boundedio.NewHeadBuffer(limit) }
