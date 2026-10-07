package persistence

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testedSecretsFileLimit = 8 << 20

func TestAESSecretStoreRequiresExactly32ByteKey(t *testing.T) {
	var nilKey []byte
	if _, err := NewAESSecretStore(nilKey, secretPath(t)); err == nil {
		t.Fatal("nil 密钥应失败")
	}
	for _, size := range []int{0, 16, 31, 33, 64} {
		if _, err := NewAESSecretStore(make([]byte, size), secretPath(t)); err == nil {
			t.Fatalf("%d 字节密钥应失败", size)
		}
	}
	if _, err := NewAESSecretStore(testKey(1), secretPath(t)); err != nil {
		t.Fatalf("32 字节密钥应成功: %v", err)
	}
}

func TestAESSecretStoreSupportsEmptyEntryKeyAndValue(t *testing.T) {
	store := mustStore(t, testKey(12), secretPath(t))
	if err := store.SetSecret("", ""); err != nil {
		t.Fatalf("空键和空值是既有格式允许的字符串，应可保存: %v", err)
	}
	if got, err := store.GetSecret(""); err != nil || got != "" {
		t.Fatalf("空键和空值往返失败: got=%q err=%v", got, err)
	}
	if err := store.DeleteSecret(""); err != nil {
		t.Fatalf("删除空键失败: %v", err)
	}
	if _, err := store.GetSecret(""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("删除空键后应返回 ErrSecretNotFound，得到 %v", err)
	}
}

func TestAESSecretStoreRoundTripsGenericAndBackupAliases(t *testing.T) {
	store := mustStore(t, testKey(1), secretPath(t))
	if !store.Available() {
		t.Fatal("有效密钥的 store 应可用")
	}
	if err := store.SetSecret("notification/token", "generic-secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSecret("notification/token"); err != nil || got != "generic-secret" {
		t.Fatalf("通用秘密往返失败: got=%q err=%v", got, err)
	}
	if err := store.SetBackupPassword("UDID-1", "backup-password"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetBackupPassword("UDID-1"); err != nil || got != "backup-password" {
		t.Fatalf("备份密码别名往返失败: got=%q err=%v", got, err)
	}
}

func TestAESSecretStoreUsesRandomNonce(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(2), path)
	if err := store.SetSecret("same-key", "same-value"); err != nil {
		t.Fatal(err)
	}
	first := rawEntries(t, path)["same-key"]
	if err := store.SetSecret("same-key", "same-value"); err != nil {
		t.Fatal(err)
	}
	second := rawEntries(t, path)["same-key"]
	if first == second {
		t.Fatal("相同明文的两次密文不能相同")
	}
}

func TestAESSecretStoreBindsCiphertextToEntryKey(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(3), path)
	if err := store.SetSecret("key-a", "value-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("key-b", "value-b"); err != nil {
		t.Fatal(err)
	}
	entries := rawEntries(t, path)
	entries["key-a"], entries["key-b"] = entries["key-b"], entries["key-a"]
	writeRawEntries(t, path, entries)
	for _, key := range []string{"key-a", "key-b"} {
		if _, err := store.GetSecret(key); err == nil {
			t.Fatalf("交换 AAD 绑定的 %s 密文后应认证失败", key)
		}
	}
}

func TestAESSecretStoreRejectsTamperingAndWrongKey(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(4), path)
	if err := store.SetSecret("entry", "private-value"); err != nil {
		t.Fatal(err)
	}

	entries := rawEntries(t, path)
	raw, err := base64.StdEncoding.DecodeString(entries["entry"])
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	entries["entry"] = base64.StdEncoding.EncodeToString(raw)
	writeRawEntries(t, path, entries)
	if _, err := store.GetSecret("entry"); err == nil {
		t.Fatal("篡改密文后应认证失败")
	}

	// Restore a valid ciphertext before checking another key.
	if err := store.SetSecret("entry", "private-value"); err != nil {
		t.Fatal(err)
	}
	wrong := mustStore(t, testKey(5), path)
	if _, err := wrong.GetSecret("entry"); err == nil {
		t.Fatal("错误密钥解密应失败")
	}
}

