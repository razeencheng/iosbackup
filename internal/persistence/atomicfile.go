// Package persistence 提供与业务无关的本地持久化原语。
package persistence

import (
	"fmt"
	"os"
	"path/filepath"
)

type atomicTempFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type atomicDir interface {
	Sync() error
	Close() error
}

type atomicOps struct {
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (atomicTempFile, error)
	rename     func(string, string) error
	openDir    func(string) (atomicDir, error)
}

func defaultAtomicOps() atomicOps {
	return atomicOps{
		mkdirAll: os.MkdirAll,
		createTemp: func(dir, pattern string) (atomicTempFile, error) {
			return os.CreateTemp(dir, pattern)
		},
		rename: os.Rename,
		openDir: func(path string) (atomicDir, error) {
			return os.Open(path)
		},
	}
}

// WriteFileAtomic 在目标目录内写入并刷盘临时文件，然后以 rename 原子替换目标。
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	return writeFileAtomic(path, data, mode, defaultAtomicOps())
}

func writeFileAtomic(path string, data []byte, mode os.FileMode, ops atomicOps) (retErr error) {
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}

	tmp, err := ops.createTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpPath := tmp.Name()
	closeAttempted := false
	defer func() {
		if !closeAttempted {
			if closeErr := tmp.Close(); retErr == nil && closeErr != nil {
				retErr = fmt.Errorf("关闭临时文件: %w", closeErr)
			}
		}
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("设置临时文件权限: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("同步临时文件: %w", err)
	}
	closeErr := tmp.Close()
	closeAttempted = true
	if closeErr != nil {
		return fmt.Errorf("关闭临时文件: %w", closeErr)
	}

	if err := ops.rename(tmpPath, path); err != nil {
		return fmt.Errorf("替换配置文件: %w", err)
	}

	dirFile, err := ops.openDir(dir)
	if err != nil {
		return fmt.Errorf("打开配置目录: %w", err)
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("同步配置目录: %w", err)
	}
	return nil
}
