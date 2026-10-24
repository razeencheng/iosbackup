package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"iosbackup/internal/i18n"
)

func indexReadError(code string, kind, cause error) error {
	if cause != nil {
		kind = errors.Join(kind, cause)
	}
	return &backupIndexReadError{Code: code, Err: kind}
}

// resolveBackupIndexSource 只在锁内复制配置；磁盘读取与连接服务完全独立。
// Key 绑定配置路径，Identity 绑定当前目录实体，临时卸载不会丢失历史缓存键。
func (app *application) resolveBackupIndexSource(ctx context.Context, udid string) (backupIndexSource, error) {
	if !udidPattern.MatchString(udid) {
		return backupIndexSource{}, indexReadError(i18n.InvalidUDID, errBackupIndexUnsafeSource, nil)
	}
	app.mu.RLock()
	blocked := app.deviceRemovalBlockedUnsafe(udid)
	cfg := app.configs[udid]
	knownConfig := cfg != nil
	knownDevice := app.devices[udid] != nil
	root := app.paths.BackupsRoot
	if cfg != nil && cfg.BackupDirectory != "" {
		root = cfg.BackupDirectory
	}
	app.mu.RUnlock()
	if blocked != nil {
		return backupIndexSource{}, blocked
	}
	if !knownConfig && !knownDevice {
		return backupIndexSource{}, errDeviceNotFound
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return backupIndexSource{}, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	source := backupIndexSource{UDID: udid, Directory: filepath.Join(absolute, udid), Key: indexDigest(filepath.Clean(absolute) + "\x00" + udid)}
	if err := ctx.Err(); err != nil {
		return source, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return source, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	rootFile, err := os.Open(canonical)
	if err != nil {
		return source, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	_, readErr := rootFile.ReadDir(1)
	closeErr := rootFile.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return source, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, readErr)
	}
	if closeErr != nil {
		return source, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, closeErr)
	}
	source.Directory = filepath.Join(canonical, udid)
	info, err := os.Lstat(source.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return source, indexReadError(i18n.BackupIndexMissing, errBackupIndexMissing, err)
	}
	if err != nil {
		return source, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return source, indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
	}
	source.Identity = indexDigest(canonical + "\x00" + indexFileIdentity(info))
	source.Fingerprint, err = backupIndexSourceFingerprint(ctx, source)
	return source, err
}

func indexDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func indexFileIdentity(info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	return fmt.Sprintf("%s:%d:%d", info.Name(), info.Size(), info.ModTime().UnixNano())
}

// originalBackupSize 忽略根目录中本程序生成的解包结果，保留普通隐藏文件和 .tmp 文件。
func originalBackupSize(ctx context.Context, directory string) (int64, error) {
	root, err := openBackupIndexRoot(backupIndexSource{Directory: directory})
	if err != nil {
		return 0, err
	}
	defer root.Close()
	return originalBackupSizeRoot(ctx, root)
}

func originalBackupSizeRoot(ctx context.Context, root *os.Root) (int64, error) {
	return originalBackupSizeFS(ctx, root.FS())
}

func originalBackupSizeFS(ctx context.Context, sourceFS fs.FS) (int64, error) {
	var total int64
	err := fs.WalkDir(sourceFS, ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if p != "." && filepath.Dir(p) == "." && (entry.Name() == "_unback_" || strings.HasPrefix(entry.Name(), ".iosbackup-unback-")) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
		}
		// 通过根句柄查询，避免旧版 Go 的 DirEntry 在祖先目录重命名后仍使用旧路径。
		info, err := fs.Stat(sourceFS, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
		}
		total, err = checkedBackupIndexSize(total, info.Size())
		return err
	})
	return total, err
}

// backupIndexSourceFingerprint 检查目录实体及关键清单文件，供发布前后验证。
// 不读取大体积 Manifest.db 全文，plist 额外计算内容摘要以识别等长修改。
func backupIndexSourceFingerprint(ctx context.Context, source backupIndexSource) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := openBackupIndexRoot(source)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s", source.Directory, indexFileIdentity(info))
	for _, name := range []string{"Manifest.db", "Manifest.db-wal", "Manifest.db-journal", "Manifest.plist", "Info.plist", "Status.plist"} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fi, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(&b, "\x00%s:missing", name)
			continue
		}
		if err != nil {
			return "", indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
		}
		if !fi.Mode().IsRegular() {
			return "", indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
		}
		fmt.Fprintf(&b, "\x00%s:%s:%d:%d", name, indexFileIdentity(fi), fi.Size(), fi.ModTime().UnixNano())
		if strings.HasSuffix(name, ".plist") {
			f, err := openBackupIndexRegular(root, name)
			if err != nil {
				return "", err
			}
			hash := sha256.New()
			n, copyErr := io.Copy(hash, io.LimitReader(f, backupIndexMaxPlistBytes+1))
			closeErr := f.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			if n > backupIndexMaxPlistBytes {
				return "", indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
			}
			fmt.Fprintf(&b, ":%x", hash.Sum(nil))
		}
	}
	return indexDigest(b.String()), nil
}

// openBackupIndexRoot 将整个读取绑定到目录句柄；祖先重命名/换链接不会使后续读取越界。
func openBackupIndexRoot(source backupIndexSource) (*os.Root, error) {
	info, err := os.Lstat(source.Directory)
	if err != nil {
		return nil, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
	}
	root, err := os.OpenRoot(source.Directory)
	if err != nil {
		return nil, indexReadError(i18n.BackupIndexUnavailable, errBackupIndexUnavailable, err)
	}
	opened, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	if !os.SameFile(info, opened) || (source.Identity != "" && source.Identity != indexDigest(filepath.Dir(source.Directory)+"\x00"+indexFileIdentity(opened))) {
		root.Close()
		return nil, indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
	}
	return root, nil
}

// openBackupIndexRegular 通过已锚定目录读取，禁止跟随最后一级链接并避免 FIFO 阻塞。
func openBackupIndexRegular(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, indexReadError(i18n.BackupIndexUnsafeSource, errBackupIndexUnsafeSource, nil)
	}
	return f, nil
}

func checkedBackupIndexSize(total, size int64) (int64, error) {
	if total < 0 || size < 0 || total > math.MaxInt64-size {
		return 0, indexReadError(i18n.BackupIndexLimit, errBackupIndexLimit, nil)
	}
	return total + size, nil
}
