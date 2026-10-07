// Package config parses and validates immutable process configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr        = "127.0.0.1"
	defaultPort              = 8080
	defaultConfigsRoot       = "/configs"
	defaultBackupsRoot       = "/backups"
	defaultMaxHeavyJobs      = 1
	maxHeavyJobs             = 2
	defaultPresenceInterval  = 10 * time.Second
	defaultDisconnectGrace   = 30 * time.Second
	defaultSchedulerInterval = 30 * time.Second
	defaultNetmuxdLogLevel   = "warn"
)

const (
	WiFiBackendNetmuxd  = "netmuxd"
	WiFiBackendUSBMuxD2 = "usbmuxd2"
)

// Runtime 是进程启动时一次性解析并校验的运行配置。
// 运行期间不得重新读取环境变量；后续模块只接收该不可变值。
type Runtime struct {
	ListenAddr                   string
	Port                         int
	ConfigsRoot                  string
	BackupsRoot                  string
	AuthEnabled                  bool
	EnableExperimentalOperations bool
	InsecureAllowRemote          bool
	AdminPasswordFile            string
	SecretKey                    string
	SecretKeyFile                string
	MaxHeavyJobs                 int
	PresenceInterval             time.Duration
	DeviceDisconnectGrace        time.Duration
	BackupPreparationTimeout     time.Duration
	BackupInactivityTimeout      time.Duration
	BackupAuthorizationTimeout   time.Duration
	SchedulerInterval            time.Duration
	NetmuxdLogLevel              string
	WiFiBackend                  string
	WiFiPowerAssertion           bool
	MinFreeBytes                 uint64
	WebhookAllowCIDRs            []netip.Prefix
}

// Default 返回一份独立的默认运行配置。
func Default() Runtime {
	return Runtime{
		ListenAddr:                   defaultListenAddr,
		Port:                         defaultPort,
		ConfigsRoot:                  defaultConfigsRoot,
		BackupsRoot:                  defaultBackupsRoot,
		AuthEnabled:                  false,
		EnableExperimentalOperations: false,
		MaxHeavyJobs:                 defaultMaxHeavyJobs,
		PresenceInterval:             defaultPresenceInterval,
		DeviceDisconnectGrace:        defaultDisconnectGrace,
		BackupPreparationTimeout:     30 * time.Minute,
		BackupInactivityTimeout:      10 * time.Minute,
		BackupAuthorizationTimeout:   5 * time.Minute,
		SchedulerInterval:            defaultSchedulerInterval,
		NetmuxdLogLevel:              defaultNetmuxdLogLevel,
		WiFiBackend:                  WiFiBackendNetmuxd,
		WiFiPowerAssertion:           true,
	}
}

