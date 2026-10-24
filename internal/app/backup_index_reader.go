package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"iosbackup/internal/i18n"
)

func readBackupIndexOverview(ctx context.Context, source backupIndexSource, converter backupPlistConverter) (result backupIndexOverview, err error) {
	result = backupIndexOverview{Status: "available", Freshness: "current"}
	defer func() {
		if err != nil {
			result.Status = "unavailable"
			result.Freshness = "unknown"
			result.MessageCode = backupIndexErrorCode(err)
		}
	}()
	root, err := openBackupIndexRoot(source)
	if err != nil {
		return result, err
	}
	defer root.Close()
	found := false
	for _, name := range []string{"Manifest.plist", "Info.plist", "Status.plist"} {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		f, err := openBackupIndexRegular(root, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, backupIndexMaxPlistBytes+1))
		closeErr := f.Close()
		if readErr != nil {
			return result, readErr
		}
		if closeErr != nil {
			return result, closeErr
		}
		values, err := parseBackupPlist(ctx, data, converter)
		if err != nil {
			return result, err
		}
		found = true
		if name == "Manifest.plist" {
			if value, ok := values["IsEncrypted"]; ok {
				encrypted, valid := value.(bool)
				if !valid {
					return result, plistCorrupt(errors.New("IsEncrypted must be boolean"))
				}
				result.Encrypted = &encrypted
			}
		}
		if name == "Info.plist" {
			if value, ok := values["Device Name"]; ok {
				deviceName, valid := value.(string)
				if !valid {
					return result, plistCorrupt(errors.New("Device Name must be string"))
				}
				result.DeviceName = deviceName
			}
			if value, ok := values["Last Backup Date"]; ok {
				date, valid := value.(time.Time)
				if !valid {
					return result, plistCorrupt(errors.New("Last Backup Date must be date"))
				}
				date = toBeijingTime(date)
				result.BackupDate = &date
			}
		}
		if name == "Status.plist" {
			if value, ok := values["Date"]; ok {
				date, valid := value.(time.Time)
				if !valid {
					return result, plistCorrupt(errors.New("Date must be date"))
				}
				date = toBeijingTime(date)
				result.BackupDate = &date
			}
		}
	}
	if !found {
		return result, plistCorrupt(errors.New("backup metadata absent"))
	}
	size, err := originalBackupSizeRoot(ctx, root)
	if err != nil {
		return result, err
	}
	result.SizeBytes = &size
	result.ReadAt = nowBeijing()
	return result, nil
}

func streamLocalBackupIndex(ctx context.Context, source backupIndexSource, encrypted *bool, streamer backupManifestRowStreamer, onRecord func(backupIndexRecord) error) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := openBackupIndexRoot(source)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if encrypted != nil && *encrypted {
		return 0, indexReadError(i18n.BackupIndexEncrypted, errBackupIndexEncrypted, nil)
	}
	if err := requireStableBackupManifest(root, "Manifest.db"); err != nil {
		return 0, err
	}
	manifestPath := filepath.Join(source.Directory, "Manifest.db")
	file, err := openBackupIndexRegular(root, "Manifest.db")
	if err != nil {
		return 0, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	header := make([]byte, len("SQLite format 3\x00"))
	_, readErr := io.ReadFull(file, header)
	defer file.Close()
	if readErr != nil || string(header) != "SQLite format 3\x00" {
		return 0, plistCorrupt(errors.New("Manifest.db is not a SQLite database"))
	}
	if streamer == nil {
		streamer = func(ctx context.Context, _ string, onRow func(backupManifestRow) error) error {
			return streamBackupManifestFileRows(ctx, cmdSQLite3, file, onRow)
		}
	}
	count := 0
	err = streamer(ctx, manifestPath, func(row backupManifestRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(row.FileID)+len(row.Domain)+len(row.RelativePath) > backupIndexMaxRecordBytes {
			return indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
		}
		if count == math.MaxInt {
			return indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
		}
		if err := onRecord(backupIndexRecord{FileID: row.FileID, Domain: row.Domain, RelativePath: row.RelativePath, Flags: row.Flags}); err != nil {
			return err
		}
		count++
		return nil
	})
	if err == nil {
		err = requireStableBackupManifest(root, "Manifest.db")
	}
	return count, err
}

