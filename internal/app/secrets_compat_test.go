package app

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSecretStoreCompatibilityErrorsPreserveSentinelIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	store, err := newAESSecretStore(make([]byte, 32), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSecret("missing"); !errors.Is(err, errPasswordNotFound) {
		t.Fatalf("缺失秘密必须保持 errPasswordNotFound 身份，得到 %v", err)
	}

	unavailable := newUnavailableSecretStore(path)
	if _, err := unavailable.GetSecret("missing"); !errors.Is(err, errEncryptionUnavailable) {
		t.Fatalf("不可用秘密存储必须保持 errEncryptionUnavailable 身份，得到 %v", err)
	}
}