// Load 从给定的环境读取函数加载并校验运行配置。
func Load(getenv func(string) string) (Runtime, error) {
	cfg := Default()
	var err error

	if value := strings.TrimSpace(getenv("IOSBK_LISTEN_ADDR")); value != "" {
		addr, parseErr := netip.ParseAddr(value)
		if parseErr != nil {
			return Runtime{}, fmt.Errorf("IOSBK_LISTEN_ADDR: %w", parseErr)
		}
		cfg.ListenAddr = addr.String()
	}

	if value := strings.TrimSpace(getenv("PORT")); value != "" {
		cfg.Port, err = parseIntRange("PORT", value, 1, 65535)
		if err != nil {
			return Runtime{}, err
		}
	}

	if value := strings.TrimSpace(getenv("IOSBK_CONFIGS_DIR")); value != "" {
		cfg.ConfigsRoot, err = cleanAbsolutePath("IOSBK_CONFIGS_DIR", value, false)
		if err != nil {
			return Runtime{}, err
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_BACKUPS_DIR")); value != "" {
		cfg.BackupsRoot, err = cleanAbsolutePath("IOSBK_BACKUPS_DIR", value, false)
		if err != nil {
			return Runtime{}, err
		}
	}

	if value := strings.TrimSpace(getenv("IOSBK_AUTH_ENABLED")); value != "" {
		cfg.AuthEnabled, err = parseStrictBool("IOSBK_AUTH_ENABLED", value)
		if err != nil {
			return Runtime{}, err
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS")); value != "" {
		cfg.EnableExperimentalOperations, err = parseStrictBool("IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS", value)
		if err != nil {
			return Runtime{}, err
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_INSECURE_ALLOW_REMOTE")); value != "" {
		cfg.InsecureAllowRemote, err = parseStrictBool("IOSBK_INSECURE_ALLOW_REMOTE", value)
		if err != nil {
			return Runtime{}, err
		}
	}

	if raw := getenv("IOSBK_ADMIN_PASSWORD_FILE"); raw != "" {
		value := strings.TrimSpace(raw)
		cfg.AdminPasswordFile, err = cleanAbsolutePath("IOSBK_ADMIN_PASSWORD_FILE", value, true)
		if err != nil {
			return Runtime{}, err
		}
	}

	cfg.SecretKey = getenv("IOSBK_SECRET_KEY")
	if raw := getenv("IOSBK_SECRET_KEY_FILE"); raw != "" {
		value := strings.TrimSpace(raw)
		cfg.SecretKeyFile, err = cleanAbsolutePath("IOSBK_SECRET_KEY_FILE", value, true)
		if err != nil {
			return Runtime{}, err
		}
	}
	if cfg.SecretKey != "" && cfg.SecretKeyFile != "" {
		return Runtime{}, errors.New("IOSBK_SECRET_KEY 与 IOSBK_SECRET_KEY_FILE 不能同时配置")
	}

	if value := strings.TrimSpace(getenv("IOSBK_MAX_HEAVY_JOBS")); value != "" {
		cfg.MaxHeavyJobs, err = parseIntRange("IOSBK_MAX_HEAVY_JOBS", value, 1, maxHeavyJobs)
		if err != nil {
			return Runtime{}, err
		}
	}

	if value := strings.TrimSpace(getenv("IOSBK_PRESENCE_INTERVAL")); value != "" {
		cfg.PresenceInterval, err = parseDurationRange("IOSBK_PRESENCE_INTERVAL", value, time.Second, 10*time.Minute)
		if err != nil {
			return Runtime{}, err
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_DEVICE_DISCONNECT_GRACE")); value != "" {
		cfg.DeviceDisconnectGrace, err = parseDurationRange("IOSBK_DEVICE_DISCONNECT_GRACE", value, 5*time.Second, 10*time.Minute)
		if err != nil {
			return Runtime{}, err
		}
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"IOSBK_BACKUP_PREPARATION_TIMEOUT", &cfg.BackupPreparationTimeout},
		{"IOSBK_BACKUP_INACTIVITY_TIMEOUT", &cfg.BackupInactivityTimeout},
		{"IOSBK_BACKUP_AUTHORIZATION_TIMEOUT", &cfg.BackupAuthorizationTimeout},
	} {
		if value := strings.TrimSpace(getenv(item.name)); value != "" {
			*item.target, err = parseDurationRange(item.name, value, time.Second, 24*time.Hour)
			if err != nil {
				return Runtime{}, err
			}
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_SCHEDULER_INTERVAL")); value != "" {
		cfg.SchedulerInterval, err = parseDurationRange("IOSBK_SCHEDULER_INTERVAL", value, time.Second, 24*time.Hour)
		if err != nil {
			return Runtime{}, err
		}
	}
	if value := strings.ToLower(strings.TrimSpace(getenv("IOSBK_NETMUXD_LOG_LEVEL"))); value != "" {
		switch value {
		case "error", "warn", "info", "debug", "trace":
			cfg.NetmuxdLogLevel = value
		default:
			return Runtime{}, fmt.Errorf("IOSBK_NETMUXD_LOG_LEVEL 只接受 error/warn/info/debug/trace: %q", value)
		}
	}
	if value := strings.ToLower(strings.TrimSpace(getenv("IOSBK_WIFI_BACKEND"))); value != "" {
		switch value {
		case WiFiBackendNetmuxd, WiFiBackendUSBMuxD2:
			cfg.WiFiBackend = value
		default:
			return Runtime{}, fmt.Errorf("IOSBK_WIFI_BACKEND 只接受 netmuxd/usbmuxd2: %q", value)
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_WIFI_POWER_ASSERTION")); value != "" {
		cfg.WiFiPowerAssertion, err = parseStrictBool("IOSBK_WIFI_POWER_ASSERTION", value)
		if err != nil {
			return Runtime{}, err
		}
	}

	if value := strings.TrimSpace(getenv("IOSBK_MIN_FREE_BYTES")); value != "" {
		cfg.MinFreeBytes, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			return Runtime{}, fmt.Errorf("IOSBK_MIN_FREE_BYTES: %w", err)
		}
	}
	if value := strings.TrimSpace(getenv("IOSBK_WEBHOOK_ALLOW_CIDRS")); value != "" {
		for _, item := range strings.Split(value, ",") {
			prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(item))
			if parseErr != nil || !prefix.Addr().IsPrivate() {
				return Runtime{}, fmt.Errorf("IOSBK_WEBHOOK_ALLOW_CIDRS 只接受私网 CIDR: %q", item)
			}
			cfg.WebhookAllowCIDRs = append(cfg.WebhookAllowCIDRs, prefix.Masked())
		}
	}

	listenAddr, _ := netip.ParseAddr(cfg.ListenAddr)
	if !cfg.AuthEnabled && !listenAddr.IsLoopback() && !cfg.InsecureAllowRemote {
		return Runtime{}, errors.New("认证关闭时监听非 loopback 地址需要 IOSBK_INSECURE_ALLOW_REMOTE=true 显式确认")
	}
	return cfg, nil
}

// ServerAddress 返回适用于 net/http 的监听地址。
func (cfg Runtime) ServerAddress() string {
	return net.JoinHostPort(cfg.ListenAddr, strconv.Itoa(cfg.Port))
}

func parseStrictBool(name, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func parseIntRange(name, value string, min, max int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return parsed, nil
}

func parseDurationRange(name, value string, min, max time.Duration) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %s and %s", name, min, max)
	}
	return parsed, nil
}

func cleanAbsolutePath(name, value string, allowFile bool) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path", name)
	}
	cleaned := filepath.Clean(value)
	if cleaned == string(filepath.Separator) {
		kind := "directory"
		if allowFile {
			kind = "file"
		}
		return "", fmt.Errorf("%s must not use the filesystem root as a %s", name, kind)
	}
	return cleaned, nil
}