func TestAESSecretStoreRejectsMalformedFiles(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "invalid json", data: []byte("{")},
		{name: "invalid base64", data: mustJSON(t, map[string]string{"entry": "%%%"})},
		{name: "short ciphertext", data: mustJSON(t, map[string]string{"entry": base64.StdEncoding.EncodeToString([]byte("short"))})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := secretPath(t)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			store := mustStore(t, testKey(6), path)
			if _, err := store.GetSecret("entry"); err == nil {
				t.Fatal("损坏文件应返回错误")
			}
		})
	}
}

func TestAESSecretStoreRejectsJSONNullWithoutPanicking(t *testing.T) {
	path := secretPath(t)
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := mustStore(t, testKey(13), path)
	err := store.SetSecret("entry", "must-not-appear-in-error")
	if err == nil {
		t.Fatal("JSON null 不是秘密条目对象，应返回错误")
	}
	if !strings.Contains(err.Error(), "JSON 对象") {
		t.Fatalf("应返回明确的 JSON 对象错误，得到 %v", err)
	}
	if strings.Contains(err.Error(), "must-not-appear-in-error") {
		t.Fatalf("损坏文件错误不能泄露待保存秘密: %v", err)
	}
}

func TestAESSecretStoreMissingFileAndEntryUseStableNotFoundError(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(7), path)
	if _, err := store.GetSecret("missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("文件不存在时应返回 ErrSecretNotFound，得到 %v", err)
	}
	if err := store.SetSecret("present", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetBackupPassword("missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("条目不存在时应返回 ErrSecretNotFound，得到 %v", err)
	}
}

