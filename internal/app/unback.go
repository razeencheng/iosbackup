package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"iosbackup/internal/i18n"
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
		return i18n.NewError(i18n.UnpackExists, map[string]any{"path": finalPath})
	} else if !errors.Is(err, os.ErrNotExist) {
		return operationError(i18n.UnpackFailed, err, "")
	}
	stagingPath, err := os.MkdirTemp(backupPath, ".iosbackup-unback-")
	if err != nil {
		return operationError(i18n.DirectoryCreateFailed, err, "")
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
	app.addInfoLog(udid, "event=unpack.started Reading local Manifest.db; device connection is not required")
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
				return fmt.Errorf("Manifest.db contains invalid fileID %q", row.FileID)
			}
			source, err := backupObjectPath(backupPath, strings.ToLower(row.FileID))
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := copyBackupObject(source, target); err != nil {
				return fmt.Errorf("export %s/%s: %w", row.Domain, row.RelativePath, err)
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
			return fmt.Errorf("Manifest.db contains unsupported flags=%d", row.Flags)
		}
		processed := summary.Files + summary.Directories + summary.SkippedSymlinks
		if processed%5000 == 0 {
			app.addInfoLog(udid, fmt.Sprintf("event=unpack.progress processed=%d", processed))
		}
		return nil
	})
	if err != nil {
		message := fmt.Sprintf("event=unpack.failed error=%v", err)
		app.addErrorLog(udid, message)
		app.notifySystemEvent("unpack_failed", map[string]string{"device_name": udid, "diagnostics": message})
		return operationError(i18n.UnpackFailed, err, "")
	}
	summary.CompletedAt = app.nowTime().Format(time.RFC3339)
	marker, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(stagingPath, ".iosbackup-unback.json"), marker, 0o600); err != nil {
		return fmt.Errorf("write unpack completion marker: %w", err)
	}
	if err := os.Chmod(stagingPath, 0o755); err != nil {
		return fmt.Errorf("set unpack directory permissions: %w", err)
	}
	if err := os.Rename(stagingPath, finalPath); err != nil {
		return fmt.Errorf("publish unpack directory: %w", err)
	}
	published = true
	app.addInfoLog(udid, fmt.Sprintf("event=unpack.completed files=%d directories=%d skipped_symlinks=%d", summary.Files, summary.Directories, summary.SkippedSymlinks))
	return nil
}

func (app *application) acquireLocalBackupOp(udid string, mutates ...bool) (string, func(), error) {
	if !udidPattern.MatchString(udid) {
		return "", nil, i18n.NewError(i18n.InvalidUDID, nil)
	}
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return "", nil, err
	}
	if app.backupInProgress[udid] || app.checkInProgress[udid] {
		app.mu.Unlock()
		return "", nil, i18n.NewError(i18n.Busy, nil)
	}
	if err := app.connectionAdmissionUnsafe(nil); err != nil {
		app.mu.Unlock()
		return "", nil, err
	}
	root := app.paths.BackupsRoot
	if cfg := app.configs[udid]; cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	backupPath := filepath.Join(root, udid)
	app.backupInProgress[udid] = true
	app.mu.Unlock()
	changed := len(mutates) > 0 && mutates[0]
	app.beginBackupIndexWrite(udid, root, changed)
	app.broadcastStatus()
	release := func() {
		manager := app.lockBackupIndexFinish()
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
		if manager != nil {
			manager.releaseWriteUnsafe(udid)
			manager.mu.Unlock()
		}
		app.broadcastStatus()
	}
	return backupPath, release, nil
}

func requirePlainSQLiteManifest(manifestPath string) error {
	f, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("read Manifest.db: %w", err)
	}
	defer f.Close()
	header := make([]byte, len("SQLite format 3\x00"))
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("incomplete Manifest.db: %w", err)
	}
	if string(header) != "SQLite format 3\x00" {
		return i18n.NewError(i18n.UnpackUnencryptedRequired, nil)
	}
	return nil
}

func safeUnbackTarget(stagingRoot, domain, relative string) (string, error) {
	if domain == "" || domain == "." || domain == ".." || strings.ContainsAny(domain, "/\\\x00\r\n") {
		return "", fmt.Errorf("Manifest.db contains invalid domain %q", domain)
	}
	// iOS/Unix 文件名除 NUL 与路径分隔符外允许换行、制表等控制字节；
	// 这些字节本身不会造成越界，真正的边界由绝对路径与 clean 后的 .. 校验保证。
	if strings.ContainsRune(relative, '\x00') || path.IsAbs(relative) {
		return "", fmt.Errorf("Manifest.db contains invalid relative path %q", relative)
	}
	cleanRelative := path.Clean(relative)
	if cleanRelative == "." {
		cleanRelative = ""
	}
	if cleanRelative == ".." || strings.HasPrefix(cleanRelative, "../") {
		return "", fmt.Errorf("Manifest.db path escapes the backup: %q", relative)
	}
	target := filepath.Join(stagingRoot, domain, filepath.FromSlash(cleanRelative))
	rel, err := filepath.Rel(stagingRoot, target)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Manifest.db path escapes the backup: %q/%q", domain, relative)
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
	return "", fmt.Errorf("backup object missing: %s", fileID)
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
	return streamBackupManifestRowsCommand(ctx, cmdSQLite3, manifestPath, onRow)
}

func streamBackupManifestRowsCommand(ctx context.Context, binary, manifestPath string, onRow func(backupManifestRow) error) error {
	root, err := os.OpenRoot(filepath.Dir(manifestPath))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := requireStableBackupManifest(root, filepath.Base(manifestPath)); err != nil {
		return err
	}
	file, err := openBackupIndexRegular(root, filepath.Base(manifestPath))
	if err != nil {
		return err
	}
	defer file.Close()
	if err := streamBackupManifestFileRows(ctx, binary, file, onRow); err != nil {
		return err
	}
	return requireStableBackupManifest(root, filepath.Base(manifestPath))
}

// streamBackupManifestFileRows 只读取继承的文件描述符，不重新打开可能被替换的来源路径。
// 调用方必须已排除本应用写入；发布前后仍需比较来源指纹以检测外部修改。
func streamBackupManifestFileRows(ctx context.Context, binary string, file *os.File, onRow func(backupManifestRow) error) error {
	query := "SELECT fileID, domain, relativePath, flags FROM Files ORDER BY domain, relativePath;"
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := newExecCmd(cmdCtx, binary, "-readonly", "-batch", "-noheader", "-csv", "file:/dev/fd/3?mode=ro&immutable=1", query)
	cmd.cmd.ExtraFiles = []*os.File{file}
	cmd.SetGracefulCancel(time.Second)
	stdout, err := cmd.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := newTailBuffer(maxCommandErrorBytes)
	cmd.SetStderr(stderr)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sqlite3: %w", err)
	}

	parseErr := readBackupManifestCSV(cmdCtx, stdout, onRow)
	if parseErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if waitErr != nil {
		return fmt.Errorf("sqlite3 could not read Manifest.db: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}
