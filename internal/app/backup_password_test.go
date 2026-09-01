package app

import "testing"

// backupPasswordEnv：有可用 secretStore 且存了密码时返回 BACKUP_PASSWORD env，否则 nil。
func TestBackupPasswordEnv(t *testing.T) {
	app := newApplication()
	if env := app.backupPasswordEnv("U"); env != nil {
		t.Errorf("无 secretStore 应返回 nil，得 %v", env)
	}

	store, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = store

	if env := app.backupPasswordEnv("U"); env != nil {
		t.Errorf("未存密码应返回 nil，得 %v", env)
	}

	if err := store.SetBackupPassword("U", "s3cret"); err != nil {
		t.Fatal(err)
	}
	env := app.backupPasswordEnv("U")
	if len(env) != 1 || env[0] != "BACKUP_PASSWORD=s3cret" {
		t.Errorf("应返回 [BACKUP_PASSWORD=s3cret]，得 %v", env)
	}
}

// isEncryptedBackupError：识别 idevicebackup2 对加密备份的"密码错误"输出。
func TestIsEncryptedBackupError(t *testing.T) {
	yes := []byte("ErrorCode 207: Invalid password when restoring encrypted backup (MBErrorDomain/207)")
	if !isEncryptedBackupError(yes) {
		t.Error("应识别为加密备份密码错误")
	}
	if isEncryptedBackupError([]byte("some other error")) {
		t.Error("普通错误不应误判为加密错误")
	}
}
