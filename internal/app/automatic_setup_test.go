package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func automaticSetupConfig(t *testing.T) runtimeConfig {
	t.Helper()
	cfg := defaultRuntimeConfig()
	cfg.AuthEnabled = true
	cfg.ConfigsRoot = filepath.Join(t.TempDir(), "configs")
	cfg.BackupsRoot = t.TempDir()
	return cfg
}

func TestAutomaticAdminPasswordFileAndRestart(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	first, password, err := newAuthManager(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.ConfigsRoot, "admin_password"))
	if err != nil {
		t.Fatalf("首次启动应保存可找回的密码文件: %v", err)
	}
	if strings.TrimSpace(string(data)) != password || !first.AuthenticatePassword(password) {
		t.Fatal("密码文件必须是首次可用密码")
	}
	for _, p := range []string{filepath.Join(cfg.ConfigsRoot, "admin_password"), paths.AuthCredentialsFile} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("秘密文件权限不是0600: %v %v", info, err)
		}
	}
	second, generated, err := newAuthManager(cfg, paths)
	if err != nil || generated != "" || !second.AuthenticatePassword(password) {
		t.Fatalf("重启必须复用密码且不再次返回明文: %v", err)
	}
	digest, _ := os.ReadFile(paths.AuthCredentialsFile)
	if bytes.Contains(digest, []byte(password)) {
		t.Fatal("摘要文件包含明文")
	}
}

