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

	"iosbackup/internal/i18n"
)

var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func validateBackupConfig(cfg backupConfig, allowedRoots []string) error {
	if _, err := cfg.History.Normalize(); err != nil {
		return i18n.NewError(i18n.ConfigInvalid, nil)
	}
	if !udidPattern.MatchString(cfg.UDID) {
		return i18n.NewError(i18n.InvalidUDID, nil)
	}
	if err := validateClock("start_time", cfg.StartTime); err != nil {
		return err
	}
	if err := validateClock("end_time", cfg.EndTime); err != nil {
		return err
	}
	if cfg.BackupInterval < 1 || cfg.BackupInterval > 720 {
		return i18n.NewError(i18n.BackupIntervalRange, map[string]any{"min": 1, "max": 720})
	}
	if cfg.MinBatteryLevel < 0 || cfg.MinBatteryLevel > 100 {
		return i18n.NewError(i18n.BatteryLevelRange, map[string]any{"min": 0, "max": 100})
	}
	if cfg.BackupDirectory == "" || !pathWithinRoots(cfg.BackupDirectory, allowedRoots) {
		return i18n.NewError(i18n.BackupDirectoryNotAllowed, nil)
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
		return netip.Addr{}, &i18n.Error{Code: i18n.IPInvalid, Detail: err.Error(), Cause: err}
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.String() == "255.255.255.255" {
		return netip.Addr{}, i18n.NewError(i18n.IPNotAllowed, nil)
	}
	return addr, nil
}

func validateClock(field, value string) error {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return i18n.NewError(i18n.InvalidClock, map[string]any{"field": field})
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
