package app

import (
	"encoding/base64"
	"log"
	"os"

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

// InitSecretStore 从应用环境与路径策略初始化秘密存储。
//   - 未设置 → 降级（available=false）；若 secrets.enc 已存在则记 ERROR 日志
//   - 设置但非法（非 base64 / 非 32 字节）→ log.Fatal
func initSecretStore() secretStore {
	path := dirConfigs + "/secrets.enc"
	keyB64 := os.Getenv(secretsEnvKey)
	if keyB64 == "" {
		if _, err := os.Stat(path); err == nil {
			log.Printf("ERROR: 检测到 %s 但未配置 %s，已有备份密码不可读（其余功能照常）", path, secretsEnvKey)
		}
		return newUnavailableSecretStore(path)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		log.Fatalf("%s 不是合法 base64：%v（用 `openssl rand -base64 32` 生成）", secretsEnvKey, err)
	}
	store, err := newAESSecretStore(key, path)
	if err != nil {
		log.Fatalf("%s 无效：%v（须 base64 编码的恰 32 字节，用 `openssl rand -base64 32`）", secretsEnvKey, err)
	}
	return store
}
