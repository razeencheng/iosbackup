package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthDefaultsDisabled(t *testing.T) {
	cfg := defaultRuntimeConfig()
	manager, generated, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() || generated != "" {
		t.Fatal("认证默认应关闭且不生成密码")
	}
}

func TestAuthEnabledUsesIOSBackupUsername(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot = t.TempDir()
	manager, generated, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if generated == "" || !manager.Authenticate("iosbackup", generated) {
		t.Fatal("启用认证时应使用 iosbackup 和首次生成密码")
	}
}

func TestLegacyUsernameSettingCannotCreateMultipleLoginIdentities(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"IOSBK_AUTH_ENABLED":   "true",
		"IOSBK_ADMIN_USERNAME": "backup-admin",
		"IOSBK_CONFIGS_DIR":    t.TempDir(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	manager, generated, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Authenticate("iosbackup", generated) {
		t.Fatal("对外 Basic Auth 应始终使用固定兼容标识 iosbackup")
	}
	if manager.Authenticate("backup-admin", generated) {
		t.Fatal("旧的用户名环境变量不应再创建另一个登录身份")
	}
}

func TestGeneratedPasswordPersistsAcrossRestart(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot = t.TempDir()
	paths := newAppPaths(cfg)
	first, password, err := newAuthManager(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	second, generatedAgain, err := newAuthManager(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	if generatedAgain != "" || !first.Authenticate("iosbackup", password) || !second.Authenticate("iosbackup", password) {
		t.Fatal("重启后必须继续接受首次生成的密码且不得再次输出明文")
	}
	data, err := os.ReadFile(filepath.Join(cfg.ConfigsRoot, "auth_credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(password)) {
		t.Fatal("凭据文件不得包含明文密码")
	}
	info, err := os.Stat(filepath.Join(cfg.ConfigsRoot, "auth_credentials.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("凭据文件权限必须为 0600: %v, %v", info, err)
	}
}

func TestAuthPasswordFileRejectsWeakPassword(t *testing.T) {
	passwordPath := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordPath, []byte("short-password"), 0400); err != nil {
		t.Fatal(err)
	}
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot = t.TempDir()
	cfg.AdminPasswordFile = passwordPath
	if _, _, err := newAuthManager(cfg, newAppPaths(cfg)); err == nil {
		t.Fatal("密码文件不得把弱人工密码引入快速 SHA-256 校验设计")
	}
}

func TestDifferentInstallationsGenerateDifferentPasswords(t *testing.T) {
	cfgA, cfgB := defaultRuntimeConfig(), defaultRuntimeConfig()
	cfgA.AuthEnabled, cfgB.AuthEnabled = true, true
	cfgA.ConfigsRoot, cfgB.ConfigsRoot = t.TempDir(), t.TempDir()
	_, passwordA, err := newAuthManager(cfgA, newAppPaths(cfgA))
	if err != nil {
		t.Fatal(err)
	}
	_, passwordB, err := newAuthManager(cfgB, newAppPaths(cfgB))
	if err != nil {
		t.Fatal(err)
	}
	if passwordA == passwordB || len(passwordA) < 32 || len(passwordB) < 32 {
		t.Fatal("不同安装必须生成不同且至少 192 bit 的密码")
	}
}

func TestAuthPasswordFileDoesNotPersistOrLogPlaintext(t *testing.T) {
	passwordPath := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordPath, []byte("file-password-with-high-entropy\n"), 0400); err != nil {
		t.Fatal(err)
	}
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot = t.TempDir()
	cfg.AdminPasswordFile = passwordPath
	manager, generated, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if generated != "" || !manager.Authenticate("iosbackup", "file-password-with-high-entropy") {
		t.Fatal("密码文件应直接作为认证来源，且不得返回明文用于日志")
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigsRoot, "auth_credentials.json")); !os.IsNotExist(err) {
		t.Fatal("密码文件模式不得复制摘要到安装配置")
	}
}

func TestAuthDisabledHasNoPersistentBanner(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.ConfigsRoot, cfg.BackupsRoot = t.TempDir(), t.TempDir()
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.secretStore = newUnavailableSecretStore(filepath.Join(cfg.ConfigsRoot, "secrets.enc"))
	rr := performRequest(app.setupRoutes(), "GET", "/", nil, nil)
	if rr.Code != 200 {
		t.Fatalf("首页应正常访问，得到 %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "认证已关闭") || strings.Contains(strings.ToLower(rr.Body.String()), "authentication disabled") {
		t.Fatal("认证关闭时不应持续显示横幅")
	}
}