func TestAutomaticAdminPreservesLegacyDigest(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	if err := os.MkdirAll(cfg.ConfigsRoot, 0700); err != nil {
		t.Fatal(err)
	}
	password := "legacy-password-long-enough-for-test"
	sum := sha256.Sum256([]byte(password))
	data, _ := json.Marshal(authCredentialsFile{SchemaVersion: 1, PasswordSHA256: hex.EncodeToString(sum[:])})
	if err := os.WriteFile(paths.AuthCredentialsFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager, generated, err := newAuthManager(cfg, paths)
	if err != nil || generated != "" || !manager.AuthenticatePassword(password) {
		t.Fatalf("旧摘要必须保留: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cfg.ConfigsRoot, "admin_password")); !os.IsNotExist(err) {
		t.Fatal("旧摘要不得生成替代密码")
	}
	if err := os.WriteFile(filepath.Join(cfg.ConfigsRoot, "admin_password"), []byte("different-password-with-enough-entropy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newAuthManager(cfg, paths); err == nil {
		t.Fatal("旧摘要与默认密码冲突必须失败")
	}
}

func TestAutomaticAdminRecoversPasswordOnlyInitialization(t *testing.T) {
	cfg := automaticSetupConfig(t)
	if err := os.MkdirAll(cfg.ConfigsRoot, 0755); err != nil {
		t.Fatal(err)
	}
	password := "old-compose-password-with-enough-entropy"
	if err := os.WriteFile(filepath.Join(cfg.ConfigsRoot, "admin_password"), []byte(password+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, generated, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil || generated != "" || !manager.AuthenticatePassword(password) {
		t.Fatalf("必须接受旧Compose密码或中断后密码文件: %v", err)
	}
	info, _ := os.Stat(cfg.ConfigsRoot)
	if info.Mode().Perm() != 0700 {
		t.Fatal("配置目录应收紧至0700")
	}
	info, _ = os.Stat(filepath.Join(cfg.ConfigsRoot, "admin_password"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("默认密码文件应收紧至0600")
	}
}

func TestAutomaticAdminConcurrentInitialization(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	const count = 12
	managers := make([]*authManager, count)
	passwords := make([]string, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range managers {
		wg.Add(1)
		go func(i int) { defer wg.Done(); managers[i], passwords[i], errs[i] = newAuthManager(cfg, paths) }(i)
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(cfg.ConfigsRoot, "admin_password"))
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(data))
	generatedCount := 0
	for i := range managers {
		if errs[i] != nil || managers[i] == nil || !managers[i].AuthenticatePassword(password) {
			t.Fatalf("并发初始化必须接受同一获胜密码: %d %v", i, errs[i])
		}
		if passwords[i] != "" {
			generatedCount++
		}
	}
	if generatedCount != 1 {
		t.Fatalf("只允许创建者获得一次性明文, 得到%d", generatedCount)
	}
}

func TestAutomaticSecretStoreAvailableWithoutEnvironment(t *testing.T) {
	cfg := automaticSetupConfig(t)
	store, err := initSecretStore(cfg, newAppPaths(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !store.Available() {
		t.Fatal("新实例未配置密钥应自动生成可用存储")
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigsRoot, "secret_key")); err != nil {
		t.Fatal(err)
	}
}

func writeSetupFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticAdminRejectsInvalidDefaults(t *testing.T) {
	cases := []struct {
		name, file string
		data       []byte
	}{
		{"bad JSON", "auth_credentials.json", []byte("{")},
		{"unknown schema", "auth_credentials.json", []byte(`{"schema_version":2,"password_sha256":"00"}`)},
		{"bad digest", "auth_credentials.json", []byte(`{"schema_version":1,"password_sha256":"no"}`)},
		{"short digest", "auth_credentials.json", []byte(`{"schema_version":1,"password_sha256":"00"}`)},
		{"weak password", "admin_password", []byte("weak")},
		{"oversize password", "admin_password", bytes.Repeat([]byte("a"), 65537)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := automaticSetupConfig(t)
			path := filepath.Join(cfg.ConfigsRoot, tc.file)
			writeSetupFile(t, path, tc.data)
			if _, _, err := newAuthManager(cfg, newAppPaths(cfg)); err == nil {
				t.Fatal("损坏配置不得生成替代密码")
			}
			data, _ := os.ReadFile(path)
			if !bytes.Equal(data, tc.data) {
				t.Fatal("失败不得修改原配置")
			}
		})
	}
}

func TestAutomaticAdminExplicitFileWinsWithoutCopy(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	writeSetupFile(t, paths.AuthCredentialsFile, []byte("corrupt old ignored digest"))
	writeSetupFile(t, paths.AdminPasswordFile, []byte("corrupt old ignored password"))
	cfg.AdminPasswordFile = filepath.Join(t.TempDir(), "admin_password")
	password := "explicit-password-with-enough-entropy"
	writeSetupFile(t, cfg.AdminPasswordFile, []byte(password+"\r\n"))
	manager, generated, err := newAuthManager(cfg, paths)
	if err != nil || generated != "" || !manager.AuthenticatePassword(password) {
		t.Fatalf("显式文件应优先且不返回明文日志: %v", err)
	}
	if err := os.Remove(cfg.AdminPasswordFile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newAuthManager(cfg, paths); err == nil {
		t.Fatal("显式文件读取失败不能回退默认文件")
	}
}

func TestAutomaticAdminDisabledCreatesNoCredentials(t *testing.T) {
	cfg := automaticSetupConfig(t)
	cfg.AuthEnabled = false
	_, password, err := newAuthManager(cfg, newAppPaths(cfg))
	if err != nil || password != "" {
		t.Fatal("关闭认证不应生成密码")
	}
	if _, err := os.Lstat(cfg.ConfigsRoot); !os.IsNotExist(err) {
		t.Fatal("关闭认证不应建立密码目录或文件")
	}
}

func TestAutomaticDefaultsRejectUnsafeFiles(t *testing.T) {
	for _, file := range []string{"admin_password", "auth_credentials.json", "secret_key", "secrets.enc", "csrf_secret.json"} {
		for _, kind := range []string{"symlink", "dangling", "directory", "fifo"} {
			t.Run(file+"/"+kind, func(t *testing.T) {
				cfg := automaticSetupConfig(t)
				paths := newAppPaths(cfg)
				if err := os.MkdirAll(cfg.ConfigsRoot, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(cfg.ConfigsRoot, file)
				switch kind {
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					writeSetupFile(t, target, bytes.Repeat([]byte("x"), 32))
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				case "dangling":
					if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "fifo":
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				switch file {
				case "admin_password", "auth_credentials.json":
					_, _, err = newAuthManager(cfg, paths)
				case "secret_key":
					_, err = initSecretStore(cfg, paths)
				case "secrets.enc":
					cfg.SecretKey = base64.StdEncoding.EncodeToString(testKey32())
					_, err = initSecretStore(cfg, paths)
				case "csrf_secret.json":
					_, err = newCSRFManager(path)
				}
				if err == nil {
					t.Fatal("不安全默认文件必须失败，不能阻塞或覆盖")
				}
			})
		}
	}
}

func TestAutomaticSecretKeyRestartAndInterruptedInitialization(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	store, err := initSecretStore(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	keyData, err := os.ReadFile(paths.SecretKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyData)))
	if err != nil || len(key) != 32 {
		t.Fatal("默认主密钥必须是Base64编码32字节")
	}
	if info, err := os.Stat(paths.SecretKeyFile); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("主密钥权限必须0600")
	}
	// 首次创建 key 但未写密文时重启也不得轮换 key。
	restart, err := initSecretStore(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("notification/example", "sensitive-value"); err != nil {
		t.Fatal(err)
	}
	if got, err := restart.GetSecret("notification/example"); err != nil || got != "sensitive-value" {
		t.Fatalf("中断后必须使用同一密钥: %v", err)
	}
	third, err := initSecretStore(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := third.GetSecret("notification/example"); err != nil || got != "sensitive-value" {
		t.Fatalf("重启必须解密旧数据: %v", err)
	}
	keyAfter, _ := os.ReadFile(paths.SecretKeyFile)
	if !bytes.Equal(keyData, keyAfter) {
		t.Fatal("重启不得重写密钥")
	}
	other := automaticSetupConfig(t)
	if _, err := initSecretStore(other, newAppPaths(other)); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := os.ReadFile(newAppPaths(other).SecretKeyFile)
	if bytes.Equal(keyData, otherKey) {
		t.Fatal("独立安装必须使用不同随机密钥")
	}
}

func TestAutomaticSecretKeyExplicitSourcesAndConflict(t *testing.T) {
	for _, source := range []string{"env", "file"} {
		t.Run(source, func(t *testing.T) {
			cfg := automaticSetupConfig(t)
			paths := newAppPaths(cfg)
			key := base64.StdEncoding.EncodeToString(testKey32())
			if source == "env" {
				cfg.SecretKey = key
			} else {
				cfg.SecretKeyFile = filepath.Join(t.TempDir(), "key")
				writeSetupFile(t, cfg.SecretKeyFile, []byte(key+"\n"))
			}
			original, err := newAESSecretStore(testKey32(), paths.SecretsFile)
			if err != nil {
				t.Fatal(err)
			}
			if err := original.SetSecret("legacy", "legacy-value"); err != nil {
				t.Fatal(err)
			}
			store, err := initSecretStore(cfg, paths)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := store.GetSecret("legacy"); err != nil || got != "legacy-value" {
				t.Fatal("显式旧密钥应继续可用")
			}
			if _, err := os.Lstat(paths.SecretKeyFile); !os.IsNotExist(err) {
				t.Fatal("显式密钥不得自动复制落盘")
			}
			// 显式源优先于无效默认文件。
			writeSetupFile(t, paths.SecretKeyFile, []byte("ignored-invalid-default"))
			if _, err := initSecretStore(cfg, paths); err != nil {
				t.Fatalf("显式配置应忽略默认密钥: %v", err)
			}
		})
	}
	cfg := automaticSetupConfig(t)
	cfg.SecretKey = "configured"
	cfg.SecretKeyFile = "/missing"
	if _, err := initSecretStore(cfg, newAppPaths(cfg)); err == nil || !strings.Contains(err.Error(), "不能同时配置") {
		t.Fatal("双重显式源必须清楚报错")
	}
}

func TestAutomaticSecretKeyInvalidSourcesNeverFallback(t *testing.T) {
	for _, source := range []string{"env", "file", "default"} {
		for _, value := range []string{"not-base64", "   ", base64.StdEncoding.EncodeToString(make([]byte, 31)), ""} {
			if source == "env" && value == "" {
				continue
			}
			t.Run(source+"/"+value, func(t *testing.T) {
				cfg := automaticSetupConfig(t)
				paths := newAppPaths(cfg)
				switch source {
				case "env":
					cfg.SecretKey = value
				case "file":
					cfg.SecretKeyFile = filepath.Join(t.TempDir(), "key")
					writeSetupFile(t, cfg.SecretKeyFile, []byte(value))
				case "default":
					writeSetupFile(t, paths.SecretKeyFile, []byte(value))
				}
				if _, err := initSecretStore(cfg, paths); err == nil {
					t.Fatal("非法非空配置/坏文件不能回退自动生成")
				}
				if source != "default" {
					if _, err := os.Lstat(paths.SecretKeyFile); !os.IsNotExist(err) {
						t.Fatal("显式源失败不能落盘替代密钥")
					}
				}
			})
		}
	}
	cfg := automaticSetupConfig(t)
	cfg.SecretKeyFile = filepath.Join(t.TempDir(), "missing")
	if _, err := initSecretStore(cfg, newAppPaths(cfg)); err == nil {
		t.Fatal("显式密钥文件缺失必须失败")
	}
	cfg.SecretKeyFile = filepath.Join(t.TempDir(), "oversize")
	writeSetupFile(t, cfg.SecretKeyFile, bytes.Repeat([]byte("a"), 4097))
	if _, err := initSecretStore(cfg, newAppPaths(cfg)); err == nil {
		t.Fatal("显式密钥文件必须限制读取大小")
	}
}

func TestAutomaticSecretKeyMissingOrWrongPreservesCiphertext(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	original, err := newAESSecretStore(testKey32(), paths.SecretsFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.SetSecret("legacy-secret", "must-preserve"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.SecretsFile)
	if _, err := initSecretStore(cfg, paths); err == nil {
		t.Fatal("已有密文缺少原密钥必须失败")
	}
	if _, err := os.Lstat(paths.SecretKeyFile); !os.IsNotExist(err) {
		t.Fatal("不能生成替代密钥")
	}
	cfg.SecretKey = base64.StdEncoding.EncodeToString(testKey32Other())
	if _, err := initSecretStore(cfg, paths); err == nil {
		t.Fatal("错误密钥必须在启动时失败")
	}
	after, _ := os.ReadFile(paths.SecretsFile)
	if !bytes.Equal(before, after) {
		t.Fatal("初始化失败不得更改密文")
	}
}

func TestAutomaticSecretStoreRejectsCorruptCiphertextAtStartup(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("{"), []byte("null"), []byte(`{"first":"!"}`), []byte(`{"first":"eA=="}`), bytes.Repeat([]byte("x"), 8<<20+1)} {
		t.Run(strconv.Itoa(len(data)), func(t *testing.T) {
			cfg := automaticSetupConfig(t)
			paths := newAppPaths(cfg)
			cfg.SecretKey = base64.StdEncoding.EncodeToString(testKey32())
			writeSetupFile(t, paths.SecretsFile, data)
			if _, err := initSecretStore(cfg, paths); err == nil {
				t.Fatal("损坏/超长密文必须阻止启动")
			}
		})
	}
}

func TestAutomaticSecretKeyConcurrentInitialization(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	const count = 12
	stores := make([]secretStore, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) { defer wg.Done(); stores[i], errs[i] = initSecretStore(cfg, paths) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := stores[0].SetSecret("entry", "same-key-value"); err != nil {
		t.Fatal(err)
	}
	for _, store := range stores {
		if value, err := store.GetSecret("entry"); err != nil || value != "same-key-value" {
			t.Fatalf("所有初始化必须复用同一获胜密钥: %v", err)
		}
	}
}

func TestAutomaticSecurityLoginRestartAndOneTimeLogs(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	app := newApplicationWithRuntime(context.Background(), cfg)
	if err := app.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	passwordData, _ := os.ReadFile(paths.AdminPasswordFile)
	password := strings.TrimSpace(string(passwordData))
	key, _ := os.ReadFile(paths.SecretKeyFile)
	if strings.Count(logs.String(), password) != 1 || strings.Contains(logs.String(), strings.TrimSpace(string(key))) {
		t.Fatal("首次只显示管理员密码一次，不得打印主密钥")
	}
	handler := app.setupRoutes()
	if got := performRequest(handler, http.MethodGet, "/healthz", nil, nil).Code; got != http.StatusOK {
		t.Fatal("健康检查应可访问")
	}
	if got := performRequest(handler, http.MethodGet, "/api/version", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatal("新实例API必须受认证保护")
	}
	login := passwordLoginRequest(handler, password, app.csrfManager.Token(), "/")
	if login.Code != http.StatusSeeOther {
		t.Fatalf("初始文件密码应可登录: %d", login.Code)
	}
	cookie := findSessionCookie(t, login)
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatal("初始登录会话应可访问API")
	}
	if err := app.secretStore.SetSecret("saved", "persisted"); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	restart := newApplicationWithRuntime(context.Background(), cfg)
	if err := restart.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), strings.TrimSpace(string(key))) {
		t.Fatal("重启日志不得重复明文")
	}
	if !strings.Contains(logs.String(), paths.AdminPasswordFile) {
		t.Fatal("重启应提示密码文件位置")
	}
	if !restart.authManager.AuthenticatePassword(password) || app.csrfManager.Token() != restart.csrfManager.Token() {
		t.Fatal("重启应复用密码和CSRF密钥")
	}
	if restart.authManager.validateSession(cookie.Value, time.Now()) {
		t.Fatal("重启后旧会话应失效")
	}
	if value, err := restart.secretStore.GetSecret("saved"); err != nil || value != "persisted" {
		t.Fatal("重启应能解密持久化数据")
	}
}

func TestAutomaticSecurityFailsBeforeGeneratingPasswordForMissingKey(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	writeSetupFile(t, paths.SecretsFile, []byte("{}"))
	app := newApplicationWithRuntime(context.Background(), cfg)
	if err := app.initializeSecurity(); err == nil {
		t.Fatal("缺少原密钥必须拒绝初始化")
	}
	for _, path := range []string{paths.SecretKeyFile, paths.AdminPasswordFile, paths.AuthCredentialsFile, paths.CSRFSecretFile} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("失败不应创建替代秘密: %s", path)
		}
	}
}

func TestAutomaticSecretKeyMissingReadCanRecoverConcurrentWinner(t *testing.T) {
	cfg := automaticSetupConfig(t)
	paths := newAppPaths(cfg)
	// 模拟首次读取报不存在后，另一初始化者已发布密钥并开始保存秘密。
	winner, err := initSecretStore(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := winner.SetSecret("entry", "winner-value"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.SecretKeyFile)
	data, err := initializeDefaultSecretKey(paths)
	if err != nil || !bytes.Equal(data, before) {
		t.Fatalf("应重新读取并使用并发赢家密钥，而不是误报密钥丢失: %v", err)
	}
}

func TestAutomaticCSRFRejectsCorruptionAndConcurrentInitialization(t *testing.T) {
	for _, data := range []string{`{`, `{"schema_version":2,"secret":"invalid"}`, `{"schema_version":1,"secret":"invalid"}`} {
		path := filepath.Join(t.TempDir(), "csrf_secret.json")
		writeSetupFile(t, path, []byte(data))
		if _, err := newCSRFManager(path); err == nil {
			t.Fatal("错误CSRF文件不能被重新生成覆盖")
		}
	}
	path := filepath.Join(t.TempDir(), "csrf_secret.json")
	const count = 8
	managers := make([]*csrfManager, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range managers {
		wg.Add(1)
		go func(i int) { defer wg.Done(); managers[i], errs[i] = newCSRFManager(path) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		if managers[i].Token() != managers[0].Token() {
			t.Fatal("并发CSRF初始化必须复用同一密钥")
		}
	}
}

func TestAutomaticSecurityLogsLegacyExplicitAndDisabledModes(t *testing.T) {
	for _, mode := range []string{"legacy", "explicit", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			cfg := automaticSetupConfig(t)
			paths := newAppPaths(cfg)
			password := "preserved-password-that-must-not-be-logged"
			switch mode {
			case "legacy":
				sum := sha256.Sum256([]byte(password))
				data, _ := json.Marshal(authCredentialsFile{SchemaVersion: 1, PasswordSHA256: hex.EncodeToString(sum[:])})
				writeSetupFile(t, paths.AuthCredentialsFile, data)
			case "explicit":
				cfg.AdminPasswordFile = filepath.Join(t.TempDir(), "external-password")
				writeSetupFile(t, cfg.AdminPasswordFile, []byte(password))
			case "disabled":
				cfg.AuthEnabled = false
			}
			var output bytes.Buffer
			old := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(old)
			app := newApplicationWithRuntime(context.Background(), cfg)
			if err := app.initializeSecurity(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), password) || strings.Contains(output.String(), "首次生成") {
				t.Fatal("已有认证或关闭认证模式不应打印秘密")
			}
			if _, err := os.Lstat(paths.AdminPasswordFile); !os.IsNotExist(err) {
				t.Fatal("不得生成替代管理员密码")
			}
		})
	}
}

func TestAutomaticSecurityFailedSetupDoesNotLogGeneratedPassword(t *testing.T) {
	for _, file := range []string{"admin_password", "csrf_secret.json"} {
		t.Run(file, func(t *testing.T) {
			cfg := automaticSetupConfig(t)
			paths := newAppPaths(cfg)
			writeSetupFile(t, filepath.Join(cfg.ConfigsRoot, file), []byte("broken"))
			var output bytes.Buffer
			old := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(old)
			app := newApplicationWithRuntime(context.Background(), cfg)
			if err := app.initializeSecurity(); err == nil {
				t.Fatal("损坏认证或CSRF必须返回启动错误")
			}
			if output.Len() != 0 {
				t.Fatal("初始化未成功不得输出首次设置成功日志")
			}
			if file == "csrf_secret.json" {
				password, _ := os.ReadFile(paths.AdminPasswordFile)
				if len(password) < 24 {
					t.Fatal("初始化中断的密码仍应可从文件找回")
				}
			}
		})
	}
}

func TestAutomaticExplicitUnreadableSecretsFail(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root可以读取mode000文件")
	}
	cfg := automaticSetupConfig(t)
	cfg.AdminPasswordFile = filepath.Join(t.TempDir(), "password")
	writeSetupFile(t, cfg.AdminPasswordFile, bytes.Repeat([]byte("x"), 32))
	if err := os.Chmod(cfg.AdminPasswordFile, 0000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newAuthManager(cfg, newAppPaths(cfg)); err == nil {
		t.Fatal("不可读显式密码文件必须失败")
	}
	cfg.SecretKeyFile = cfg.AdminPasswordFile
	if _, err := initSecretStore(cfg, newAppPaths(cfg)); err == nil {
		t.Fatal("不可读显式密钥文件必须失败")
	}
}