func TestAESSecretStoreDeleteExistingAndMissingIsIdempotent(t *testing.T) {
	store := mustStore(t, testKey(8), secretPath(t))
	if err := store.SetSecret("entry", "value"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSecret("entry"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSecret("entry"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("删除后应不存在，得到 %v", err)
	}
	if err := store.DeleteSecret("entry"); err != nil {
		t.Fatalf("重复删除应保持幂等，得到 %v", err)
	}
	if err := store.DeleteBackupPassword("missing-backup"); err != nil {
		t.Fatalf("删除不存在备份密码应保持幂等，得到 %v", err)
	}
}

func TestUnavailableSecretStoreAlwaysReturnsStableError(t *testing.T) {
	store := NewUnavailableSecretStore(secretPath(t))
	if store.Available() {
		t.Fatal("不可用 store 的 Available 应为 false")
	}
	checks := []error{
		store.SetSecret("key", "value"),
		store.DeleteSecret("key"),
		store.SetBackupPassword("udid", "password"),
		store.DeleteBackupPassword("udid"),
	}
	_, getErr := store.GetSecret("key")
	checks = append(checks, getErr)
	_, backupErr := store.GetBackupPassword("udid")
	checks = append(checks, backupErr)
	for _, err := range checks {
		if !errors.Is(err, ErrEncryptionUnavailable) {
			t.Fatalf("不可用 store 应返回 ErrEncryptionUnavailable，得到 %v", err)
		}
	}
}

func TestAESSecretStoreFileIsPrivateAndContainsNoPlaintextOrKey(t *testing.T) {
	path := secretPath(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := "a-unique-plaintext-secret"
	store := mustStore(t, key, path)
	if err := store.SetSecret("entry", plaintext); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(plaintext)) {
		t.Fatal("磁盘文件泄露明文")
	}
	if bytes.Contains(data, key) {
		t.Fatal("磁盘文件泄露密钥")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secrets.enc 权限 = %04o, want 0600", got)
	}
}

func TestAESSecretStorePersistsAcrossInstances(t *testing.T) {
	path := secretPath(t)
	key := testKey(9)
	first := mustStore(t, key, path)
	if err := first.SetSecret("entry", "persisted"); err != nil {
		t.Fatal(err)
	}
	second := mustStore(t, key, path)
	if got, err := second.GetSecret("entry"); err != nil || got != "persisted" {
		t.Fatalf("新实例读取失败: got=%q err=%v", got, err)
	}
}

func TestAESSecretStoreSingleInstanceConcurrentMethodsDoNotLoseUpdates(t *testing.T) {
	store := mustStore(t, testKey(10), secretPath(t))
	const count = 48
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%02d", i)
			value := fmt.Sprintf("value-%02d", i)
			if err := store.SetSecret(key, value); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("key-%02d", i)
		want := fmt.Sprintf("value-%02d", i)
		if got, err := store.GetSecret(key); err != nil || got != want {
			t.Fatalf("并发写丢失 %s: got=%q err=%v", key, got, err)
		}
	}
}

func TestAESSecretStoreAcceptsExactSizeOnReadAndWrite(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(14), path)
	marker := "exact-boundary-content"
	entries := exactSizedSecretEntries(t, testedSecretsFileLimit, marker)

	if err := store.saveLocked(entries); err != nil {
		t.Fatalf("恰好达到大小上限的有效 JSON 应可写入: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != testedSecretsFileLimit {
		t.Fatalf("边界文件大小 = %d, want %d", info.Size(), testedSecretsFileLimit)
	}
	loaded, err := store.loadLocked()
	if err != nil {
		t.Fatalf("恰好达到大小上限的有效 JSON 应可读取: %v", err)
	}
	if !strings.HasPrefix(loaded["entry"], marker) {
		t.Fatal("边界文件内容未完整读取")
	}
}

func TestAESSecretStoreRejectsMaxPlusOneOnReadWithoutLeakingContent(t *testing.T) {
	path := secretPath(t)
	marker := "read-limit-sensitive-marker"
	data := exactSizedSecretJSON(t, testedSecretsFileLimit+1, marker)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := mustStore(t, testKey(15), path)

	_, err := store.loadLocked()
	if err == nil {
		t.Fatal("超过大小上限 1 字节的秘密文件应拒绝读取")
	}
	if !strings.Contains(err.Error(), "大小上限") {
		t.Fatalf("应返回明确的大小上限错误，得到 %v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("大小错误不能泄露文件内容: %v", err)
	}
}

func TestAESSecretStoreRejectsMaxPlusOneOnWriteAndPreservesOldFile(t *testing.T) {
	path := secretPath(t)
	old := []byte(`{"stable":"old-ciphertext"}`)
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	store := mustStore(t, testKey(16), path)
	marker := "write-limit-sensitive-marker"
	entries := exactSizedSecretEntries(t, testedSecretsFileLimit+1, marker)

	err := store.saveLocked(entries)
	if err == nil {
		t.Fatal("超过大小上限 1 字节的秘密内容应拒绝写入")
	}
	if !strings.Contains(err.Error(), "大小上限") {
		t.Fatalf("应返回明确的大小上限错误，得到 %v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("大小错误不能泄露待写内容: %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, old) {
		t.Fatal("超限保存必须保留旧文件")
	}
}

func TestAESSecretStoreSetSecretCannotCreateFutureUnreadableFile(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(17), path)
	if err := store.SetSecret("stable", "old-value"); err != nil {
		t.Fatal(err)
	}
	marker := "public-set-sensitive-marker"
	hugeSecret := marker + strings.Repeat("x", 6<<20)

	err := store.SetSecret("huge", hugeSecret)
	if err == nil {
		t.Fatal("SetSecret 不得写出超过读取上限的文件")
	}
	if !strings.Contains(err.Error(), "大小上限") || strings.Contains(err.Error(), marker) {
		t.Fatalf("SetSecret 应返回不泄密的大小错误，得到 %v", err)
	}
	reopened := mustStore(t, testKey(17), path)
	if got, err := reopened.GetSecret("stable"); err != nil || got != "old-value" {
		t.Fatalf("超限 SetSecret 后旧秘密必须仍可读: got=%q err=%v", got, err)
	}
	if _, err := reopened.GetSecret("huge"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("超限秘密不得落盘，得到 %v", err)
	}
}

func TestAESSecretStoreFailedAtomicSavePreservesReadableCiphertext(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(11), path)
	if err := store.SetSecret("stable", "old-value"); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected atomic save failure")
	store.writeFile = func(string, []byte, os.FileMode) error { return wantErr }
	if err := store.SetSecret("new", "new-value"); !errors.Is(err, wantErr) {
		t.Fatalf("应返回原始持久化错误，得到 %v", err)
	}

	reopened := mustStore(t, testKey(11), path)
	if got, err := reopened.GetSecret("stable"); err != nil || got != "old-value" {
		t.Fatalf("失败保存后旧密文应可读: got=%q err=%v", got, err)
	}
	if _, err := reopened.GetSecret("new"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("失败保存不能留下新条目，得到 %v", err)
	}
}

func TestSecretStoreErrorsDoNotLeakSensitiveValues(t *testing.T) {
	path := secretPath(t)
	key := []byte("abcdef0123456789abcdef0123456789")
	plaintext := "do-not-leak-this-plaintext"
	entryKey := "sensitive-entry-key"
	store := mustStore(t, key, path)
	if err := store.SetSecret(entryKey, plaintext); err != nil {
		t.Fatal(err)
	}
	entries := rawEntries(t, path)
	rawCiphertext := entries[entryKey]
	entries[entryKey] = rawCiphertext[:len(rawCiphertext)-2] + "%%"
	writeRawEntries(t, path, entries)
	_, err := store.GetSecret(entryKey)
	if err == nil {
		t.Fatal("损坏密文应失败")
	}
	for _, sensitive := range []string{plaintext, string(key), rawCiphertext} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("错误信息泄露敏感值 %q: %v", sensitive, err)
		}
	}
}

