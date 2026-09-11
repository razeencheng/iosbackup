package config_test

import (
	"math"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"iosbackup/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddr != "127.0.0.1" {
		t.Fatalf("listen address=%q", cfg.ListenAddr)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port=%d", cfg.Port)
	}
	if cfg.ConfigsRoot != "/configs" || cfg.BackupsRoot != "/backups" {
		t.Fatalf("roots=%q,%q", cfg.ConfigsRoot, cfg.BackupsRoot)
	}
	if cfg.AuthEnabled {
		t.Fatal("auth must default to disabled")
	}
	if cfg.MaxHeavyJobs != 1 {
		t.Fatalf("max heavy jobs=%d", cfg.MaxHeavyJobs)
	}
	if cfg.InsecureAllowRemote || cfg.AdminPasswordFile != "" || cfg.MinFreeBytes != 0 {
		t.Fatalf("unexpected opt-in defaults: insecure=%v password=%q min-free=%d", cfg.InsecureAllowRemote, cfg.AdminPasswordFile, cfg.MinFreeBytes)
	}
	if cfg.PresenceInterval != 10*time.Second {
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
	if cfg.WiFiBackend != config.WiFiBackendNetmuxd {
		t.Fatalf("Wi-Fi backend=%q", cfg.WiFiBackend)
	}
	if !cfg.WiFiPowerAssertion {
		t.Fatal("Wi-Fi power assertion must default to enabled")
	}
}

