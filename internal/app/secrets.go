package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"iosbackup/internal/persistence"
)

const secretsEnvKey = "IOSBK_SECRET_KEY"

var (
	errEncryptionUnavailable = persistence.ErrEncryptionUnavailable
	errPasswordNotFound      = persistence.ErrSecretNotFound
)

type secretStore = persistence.SecretStore
type aesSecretStore = persistence.AESSecretStore

func newAESSecretStore(key []byte, path string) (*aesSecretStore, error) {
	return persistence.NewAESSecretStore(key, path)
}
func newUnavailableSecretStore(path string) *aesSecretStore {
	return persistence.NewUnavailableSecretStore(path)
}

// initSecretStore 在任何设备/HTTP 服务启动前解析密钥并校验所有既有密文。
// 显式配置失败不得回退；没有密钥但已有密文时不得生成替代密钥。
func initSecretStore(cfg runtimeConfig, paths appPaths) (secretStore, error) {
	if cfg.SecretKey != "" && cfg.SecretKeyFile != "" {
		return nil, errors.New("IOSBK_SECRET_KEY and IOSBK_SECRET_KEY_FILE cannot both be configured")
	}
	if err := persistence.EnsurePrivateDirectory(paths.ConfigsRoot); err != nil {
		return nil, err
	}
	encoded := cfg.SecretKey
	source := secretsEnvKey
	if encoded == "" {
		keyPath := paths.SecretKeyFile
		private := true
		if cfg.SecretKeyFile != "" {
			keyPath = cfg.SecretKeyFile
			private = false
		}
		source = keyPath
		data, err := persistence.ReadSecretFile(keyPath, 4<<10, private)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) || !private {
				return nil, fmt.Errorf("read encryption master key file %s: %w", keyPath, err)
			}
			data, err = initializeDefaultSecretKey(paths)
			if err != nil {
				return nil, err
			}
		}
		encoded = strings.TrimSpace(string(data))
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must contain a standard Base64-encoded encryption master key of exactly 32 bytes", source)
	}
	store, err := newAESSecretStore(key, paths.SecretsFile)
	if err != nil {
		return nil, err
	}
	if err := store.Validate(); err != nil {
		return nil, fmt.Errorf("validate existing secret store; check the original key and data integrity: %w", err)
	}
	return store, nil
}

// initializeDefaultSecretKey 处理默认密钥首次读取缺失的初始化路径。
func initializeDefaultSecretKey(paths appPaths) ([]byte, error) {
	if _, err := os.Lstat(paths.SecretsFile); err == nil {
		// 首读缺失后，另一启动者可能已经发布密钥并写出密文。
		data, readErr := persistence.ReadSecretFile(paths.SecretKeyFile, 4<<10, true)
		if readErr == nil {
			return data, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("read concurrently initialized encryption master key: %w", readErr)
		}
		return nil, errors.New("secrets.enc exists but the original encryption master key is missing; restore secret_key or the original IOSBK_SECRET_KEY; a replacement key must not be generated")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect secret store: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate encryption master key: %w", err)
	}
	if _, err := persistence.CreatePrivateFile(paths.SecretKeyFile, []byte(base64.StdEncoding.EncodeToString(raw)+"\n")); err != nil {
		return nil, fmt.Errorf("save encryption master key: %w", err)
	}
	data, err := persistence.ReadSecretFile(paths.SecretKeyFile, 4<<10, true)
	if err != nil {
		return nil, fmt.Errorf("read encryption master key file: %w", err)
	}
	return data, nil
}
