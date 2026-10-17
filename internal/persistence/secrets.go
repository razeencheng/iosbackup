package persistence

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	// ErrEncryptionUnavailable 表示应用未提供有效的秘密存储密钥。
	ErrEncryptionUnavailable = errors.New("encryption unavailable: no valid secret-store master key is loaded")
	// ErrSecretNotFound 表示指定秘密条目不存在；通用方法复用此历史错误以保持兼容。
	ErrSecretNotFound = errors.New("backup password not found for this device")
)

const maxSecretsFileSize = 8 << 20

// SecretStore 是备份密码与通知秘密共用的加密存储接口。
type SecretStore interface {
	Available() bool
	GetSecret(key string) (string, error)
	SetSecret(key, value string) error
	DeleteSecret(key string) error
	GetBackupPassword(udid string) (string, error)
	SetBackupPassword(udid, password string) error
	DeleteBackupPassword(udid string) error
}

// AESSecretStore 使用 AES-256-GCM 把秘密写入 JSON map。
// 并发保护仅覆盖单个实例；同一路径的调用方应复用一个实例。
type AESSecretStore struct {
	mu        sync.Mutex
	gcm       cipher.AEAD
	path      string
	available bool
	writeFile func(string, []byte, os.FileMode) error
}

// NewAESSecretStore 用 32 字节密钥构造可用的秘密存储。
func NewAESSecretStore(key []byte, path string) (*AESSecretStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES key must be 32 bytes (AES-256); got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESSecretStore{
		gcm:       gcm,
		path:      path,
		available: true,
		writeFile: WriteFileAtomic,
	}, nil
}

// NewUnavailableSecretStore 构造缺密钥时的降级存储。
func NewUnavailableSecretStore(path string) *AESSecretStore {
	return &AESSecretStore{
		path:      path,
		available: false,
		writeFile: WriteFileAtomic,
	}
}

func (s *AESSecretStore) Available() bool { return s.available }

// Validate 在启动时验证全部密文，避免错误密钥写入后形成混合密钥存储。
func (s *AESSecretStore) Validate() error {
	if !s.available {
		return ErrEncryptionUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadLocked()
	if err != nil {
		return err
	}
	for key, encoded := range entries {
		if _, err := s.decrypt(key, encoded); err != nil {
			return err
		}
	}
	return nil
}

func (s *AESSecretStore) SetBackupPassword(udid, password string) error {
	return s.SetSecret(udid, password)
}

func (s *AESSecretStore) SetSecret(key, value string) error {
	if !s.available {
		return ErrEncryptionUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return err
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	sealed := s.gcm.Seal(nonce, nonce, []byte(value), []byte(key))
	entries[key] = base64.StdEncoding.EncodeToString(sealed)
	return s.saveLocked(entries)
}

func (s *AESSecretStore) GetBackupPassword(udid string) (string, error) {
	return s.GetSecret(udid)
}

func (s *AESSecretStore) GetSecret(key string) (string, error) {
	if !s.available {
		return "", ErrEncryptionUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return "", err
	}
	encoded, ok := entries[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return s.decrypt(key, encoded)
}

func (s *AESSecretStore) decrypt(key, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext as base64: %w", err)
	}
	nonceSize := s.gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext is corrupt: insufficient length")
	}
	plain, err := s.gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], []byte(key))
	if err != nil {
		return "", fmt.Errorf("decrypt ciphertext (key mismatch or corrupt data): %w", err)
	}
	return string(plain), nil
}

func (s *AESSecretStore) DeleteBackupPassword(udid string) error {
	return s.DeleteSecret(udid)
}

func (s *AESSecretStore) DeleteSecret(key string) error {
	if !s.available {
		return ErrEncryptionUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return err
	}
	delete(entries, key)
	return s.saveLocked(entries)
}

func (s *AESSecretStore) loadLocked() (map[string]string, error) {
	entries := make(map[string]string)
	data, err := ReadSecretFile(s.path, maxSecretsFileSize, true)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return entries, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("secret-store file is empty; data integrity cannot be verified")
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if entries == nil {
		return nil, fmt.Errorf("parse %s: content must be a JSON object", s.path)
	}
	return entries, nil
}

func (s *AESSecretStore) saveLocked(entries map[string]string) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxSecretsFileSize {
		return fmt.Errorf("secret-store content exceeds the size limit of %d bytes", maxSecretsFileSize)
	}
	return s.writeFile(s.path, data, 0o600)
}