func TestLoadCustomValues(t *testing.T) {
	cfg, err := config.Load(mapEnv(map[string]string{
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
		"IOSBK_WIFI_BACKEND":            "usbmuxd2",
		"IOSBK_WIFI_POWER_ASSERTION":    "false",
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
	if !cfg.AuthEnabled || cfg.AdminPasswordFile != "/run/secrets/iosbackup-password" {
		t.Fatalf("auth=%v password file=%q", cfg.AuthEnabled, cfg.AdminPasswordFile)
	}
	if cfg.MaxHeavyJobs != 2 || cfg.MinFreeBytes != 1048576 {
		t.Fatalf("limits=%d,%d", cfg.MaxHeavyJobs, cfg.MinFreeBytes)
	}
	if cfg.PresenceInterval != 15*time.Second || cfg.DeviceDisconnectGrace != 45*time.Second || cfg.SchedulerInterval != time.Minute {
		t.Fatalf("intervals=%s,%s,%s", cfg.PresenceInterval, cfg.DeviceDisconnectGrace, cfg.SchedulerInterval)
	}
	if cfg.NetmuxdLogLevel != "debug" || cfg.WiFiBackend != config.WiFiBackendUSBMuxD2 {
		t.Fatalf("netmuxd=%q backend=%q", cfg.NetmuxdLogLevel, cfg.WiFiBackend)
	}
	if cfg.WiFiPowerAssertion {
		t.Fatal("Wi-Fi power assertion override should disable it")
	}
	wantCIDRs := []netip.Prefix{mustPrefix("192.168.0.0/16"), mustPrefix("fd00::/8")}
	if !reflect.DeepEqual(cfg.WebhookAllowCIDRs, wantCIDRs) {
		t.Fatalf("webhook allowlist=%v", cfg.WebhookAllowCIDRs)
	}
}

func TestLoadNormalizesValidatedValues(t *testing.T) {
	cfg, err := config.Load(mapEnv(map[string]string{
		"IOSBK_LISTEN_ADDR":             " 2001:0db8::1 ",
		"PORT":                          "65535",
		"IOSBK_CONFIGS_DIR":             "/srv/iosbackup/../config",
		"IOSBK_BACKUPS_DIR":             "/srv/iosbackup/../backup",
		"IOSBK_AUTH_ENABLED":            " TRUE ",
		"IOSBK_ADMIN_PASSWORD_FILE":     "/run/secrets/../iosbackup-password",
		"IOSBK_MAX_HEAVY_JOBS":          "2",
		"IOSBK_PRESENCE_INTERVAL":       "10m",
		"IOSBK_DEVICE_DISCONNECT_GRACE": "5s",
		"IOSBK_SCHEDULER_INTERVAL":      "24h",
		"IOSBK_NETMUXD_LOG_LEVEL":       " TRACE ",
		"IOSBK_WIFI_BACKEND":            " USBMUXD2 ",
		"IOSBK_WIFI_POWER_ASSERTION":    " FALSE ",
		"IOSBK_MIN_FREE_BYTES":          "18446744073709551615",
		"IOSBK_WEBHOOK_ALLOW_CIDRS":     " 192.168.1.23/16 , fd12:3456::1234/64 ",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddr != "2001:db8::1" || cfg.ServerAddress() != "[2001:db8::1]:65535" {
		t.Fatalf("normalized listen address=%q server=%q", cfg.ListenAddr, cfg.ServerAddress())
	}
	if cfg.ConfigsRoot != "/srv/config" || cfg.BackupsRoot != "/srv/backup" || cfg.AdminPasswordFile != "/run/iosbackup-password" {
		t.Fatalf("normalized paths=%q,%q,%q", cfg.ConfigsRoot, cfg.BackupsRoot, cfg.AdminPasswordFile)
	}
	if cfg.PresenceInterval != 10*time.Minute || cfg.DeviceDisconnectGrace != 5*time.Second || cfg.SchedulerInterval != 24*time.Hour {
		t.Fatalf("boundary durations=%s,%s,%s", cfg.PresenceInterval, cfg.DeviceDisconnectGrace, cfg.SchedulerInterval)
	}
	if cfg.NetmuxdLogLevel != "trace" || cfg.WiFiBackend != config.WiFiBackendUSBMuxD2 || cfg.WiFiPowerAssertion {
		t.Fatalf("normalized modes=%q,%q,%v", cfg.NetmuxdLogLevel, cfg.WiFiBackend, cfg.WiFiPowerAssertion)
	}
	if cfg.MinFreeBytes != math.MaxUint64 {
		t.Fatalf("min free bytes=%d", cfg.MinFreeBytes)
	}
	wantCIDRs := []netip.Prefix{mustPrefix("192.168.0.0/16"), mustPrefix("fd12:3456::/64")}
	if !reflect.DeepEqual(cfg.WebhookAllowCIDRs, wantCIDRs) {
		t.Fatalf("masked CIDRs=%v", cfg.WebhookAllowCIDRs)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []map[string]string{
		{"IOSBK_AUTH_ENABLED": "maybe"},
		{"IOSBK_INSECURE_ALLOW_REMOTE": "maybe"},
		{"PORT": "0"},
		{"PORT": "65536"},
		{"IOSBK_LISTEN_ADDR": "not-an-address"},
		{"IOSBK_MAX_HEAVY_JOBS": "0"},
		{"IOSBK_MAX_HEAVY_JOBS": "3"},
		{"IOSBK_CONFIGS_DIR": "relative"},
		{"IOSBK_CONFIGS_DIR": "/"},
		{"IOSBK_BACKUPS_DIR": "relative"},
		{"IOSBK_BACKUPS_DIR": "/"},
		{"IOSBK_ADMIN_PASSWORD_FILE": "relative"},
		{"IOSBK_ADMIN_PASSWORD_FILE": "/"},
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
		if _, err := config.Load(mapEnv(env)); err == nil {
			t.Fatalf("accepted %#v", env)
		}
	}
}

func TestLoadPreservesValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "bool", env: map[string]string{"IOSBK_AUTH_ENABLED": "maybe"}, want: "IOSBK_AUTH_ENABLED must be true or false"},
		{name: "port range", env: map[string]string{"PORT": "0"}, want: "PORT must be between 1 and 65535"},
		{name: "directory root", env: map[string]string{"IOSBK_CONFIGS_DIR": "/"}, want: "IOSBK_CONFIGS_DIR must not use the filesystem root as a directory"},
		{name: "file root", env: map[string]string{"IOSBK_ADMIN_PASSWORD_FILE": "/"}, want: "IOSBK_ADMIN_PASSWORD_FILE must not use the filesystem root as a file"},
		{name: "heavy jobs", env: map[string]string{"IOSBK_MAX_HEAVY_JOBS": "3"}, want: "IOSBK_MAX_HEAVY_JOBS must be between 1 and 2"},
		{name: "presence duration", env: map[string]string{"IOSBK_PRESENCE_INTERVAL": "500ms"}, want: "IOSBK_PRESENCE_INTERVAL must be between 1s and 10m0s"},
		{name: "log level", env: map[string]string{"IOSBK_NETMUXD_LOG_LEVEL": "verbose"}, want: "IOSBK_NETMUXD_LOG_LEVEL 只接受 error/warn/info/debug/trace: \"verbose\""},
		{name: "Wi-Fi backend", env: map[string]string{"IOSBK_WIFI_BACKEND": "other"}, want: "IOSBK_WIFI_BACKEND 只接受 netmuxd/usbmuxd2: \"other\""},
		{name: "public CIDR", env: map[string]string{"IOSBK_WEBHOOK_ALLOW_CIDRS": "0.0.0.0/0"}, want: "IOSBK_WEBHOOK_ALLOW_CIDRS 只接受私网 CIDR: \"0.0.0.0/0\""},
		{name: "remote acknowledgement", env: map[string]string{"IOSBK_LISTEN_ADDR": "0.0.0.0"}, want: "认证关闭时监听非 loopback 地址需要 IOSBK_INSECURE_ALLOW_REMOTE=true 显式确认"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(mapEnv(tt.env))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadRequiresAcknowledgementForUnauthenticatedNonLoopback(t *testing.T) {
	if _, err := config.Load(mapEnv(map[string]string{"IOSBK_LISTEN_ADDR": "0.0.0.0"})); err == nil {
		t.Fatal("unauthenticated non-loopback listener must require acknowledgement")
	}

	cfg, err := config.Load(mapEnv(map[string]string{
		"IOSBK_LISTEN_ADDR":           "0.0.0.0",
		"IOSBK_INSECURE_ALLOW_REMOTE": "true",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureAllowRemote {
		t.Fatal("explicit acknowledgement was not retained")
	}
}

func TestServerAddressJoinsIPv4AndIPv6(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Runtime
		want string
	}{
		{name: "IPv4", cfg: config.Runtime{ListenAddr: "127.0.0.1", Port: 8080}, want: "127.0.0.1:8080"},
		{name: "IPv6", cfg: config.Runtime{ListenAddr: "::1", Port: 9000}, want: "[::1]:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ServerAddress(); got != tt.want {
				t.Fatalf("ServerAddress()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultAndLoadDoNotShareWebhookAllowCIDRs(t *testing.T) {
	first := config.Default()
	second := config.Default()
	if first.WebhookAllowCIDRs != nil || second.WebhookAllowCIDRs != nil {
		t.Fatalf("default allowlists must remain nil: %v %v", first.WebhookAllowCIDRs, second.WebhookAllowCIDRs)
	}

	env := mapEnv(map[string]string{"IOSBK_WEBHOOK_ALLOW_CIDRS": "192.168.0.0/16,fd00::/8"})
	loadedA, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	loadedB, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	loadedA.WebhookAllowCIDRs[0] = mustPrefix("172.16.0.0/12")
	if loadedB.WebhookAllowCIDRs[0] == loadedA.WebhookAllowCIDRs[0] {
		t.Fatal("Load results share mutable allowlist backing storage")
	}
}

func TestNewPathsDerivesAllWritablePaths(t *testing.T) {
	paths := config.NewPaths(config.Runtime{
		ConfigsRoot: "/srv/config",
		BackupsRoot: "/srv/backup",
	})
	want := config.Paths{
		ConfigsRoot:            "/srv/config",
		BackupsRoot:            "/srv/backup",
		BackupBase:             "/srv/backup",
		BackupConfigFile:       "/srv/config/backup_configs.json",
		NotificationConfigFile: "/srv/config/notification_configs.json",
		SecretsFile:            "/srv/config/secrets.enc",
		AuthCredentialsFile:    "/srv/config/auth_credentials.json",
		CSRFSecretFile:         "/srv/config/csrf_secret.json",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%+v, want %+v", paths, want)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func mustPrefix(value string) netip.Prefix {
	return netip.MustParsePrefix(value)
}

func TestBackupTimeoutConfiguration(t *testing.T) {
	values := map[string]string{"IOSBK_BACKUP_PREPARATION_TIMEOUT": "45m", "IOSBK_BACKUP_INACTIVITY_TIMEOUT": "12m", "IOSBK_BACKUP_AUTHORIZATION_TIMEOUT": "7m"}
	cfg, err := config.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupPreparationTimeout != 45*time.Minute || cfg.BackupInactivityTimeout != 12*time.Minute || cfg.BackupAuthorizationTimeout != 7*time.Minute {
		t.Fatalf("timeouts not loaded: %+v", cfg)
	}
	for key := range values {
		for _, value := range []string{"0", "-1s", "25h", "bad"} {
			_, err := config.Load(func(k string) string {
				if k == key {
					return value
				}
				return ""
			})
			if err == nil {
				t.Errorf("%s=%s should fail startup validation", key, value)
			}
		}
	}
}
