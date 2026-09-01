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
	ErrEncryptionUnavailable = errors.New("加密功能不可用：未配置有效的 IOSBK_SECRET_KEY")
	// ErrSecretNotFound 表示指定秘密条目不存在；通用方法复用此历史错误以保持兼容。
	ErrSecretNotFound = errors.New("未找到该设备的备份密码")
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
		return nil, fmt.Errorf("AES 密钥须为 32 字节（AES-256），实际 %d 字节", len(key))
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
		return fmt.Errorf("生成 nonce 失败: %w", err)
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
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	nonceSize := s.gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("密文损坏：长度不足")
	}
	plain, err := s.gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], []byte(key))
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥不匹配或数据损坏）: %w", err)
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
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSecretsFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSecretsFileSize {
		return nil, fmt.Errorf("秘密存储文件超过大小上限 %d 字节", maxSecretsFileSize)
	}
	if len(data) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", s.path, err)
	}
	if entries == nil {
		return nil, fmt.Errorf("解析 %s 失败：内容必须为 JSON 对象", s.path)
	}
	return entries, nil
}

func (s *AESSecretStore) saveLocked(entries map[string]string) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxSecretsFileSize {
		return fmt.Errorf("秘密存储内容超过大小上限 %d 字节", maxSecretsFileSize)
	}
	return s.writeFile(s.path, data, 0o600)
}
