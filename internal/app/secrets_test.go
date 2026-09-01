package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testKey32 返回确定性的 32 字节密钥（测试用）。
func testKey32() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

// testKey32Other 返回另一把不同的 32 字节密钥。
func testKey32Other() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(255 - i)
	}
	return key
}

func tempSecretsPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "secrets.enc")
}

// rawEntry 读取 secrets.enc 中某 udid 的原始 base64 密文。
func rawEntry(t *testing.T, path, udid string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析 secrets.enc 失败: %v", err)
	}
	return m[udid]
}

func TestSecretStoreRoundtrip(t *testing.T) {
	s, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatalf("构造 store 失败: %v", err)
	}
	if !s.Available() {
		t.Fatal("有密钥时 Available() 应为 true")
	}
	if err := s.SetBackupPassword("UDID-1", "hunter2"); err != nil {
		t.Fatalf("SetBackupPassword 失败: %v", err)
	}
	got, err := s.GetBackupPassword("UDID-1")
	if err != nil {
		t.Fatalf("GetBackupPassword 失败: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("往返应得到 hunter2，得到 %q", got)
	}
}

func TestSecretStoreWrongKeySize(t *testing.T) {
	if _, err := newAESSecretStore(make([]byte, 16), tempSecretsPath(t)); err == nil {
		t.Error("16 字节密钥应报错（须 32 字节 AES-256）")
	}
}

func TestSecretStoreRandomNonce(t *testing.T) {
	path := tempSecretsPath(t)
	s, err := newAESSecretStore(testKey32(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackupPassword("U", "same-pw"); err != nil {
		t.Fatal(err)
	}
	c1 := rawEntry(t, path, "U")
	if err := s.SetBackupPassword("U", "same-pw"); err != nil {
		t.Fatal(err)
	}
	c2 := rawEntry(t, path, "U")
	if c1 == c2 {
		t.Error("nonce 应每次随机，相同明文两次密文应不同")
	}
	got, err := s.GetBackupPassword("U")
	if err != nil || got != "same-pw" {
		t.Errorf("重设后仍应解回 same-pw，得到 %q err=%v", got, err)
	}
}

func TestSecretStoreNoPlaintextOnDisk(t *testing.T) {
	path := tempSecretsPath(t)
	s, err := newAESSecretStore(testKey32(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackupPassword("U", "TopSecretPlain"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("TopSecretPlain")) {
		t.Error("明文密码不应出现在 secrets.enc 中")
	}
}

func TestSecretStorePersistence(t *testing.T) {
	path := tempSecretsPath(t)
	key := testKey32()
	s1, err := newAESSecretStore(key, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SetBackupPassword("U", "pw-persist"); err != nil {
		t.Fatal(err)
	}
	// 新建 store（同密钥同路径）应能读回
	s2, err := newAESSecretStore(key, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.GetBackupPassword("U")
	if err != nil || got != "pw-persist" {
		t.Errorf("持久化后应读回 pw-persist，得到 %q err=%v", got, err)
	}
}

func TestSecretStoreWrongKeyCannotDecrypt(t *testing.T) {
	path := tempSecretsPath(t)
	s1, err := newAESSecretStore(testKey32(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.SetBackupPassword("U", "pw"); err != nil {
		t.Fatal(err)
	}
	s2, err := newAESSecretStore(testKey32Other(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.GetBackupPassword("U"); err == nil {
		t.Error("用不同密钥解密应失败（GCM 认证）")
	}
}

func TestSecretStoreDelete(t *testing.T) {
	path := tempSecretsPath(t)
	s, err := newAESSecretStore(testKey32(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackupPassword("U", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupPassword("U"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBackupPassword("U"); err == nil {
		t.Error("删除后 Get 应报未找到")
	}
}

func TestSecretStoreUnavailable(t *testing.T) {
	s := newUnavailableSecretStore(tempSecretsPath(t))
	if s.Available() {
		t.Error("无密钥时 Available() 应为 false")
	}
	if err := s.SetBackupPassword("U", "pw"); err == nil {
		t.Error("不可用时 Set 应明确报错")
	}
	if _, err := s.GetBackupPassword("U"); err == nil {
		t.Error("不可用时 Get 应明确报错")
	}
}

func TestSecretStoreFilePerm(t *testing.T) {
	path := tempSecretsPath(t)
	s, err := newAESSecretStore(testKey32(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBackupPassword("U", "pw"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secrets.enc 应为 0600，得到 %o", perm)
	}
}
