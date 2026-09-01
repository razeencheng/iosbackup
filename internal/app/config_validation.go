package app

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func validateBackupConfig(cfg backupConfig, allowedRoots []string) error {
	if !udidPattern.MatchString(cfg.UDID) {
		return fmt.Errorf("UDID 格式无效")
	}
	if err := validateClock("start_time", cfg.StartTime); err != nil {
		return err
	}
	if err := validateClock("end_time", cfg.EndTime); err != nil {
		return err
	}
	if cfg.BackupInterval < 1 || cfg.BackupInterval > 720 {
		return fmt.Errorf("backup_interval 必须在 1 到 720 小时之间")
	}
	if cfg.MinBatteryLevel < 0 || cfg.MinBatteryLevel > 100 {
		return fmt.Errorf("min_battery_level 必须在 0 到 100 之间")
	}
	if cfg.BackupDirectory == "" || !pathWithinRoots(cfg.BackupDirectory, allowedRoots) {
		return fmt.Errorf("backup_directory 必须位于允许的备份目录内")
	}
	if cfg.NetworkAddress != "" {
		if _, err := parseDeviceNetworkAddress(cfg.NetworkAddress); err != nil {
			return fmt.Errorf("network_address %w", err)
		}
	}
	return nil
}

// parseDeviceNetworkAddress 只接受可作为远端 iOS 设备地址的 IP 字面量。
// 必须在任何 Dial/DNS 前调用，避免把管理 API 变成主机名解析器或特殊地址探针。
func parseDeviceNetworkAddress(value string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("不是有效 IP: %w", err)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.String() == "255.255.255.255" {
		return netip.Addr{}, fmt.Errorf("不允许使用未指定、回环、组播、广播或链路本地地址")
	}
	return addr, nil
}

func validateClock(field, value string) error {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return fmt.Errorf("%s 必须使用 HH:mm 格式", field)
	}
	return nil
}

func pathWithinRoots(path string, roots []string) bool {
	path, err := resolvePathForContainment(path)
	if err != nil {
		return false
	}
	for _, root := range roots {
		root, err = resolvePathForContainment(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolvePathForContainment 解析已存在路径段中的符号链接，同时允许最终目录尚未创建。
func resolvePathForContainment(path string) (string, error) {
	current, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
