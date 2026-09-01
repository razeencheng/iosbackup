package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	authCredentialsSchemaVersion = 1
	authSessionCookieName        = "iosbk_session"
	authSessionLifetime          = 12 * time.Hour
	maxAuthSessions              = 128
	basicAuthUsername            = "iosbackup"
)

type authManager struct {
	enabled      bool
	usernameHash [32]byte
	passwordHash [32]byte
	sessionMu    sync.Mutex
	sessions     map[[32]byte]time.Time
}

func newBootstrapAuthManager(cfg runtimeConfig) *authManager {
	manager := &authManager{
		enabled:  cfg.AuthEnabled,
		sessions: make(map[[32]byte]time.Time),
	}
	manager.usernameHash = sha256.Sum256([]byte(basicAuthUsername))
	return manager
}

type authCredentialsFile struct {
	SchemaVersion  int    `json:"schema_version"`
	PasswordSHA256 string `json:"password_sha256"`
}

func newAuthManager(cfg runtimeConfig, paths appPaths) (*authManager, string, error) {
	manager := newBootstrapAuthManager(cfg)
	if !cfg.AuthEnabled {
		return manager, "", nil
	}

	if cfg.AdminPasswordFile != "" {
		data, err := os.ReadFile(cfg.AdminPasswordFile)
		if err != nil {
			return nil, "", fmt.Errorf("读取管理员密码文件: %w", err)
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if len(password) < 24 {
			return nil, "", errors.New("管理员密码文件必须提供至少 24 个字符的高熵密码")
		}
		manager.passwordHash = sha256.Sum256([]byte(password))
		return manager, "", nil
	}

	data, err := os.ReadFile(paths.AuthCredentialsFile)
	if err == nil {
		var stored authCredentialsFile
		if err := decodeStrictJSON(data, &stored); err != nil {
			return nil, "", fmt.Errorf("解析认证凭据: %w", err)
		}
		if stored.SchemaVersion != authCredentialsSchemaVersion {
			return nil, "", fmt.Errorf("不支持的认证凭据 schema_version: %d", stored.SchemaVersion)
		}
		digest, err := hex.DecodeString(stored.PasswordSHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, "", errors.New("认证凭据摘要无效")
		}
		copy(manager.passwordHash[:], digest)
		return manager, "", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("读取认证凭据: %w", err)
	}

	randomPassword := make([]byte, 32)
	if _, err := rand.Read(randomPassword); err != nil {
		return nil, "", fmt.Errorf("生成管理员密码: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(randomPassword)
	manager.passwordHash = sha256.Sum256([]byte(password))
	stored := authCredentialsFile{
		SchemaVersion:  authCredentialsSchemaVersion,
		PasswordSHA256: hex.EncodeToString(manager.passwordHash[:]),
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := writeFileAtomic(paths.AuthCredentialsFile, encoded, 0600); err != nil {
		return nil, "", fmt.Errorf("保存认证凭据: %w", err)
	}
	return manager, password, nil
}

func (a *authManager) Enabled() bool { return a != nil && a.enabled }

func (a *authManager) Authenticate(username, password string) bool {
	if !a.Enabled() {
		return true
	}
	usernameHash := sha256.Sum256([]byte(username))
	usernameOK := subtle.ConstantTimeCompare(usernameHash[:], a.usernameHash[:])
	passwordHash := sha256.Sum256([]byte(password))
	passwordOK := subtle.ConstantTimeCompare(passwordHash[:], a.passwordHash[:])
	return usernameOK&passwordOK == 1
}

func (a *authManager) AuthenticatePassword(password string) bool {
	if !a.Enabled() {
		return true
	}
	passwordHash := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(passwordHash[:], a.passwordHash[:]) == 1
}

func (a *authManager) createSession(now time.Time) (string, error) {
	if !a.Enabled() {
		return "", errors.New("认证未启用")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成登录会话: %w", err)
	}
	key := sha256.Sum256(raw)
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.sessions == nil {
		a.sessions = make(map[[32]byte]time.Time)
	}
	for existing, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, existing)
		}
	}
	if len(a.sessions) >= maxAuthSessions {
		var oldestKey [32]byte
		var oldestExpiry time.Time
		for existing, expires := range a.sessions {
			if oldestExpiry.IsZero() || expires.Before(oldestExpiry) {
				oldestKey, oldestExpiry = existing, expires
			}
		}
		delete(a.sessions, oldestKey)
	}
	a.sessions[key] = now.Add(authSessionLifetime)
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func sessionKey(token string) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

func (a *authManager) validateSession(token string, now time.Time) bool {
	if !a.Enabled() {
		return true
	}
	key, ok := sessionKey(token)
	if !ok {
		return false
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	expires, exists := a.sessions[key]
	if !exists {
		return false
	}
	if !now.Before(expires) {
		delete(a.sessions, key)
		return false
	}
	return true
}

func (a *authManager) revokeSession(token string) {
	key, ok := sessionKey(token)
	if !ok || a == nil {
		return
	}
	a.sessionMu.Lock()
	delete(a.sessions, key)
	a.sessionMu.Unlock()
}

type csrfManager struct {
	token string
}

type csrfSecretFile struct {
	SchemaVersion int    `json:"schema_version"`
	Secret        string `json:"secret"`
}

func newCSRFManager(path string) (*csrfManager, error) {
	var secret []byte
	data, err := os.ReadFile(path)
	if err == nil {
		var stored csrfSecretFile
		if err := decodeStrictJSON(data, &stored); err != nil {
			return nil, fmt.Errorf("解析 CSRF secret: %w", err)
		}
		if stored.SchemaVersion != 1 {
			return nil, fmt.Errorf("不支持的 CSRF schema_version: %d", stored.SchemaVersion)
		}
		secret, err = base64.RawURLEncoding.DecodeString(stored.Secret)
		if err != nil || len(secret) != 32 {
			return nil, errors.New("CSRF secret 无效")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		stored := csrfSecretFile{SchemaVersion: 1, Secret: base64.RawURLEncoding.EncodeToString(secret)}
		encoded, err := json.MarshalIndent(stored, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(path, encoded, 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return csrfManagerFromSecret(secret), nil
}

func newEphemeralCSRFManager() *csrfManager {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(fmt.Sprintf("生成 CSRF secret 失败: %v", err))
	}
	return csrfManagerFromSecret(secret)
}

func csrfManagerFromSecret(secret []byte) *csrfManager {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("iosbackup-browser-csrf-v1"))
	return &csrfManager{token: base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
}

func (c *csrfManager) Token() string {
	if c == nil {
		return ""
	}
	return c.token
}
