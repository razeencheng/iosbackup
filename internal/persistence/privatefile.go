package persistence

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// EnsurePrivateDirectory 只收紧专用配置目录自身，不递归修改用户数据。
// 最后一级不得是符号链接；打开后通过描述符设置权限，避免跟随替换后的路径。
func EnsurePrivateDirectory(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("配置目录必须是专用的绝对路径，不能使用文件系统根目录")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("打开配置目录（不允许符号链接）: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("配置路径必须是目录")
	}
	if err := f.Chmod(0700); err != nil {
		return fmt.Errorf("设置配置目录权限: %w", err)
	}
	return nil
}

// ReadSecretFile 拒绝符号链接和特殊文件，限长读取。private 用于本实例
// 管理的文件；外部只读挂载的凭据不修改权限。O_NONBLOCK 防止 FIFO 阻塞启动。
func ReadSecretFile(path string, limit int64, private bool) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("秘密文件必须是普通文件，不能是符号链接或特殊文件")
	}
	if private {
		if err := f.Chmod(0600); err != nil {
			return nil, fmt.Errorf("设置秘密文件权限: %w", err)
		}
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("秘密文件超过大小上限 %d 字节", limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("秘密文件超过大小上限 %d 字节", limit)
	}
	return data, nil
}

// CreatePrivateFile 将完整、已刷盘的文件以硬链接原子发布。目标已存在时返回
// false，不覆盖并发初始化的赢家。中断只会留下不可用的临时文件或完整目标。
func CreatePrivateFile(path string, data []byte) (bool, error) {
	dir := filepath.Dir(path)
	if err := EnsurePrivateDirectory(dir); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		return false, err
	}
	if err := f.Sync(); err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Link(f.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("发布秘密文件（文件系统须支持硬链接）: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return true, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
