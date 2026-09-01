package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const cmdSQLite3 = "/usr/local/bin/sqlite3"

var backupFileIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type backupManifestRow struct {
	FileID       string
	Domain       string
	RelativePath string
	Flags        int
}

type backupManifestRowStreamer func(context.Context, string, func(backupManifestRow) error) error

type unbackSummary struct {
	CompletedAt     string `json:"completed_at"`
	Files           int    `json:"files"`
	Directories     int    `json:"directories"`
	SkippedSymlinks int    `json:"skipped_symlinks"`
}

// Unback 把未加密备份的 Manifest.db 纯本地导出为可浏览目录。
// 上游 idevicebackup2 unback 自 iOS 10 备份格式变化后不可靠；本实现不连接手机，
// 并先写临时目录、成功后原子发布为 _unback_，失败不会残留半成品。
func (app *application) Unback(ctx context.Context, udid string) error {
	backupPath, release, err := app.acquireLocalBackupOp(udid)
	if err != nil {
		return err
	}
	defer release()

	manifestPath := filepath.Join(backupPath, "Manifest.db")
	if err := requirePlainSQLiteManifest(manifestPath); err != nil {
		return err
	}
	finalPath := filepath.Join(backupPath, "_unback_")
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("解包目录已存在: %s；为避免覆盖，请先确认并移走旧目录", finalPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查解包目录失败: %w", err)
	}
	stagingPath, err := os.MkdirTemp(backupPath, ".iosbackup-unback-")
	if err != nil {
		return fmt.Errorf("创建解包临时目录失败: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stagingPath)
		}
	}()

	streamer := app.manifestRows
	if streamer == nil {
		streamer = streamBackupManifestRows
	}
	summary := unbackSummary{}
	app.addInfoLog(udid, "开始从 Manifest.db 本地解包备份（无需连接手机）...")
	err = streamer(ctx, manifestPath, func(row backupManifestRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := safeUnbackTarget(stagingPath, row.Domain, row.RelativePath)
		if err != nil {
			return err
		}
		switch row.Flags {
		case 1:
			if !backupFileIDPattern.MatchString(row.FileID) {
				return fmt.Errorf("Manifest.db 包含非法 fileID %q", row.FileID)
			}
			source, err := backupObjectPath(backupPath, strings.ToLower(row.FileID))
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := copyBackupObject(source, target); err != nil {
				return fmt.Errorf("导出 %s/%s: %w", row.Domain, row.RelativePath, err)
			}
			summary.Files++
		case 2:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			summary.Directories++
		case 4:
			// 浏览用途不还原设备绝对符号链接，避免在宿主制造误导性/越界链接。
			summary.SkippedSymlinks++
		default:
			return fmt.Errorf("Manifest.db 包含不支持的 flags=%d", row.Flags)
		}
		processed := summary.Files + summary.Directories + summary.SkippedSymlinks
		if processed%5000 == 0 {
			app.addInfoLog(udid, fmt.Sprintf("本地解包进度：已处理 %d 项", processed))
		}
		return nil
	})
	if err != nil {
		app.addErrorLog(udid, fmt.Sprintf("本地 unback 失败: %v", err))
		return fmt.Errorf("unback 失败: %w", err)
	}
	summary.CompletedAt = nowBeijing().Format(time.RFC3339)
	marker, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(stagingPath, ".iosbackup-unback.json"), marker, 0o600); err != nil {
		return fmt.Errorf("写解包完成标记失败: %w", err)
	}
	if err := os.Chmod(stagingPath, 0o755); err != nil {
		return fmt.Errorf("设置解包目录权限失败: %w", err)
	}
	if err := os.Rename(stagingPath, finalPath); err != nil {
		return fmt.Errorf("发布解包目录失败: %w", err)
	}
	published = true
	app.addInfoLog(udid, fmt.Sprintf("unback 解包完成：%d 个文件、%d 个目录，跳过 %d 个设备符号链接", summary.Files, summary.Directories, summary.SkippedSymlinks))
	return nil
}