// readBackupManifestCSV 按记录限流，保留引号内的 CR/LF 字节，不截断真实文件名。
// encoding/csv 会把引号内 CRLF 归一化，故此处使用严格的四列流式解析器。
func readBackupManifestCSV(ctx context.Context, r io.Reader, onRow func(backupManifestRow) error) error {
	reader := bufio.NewReaderSize(r, 32<<10)
	for {
		fields := make([]string, 0, 4)
		var field strings.Builder
		state := byte(0) // 0 字段开始，1 普通字段，2 引号字段，3 引号结束
		consumed := 0
		complete := false
		eof := false
		for !complete {
			if err := ctx.Err(); err != nil {
				return err
			}
			c, err := reader.ReadByte()
			if errors.Is(err, io.EOF) {
				if state == 2 {
					return plistCorrupt(errors.New("unterminated CSV quoted field"))
				}
				if consumed == 0 && len(fields) == 0 {
					return nil
				}
				complete = true
				eof = true
				break
			}
			if err != nil {
				return err
			}
			consumed++
			if consumed > backupIndexMaxRecordBytes {
				return indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
			}
			if state == 2 {
				if c == '"' {
					state = 3
				} else {
					field.WriteByte(c)
				}
				continue
			}
			if c == '\r' {
				next, err := reader.Peek(1)
				if err == nil && next[0] == '\n' {
					_, _ = reader.ReadByte()
					consumed++
					c = '\n'
				}
			}
			if c == '\n' {
				if consumed == 1 && len(fields) == 0 && field.Len() == 0 {
					consumed = 0
					continue
				}
				complete = true
				break
			}
			if c == ',' {
				fields = append(fields, field.String())
				if len(fields) >= 4 {
					return plistCorrupt(errors.New("too many manifest columns"))
				}
				field.Reset()
				state = 0
				continue
			}
			switch state {
			case 0:
				if c == '"' {
					state = 2
				} else {
					state = 1
					field.WriteByte(c)
				}
			case 1:
				if c == '"' {
					return plistCorrupt(errors.New("bare quote in manifest CSV"))
				}
				field.WriteByte(c)
			case 3:
				if c != '"' {
					return plistCorrupt(errors.New("unexpected data after quoted field"))
				}
				field.WriteByte(c)
				state = 2
			}
		}
		if consumed > backupIndexMaxRecordBytes {
			return indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
		}
		fields = append(fields, field.String())
		if len(fields) != 4 {
			return plistCorrupt(fmt.Errorf("manifest CSV has %d columns", len(fields)))
		}
		flags, err := strconv.Atoi(fields[3])
		if err != nil {
			return plistCorrupt(fmt.Errorf("invalid manifest flags: %w", err))
		}
		if err := onRow(backupManifestRow{FileID: fields[0], Domain: fields[1], RelativePath: fields[2], Flags: flags}); err != nil {
			return err
		}
		if eof {
			return nil
		}
	}
}

// immutable 只可用于独立且稳定的数据库；非空事务侧车需要先由备份生成者完成提交/检查点。
// 否则 SQLite 会忽略 WAL 中已提交记录，不能把这种视图发布成完整清单。
func requireStableBackupManifest(root *os.Root, name string) error {
	for _, suffix := range []string{"-wal", "-journal"} {
		info, err := root.Lstat(name + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
		}
		if info.Size() != 0 {
			return indexReadError(i18n.BackupIndexRefreshFailed, errBackupIndexUnavailable, errors.New("Manifest.db has a nonempty transaction sidecar"))
		}
	}
	return nil
}