func mustStore(t *testing.T, key []byte, path string) *AESSecretStore {
	t.Helper()
	store, err := NewAESSecretStore(key, path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func secretPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "secrets.enc")
}

func rawEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func writeRawEntries(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.WriteFile(path, mustJSON(t, entries), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func exactSizedSecretEntries(t *testing.T, size int, marker string) map[string]string {
	t.Helper()
	empty, err := json.MarshalIndent(map[string]string{"entry": ""}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fillerLength := size - len(empty)
	if fillerLength < len(marker) {
		t.Fatalf("目标大小 %d 无法容纳 marker", size)
	}
	entries := map[string]string{"entry": marker + strings.Repeat("a", fillerLength-len(marker))}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("测试 JSON 大小 = %d, want %d", len(data), size)
	}
	return entries
}

func exactSizedSecretJSON(t *testing.T, size int, marker string) []byte {
	t.Helper()
	entries := exactSizedSecretEntries(t, size, marker)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAESSecretStoreValidateAllExistingCiphertexts(t *testing.T) {
	path := secretPath(t)
	store := mustStore(t, testKey(27), path)
	if err := store.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("first", "first-secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret("second", "second-secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.Validate(); err != nil {
		t.Fatal(err)
	}
	wrong := mustStore(t, testKey(28), path)
	if err := wrong.Validate(); err == nil {
		t.Fatal("错误密钥必须在初始化检查时失败")
	}
	entries := rawEntries(t, path)
	entries["second"] = "broken"
	writeRawEntries(t, path, entries)
	if err := store.Validate(); err == nil {
		t.Fatal("必须检查每一条密文")
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Validate(); err == nil {
		t.Fatal("损坏JSON必须失败")
	}
	if err := NewUnavailableSecretStore(path).Validate(); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatal("不可用存储不能通过验证")
	}
}