func (app *application) acquireLocalBackupOp(udid string) (string, func(), error) {
	if !udidPattern.MatchString(udid) {
		return "", nil, fmt.Errorf("无效的设备UDID")
	}
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return "", nil, err
	}
	if app.backupInProgress[udid] || app.checkInProgress[udid] {
		app.mu.Unlock()
		return "", nil, fmt.Errorf("设备 %s 正忙，正在执行备份操作，请稍后再试", udid)
	}
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	backupPath := filepath.Join(root, udid)
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	app.broadcastStatus()
	release := func() {
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		app.broadcastStatus()
	}
	return backupPath, release, nil
}

func requirePlainSQLiteManifest(manifestPath string) error {
	f, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("读取 Manifest.db 失败: %w", err)
	}
	defer f.Close()
	header := make([]byte, len("SQLite format 3\x00"))
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("Manifest.db 不完整: %w", err)
	}
	if string(header) != "SQLite format 3\x00" {
		return errors.New("本地 unback 当前只支持未加密且完整的备份；此 Manifest.db 不是明文 SQLite 数据库")
	}
	return nil
}

func safeUnbackTarget(stagingRoot, domain, relative string) (string, error) {
	if domain == "" || domain == "." || domain == ".." || strings.ContainsAny(domain, "/\\\x00\r\n") {
		return "", fmt.Errorf("Manifest.db 包含非法 domain %q", domain)
	}
	// iOS/Unix 文件名除 NUL 与路径分隔符外允许换行、制表等控制字节；
	// 这些字节本身不会造成越界，真正的边界由绝对路径与 clean 后的 .. 校验保证。
	if strings.ContainsRune(relative, '\x00') || path.IsAbs(relative) {
		return "", fmt.Errorf("Manifest.db 包含非法相对路径 %q", relative)
	}
	cleanRelative := path.Clean(relative)
	if cleanRelative == "." {
		cleanRelative = ""
	}
	if cleanRelative == ".." || strings.HasPrefix(cleanRelative, "../") {
		return "", fmt.Errorf("Manifest.db 路径越界: %q", relative)
	}
	target := filepath.Join(stagingRoot, domain, filepath.FromSlash(cleanRelative))
	rel, err := filepath.Rel(stagingRoot, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Manifest.db 路径越界: %q/%q", domain, relative)
	}
	return target, nil
}

func backupObjectPath(backupPath, fileID string) (string, error) {
	candidates := []string{
		filepath.Join(backupPath, fileID[:2], fileID),
		filepath.Join(backupPath, fileID), // 兼容较旧的平铺备份布局
	}
	for _, candidate := range candidates {
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("备份对象缺失: %s", fileID)
}

func copyBackupObject(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func streamBackupManifestRows(ctx context.Context, manifestPath string, onRow func(backupManifestRow) error) error {
	query := "SELECT fileID, domain, relativePath, flags FROM Files ORDER BY domain, relativePath;"
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := newExecCmd(cmdCtx, cmdSQLite3, "-readonly", "-batch", "-noheader", "-csv", manifestPath, query)
	cmd.SetGracefulCancel(time.Second)
	stdout, err := cmd.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := newTailBuffer(maxCommandErrorBytes)
	cmd.SetStderr(stderr)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 sqlite3 失败: %w", err)
	}

	reader := csv.NewReader(stdout)
	reader.FieldsPerRecord = 4
	reader.ReuseRecord = true
	var parseErr error
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			parseErr = err
			break
		}
		flags, err := strconv.Atoi(record[3])
		if err != nil {
			parseErr = fmt.Errorf("Manifest.db flags 无效 %q: %w", record[3], err)
			break
		}
		row := backupManifestRow{FileID: record[0], Domain: record[1], RelativePath: record[2], Flags: flags}
		if err := onRow(row); err != nil {
			parseErr = err
			break
		}
	}
	if parseErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if waitErr != nil {
		return fmt.Errorf("sqlite3 读取 Manifest.db 失败: %w（%s）", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}
