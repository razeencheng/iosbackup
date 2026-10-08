package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeConfigDefaults(t *testing.T) {
	cfg, err := loadRuntimeConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1" {
		t.Fatalf("listen address=%q", cfg.ListenAddr)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port=%d", cfg.Port)
	}
	if cfg.ConfigsRoot != "/configs" {
		t.Fatalf("configs root=%q", cfg.ConfigsRoot)
	}
	if cfg.BackupsRoot != "/backups" {
		t.Fatalf("backups root=%q", cfg.BackupsRoot)
	}
	if cfg.AuthEnabled {
		t.Fatal("auth must default to disabled")
	}
	if cfg.MaxHeavyJobs != 1 {
		t.Fatalf("max heavy jobs=%d", cfg.MaxHeavyJobs)
	}
	if cfg.PresenceInterval != 4*time.Second {
		t.Fatalf("presence interval=%s", cfg.PresenceInterval)
	}
	if cfg.DeviceDisconnectGrace != 30*time.Second {
		t.Fatalf("device disconnect grace=%s", cfg.DeviceDisconnectGrace)
	}
	if cfg.SchedulerInterval != 30*time.Second {
		t.Fatalf("scheduler interval=%s", cfg.SchedulerInterval)
	}
	if cfg.NetmuxdLogLevel != "warn" {
		t.Fatalf("netmuxd log level=%q", cfg.NetmuxdLogLevel)
	}
	if !cfg.WiFiPowerAssertion {
		t.Fatal("Wi-Fi power assertion must default to enabled")
	}
}

func TestRuntimeConfigCanDisableWiFiPowerAssertion(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_WIFI_POWER_ASSERTION": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WiFiPowerAssertion {
		t.Fatal("Wi-Fi power assertion emergency override should disable it")
	}
}

func TestRuntimeConfigCustomValues(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_LISTEN_ADDR":             "192.0.2.10",
		"PORT":                          "9000",
		"IOSBK_CONFIGS_DIR":             "/srv/iosbackup/configs",
		"IOSBK_BACKUPS_DIR":             "/srv/iosbackup/backups",
		"IOSBK_AUTH_ENABLED":            "true",
		"IOSBK_ADMIN_PASSWORD_FILE":     "/run/secrets/iosbackup-password",
		"IOSBK_MAX_HEAVY_JOBS":          "2",
		"IOSBK_PRESENCE_INTERVAL":       "15s",
		"IOSBK_DEVICE_DISCONNECT_GRACE": "45s",
		"IOSBK_SCHEDULER_INTERVAL":      "1m",
		"IOSBK_NETMUXD_LOG_LEVEL":       "DEBUG",
		"IOSBK_WIFI_POWER_ASSERTION":    "true",
		"IOSBK_MIN_FREE_BYTES":          "1048576",
		"IOSBK_WEBHOOK_ALLOW_CIDRS":     "192.168.0.0/16,fd00::/8",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "192.0.2.10" || cfg.Port != 9000 {
		t.Fatalf("listen=%s:%d", cfg.ListenAddr, cfg.Port)
	}
	if cfg.ConfigsRoot != "/srv/iosbackup/configs" || cfg.BackupsRoot != "/srv/iosbackup/backups" {
		t.Fatalf("paths=%q,%q", cfg.ConfigsRoot, cfg.BackupsRoot)
	}
	if !cfg.AuthEnabled {
		t.Fatalf("auth=%v", cfg.AuthEnabled)
	}
	if cfg.AdminPasswordFile != "/run/secrets/iosbackup-password" {
		t.Fatalf("password file=%q", cfg.AdminPasswordFile)
	}
	if cfg.MaxHeavyJobs != 2 || cfg.MinFreeBytes != 1048576 {
		t.Fatalf("limits=%d,%d", cfg.MaxHeavyJobs, cfg.MinFreeBytes)
	}
	if len(cfg.WebhookAllowCIDRs) != 2 {
		t.Fatalf("webhook allowlist=%v", cfg.WebhookAllowCIDRs)
	}
	if cfg.DeviceDisconnectGrace != 45*time.Second {
		t.Fatalf("device disconnect grace=%s", cfg.DeviceDisconnectGrace)
	}
	if cfg.NetmuxdLogLevel != "debug" {
		t.Fatalf("netmuxd log level=%q", cfg.NetmuxdLogLevel)
	}
	if !cfg.WiFiPowerAssertion {
		t.Fatal("Wi-Fi power assertion should be enabled")
	}
}

func TestRuntimeConfigRejectsInvalidValues(t *testing.T) {
	tests := []map[string]string{
		{"IOSBK_AUTH_ENABLED": "maybe"},
		{"PORT": "0"},
		{"PORT": "65536"},
		{"IOSBK_LISTEN_ADDR": "not-an-address"},
		{"IOSBK_MAX_HEAVY_JOBS": "0"},
		{"IOSBK_MAX_HEAVY_JOBS": "3"},
		{"IOSBK_CONFIGS_DIR": "relative"},
		{"IOSBK_BACKUPS_DIR": "relative"},
		{"IOSBK_ADMIN_PASSWORD_FILE": "relative"},
		{"IOSBK_PRESENCE_INTERVAL": "500ms"},
		{"IOSBK_DEVICE_DISCONNECT_GRACE": "4s"},
		{"IOSBK_SCHEDULER_INTERVAL": "0s"},
		{"IOSBK_NETMUXD_LOG_LEVEL": "verbose"},
		{"IOSBK_WIFI_BACKEND": "other"},
		{"IOSBK_WIFI_POWER_ASSERTION": "yes"},
		{"IOSBK_MIN_FREE_BYTES": "-1"},
		{"IOSBK_WEBHOOK_ALLOW_CIDRS": "0.0.0.0/0"},
		{"IOSBK_WEBHOOK_ALLOW_CIDRS": "not-a-cidr"},
	}
	for _, env := range tests {
		if _, err := loadRuntimeConfig(mapEnv(env)); err == nil {
			t.Fatalf("accepted %#v", env)
		}
	}
}

func TestUsbmuxd2WiFiBackendUsesDefaultMuxSocket(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_WIFI_BACKEND": "usbmuxd2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app := newApplicationWithRuntime(context.Background(), cfg)

	_, cancel, args, env := app.ideviceCommand(
		context.Background(),
		cmdKindShort,
		networkDevice("NET-USBMUXD2-001"),
		nil,
		cmdIdevicePair,
		[]string{"validate"},
	)
	if cancel != nil {
		defer cancel()
	}
	if !argsHas(args, "-n") {
		t.Fatalf("usbmuxd2 Wi-Fi 网络命令仍必须带 -n，args=%v", args)
	}
	if envHas(env, "USBMUXD_SOCKET_ADDRESS") {
		t.Fatalf("usbmuxd2 Wi-Fi 网络命令必须走默认 socket，env=%v", env)
	}
	if !envHas(env, "OPENSSL_CONF") {
		t.Fatalf("设备 TLS 配置仍应注入，env=%v", env)
	}
}

func TestUnauthenticatedNonLoopbackNeedsExplicitAcknowledgement(t *testing.T) {
	if _, err := loadRuntimeConfig(mapEnv(map[string]string{"IOSBK_LISTEN_ADDR": "0.0.0.0"})); err == nil {
		t.Fatal("关闭认证时监听非 loopback 必须显式确认")
	}
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_LISTEN_ADDR": "0.0.0.0", "IOSBK_INSECURE_ALLOW_REMOTE": "true",
	}))
	if err != nil || !cfg.InsecureAllowRemote {
		t.Fatalf("显式确认后应允许启动: %+v, %v", cfg, err)
	}
}

func TestRuntimeConfigBuildsAppPaths(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_CONFIGS_DIR": "/srv/config",
		"IOSBK_BACKUPS_DIR": "/srv/backup",
	}))
	if err != nil {
		t.Fatal(err)
	}
	paths := newAppPaths(cfg)
	if paths.BackupConfigFile != "/srv/config/backup_configs.json" {
		t.Fatalf("backup config=%q", paths.BackupConfigFile)
	}
	if paths.NotificationConfigFile != "/srv/config/notification_configs.json" {
		t.Fatalf("notification config=%q", paths.NotificationConfigFile)
	}
	if paths.SecretsFile != "/srv/config/secrets.enc" {
		t.Fatalf("secrets=%q", paths.SecretsFile)
	}
}

func TestRunReturnsForCancelledContext(t *testing.T) {
	cfg, err := loadRuntimeConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error=%v", err)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
