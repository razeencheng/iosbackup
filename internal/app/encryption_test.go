package app

import (
	"context"
	"strings"
	"testing"
)

func newEncTestApp(t *testing.T, udid string) (*application, *mockRunner) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	store, err := newAESSecretStore(testKey32(), tempSecretsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = store
	addOnlineDevice(app, udid, connectionTypeDesc(connectTypeUSB))
	return app, runner
}

// assertNoPasswordInArgs 断言 args 里不含任何敏感密码值。
func assertNoPasswordInArgs(t *testing.T, args []string, secrets ...string) {
	t.Helper()
	for _, a := range args {
		for _, s := range secrets {
			if s != "" && strings.Contains(a, s) {
				t.Errorf("密码 %q 绝不应出现在 argv: %v", s, args)
			}
		}
	}
}

func TestSetBackupEncryptionOnStoresPassword(t *testing.T) {
	app, runner := newEncTestApp(t, "ENC-ON")

	if err := app.SetBackupEncryption(context.Background(), "ENC-ON", true, "mypw"); err != nil {
		t.Fatalf("SetBackupEncryption on 失败: %v", err)
	}
	call, _ := runner.lastCall()
	if !argsHas(call.args, "encryption") || !argsHas(call.args, "on") {
		t.Errorf("应 encryption on，args=%v", call.args)
	}
	assertNoPasswordInArgs(t, call.args, "mypw")
	if envGet(call.env, "BACKUP_PASSWORD") != "mypw" {
		t.Errorf("密码应在 env BACKUP_PASSWORD，得到 %q", envGet(call.env, "BACKUP_PASSWORD"))
	}
	got, err := app.secretStore.GetBackupPassword("ENC-ON")
	if err != nil || got != "mypw" {
		t.Errorf("开启加密后应存密码，得到 %q err=%v", got, err)
	}
}

func TestSetBackupEncryptionRequiresKey(t *testing.T) {
	app := newApplication()
	app.cmdRunner = (&mockRunner{}).run
	app.secretStore = newUnavailableSecretStore(tempSecretsPath(t))
	addOnlineDevice(app, "ENC-NK", connectionTypeDesc(connectTypeUSB))

	if err := app.SetBackupEncryption(context.Background(), "ENC-NK", true, "pw"); err == nil {
		t.Error("缺密钥时开启加密应报错（无法安全存密码）")
	}
}

func TestSetBackupEncryptionOffDeletesPassword(t *testing.T) {
	app, _ := newEncTestApp(t, "ENC-OFF")
	if err := app.SetBackupEncryption(context.Background(), "ENC-OFF", true, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetBackupEncryption(context.Background(), "ENC-OFF", false, "pw"); err != nil {
		t.Fatalf("关闭加密失败: %v", err)
	}
	if _, err := app.secretStore.GetBackupPassword("ENC-OFF"); err == nil {
		t.Error("关闭加密后应删除存储的密码")
	}
}

func TestChangeBackupPassword(t *testing.T) {
	app, runner := newEncTestApp(t, "CHG")

	if err := app.ChangeBackupPassword(context.Background(), "CHG", "oldpw", "newpw"); err != nil {
		t.Fatalf("ChangeBackupPassword 失败: %v", err)
	}
	call, _ := runner.lastCall()
	if !argsHas(call.args, "changepw") {
		t.Errorf("应 changepw，args=%v", call.args)
	}
	assertNoPasswordInArgs(t, call.args, "oldpw", "newpw")
	if envGet(call.env, "BACKUP_PASSWORD") != "oldpw" {
		t.Errorf("旧密码应在 env BACKUP_PASSWORD，得到 %q", envGet(call.env, "BACKUP_PASSWORD"))
	}
	if envGet(call.env, "BACKUP_PASSWORD_NEW") != "newpw" {
		t.Errorf("新密码应在 env BACKUP_PASSWORD_NEW，得到 %q", envGet(call.env, "BACKUP_PASSWORD_NEW"))
	}
	got, _ := app.secretStore.GetBackupPassword("CHG")
	if got != "newpw" {
		t.Errorf("改密后应存新密码，得到 %q", got)
	}
}

func TestRestoreCommandAndPasswordEnv(t *testing.T) {
	app, runner := newEncTestApp(t, "RST")

	opts := restoreOptions{System: true, Settings: true, Reboot: false, Remove: true}
	if err := app.Restore(context.Background(), "RST", "rpw", opts); err != nil {
		t.Fatalf("Restore 失败: %v", err)
	}
	call, _ := runner.lastCall()
	if !argsHas(call.args, "restore") {
		t.Errorf("应 restore，args=%v", call.args)
	}
	for _, want := range []string{"--system", "--settings", "--no-reboot", "--remove"} {
		if !argsHas(call.args, want) {
			t.Errorf("args 应含 %s，得到 %v", want, call.args)
		}
	}
	assertNoPasswordInArgs(t, call.args, "rpw")
	if envGet(call.env, "BACKUP_PASSWORD") != "rpw" {
		t.Errorf("恢复密码应在 env，得到 %q", envGet(call.env, "BACKUP_PASSWORD"))
	}
}

func TestRestoreLongCommandNoDeadline(t *testing.T) {
	app := newApplication()
	var deadlineSet bool
	app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
		_, deadlineSet = ctx.Deadline()
		return nil, nil
	}
	store, _ := newAESSecretStore(testKey32(), tempSecretsPath(t))
	app.secretStore = store
	addOnlineDevice(app, "RST-L", connectionTypeDesc(connectTypeUSB))

	_ = app.Restore(context.Background(), "RST-L", "", restoreOptions{Reboot: true})
	if deadlineSet {
		t.Error("restore 是长命令，不应设固定 deadline")
	}
}

func TestP4OpsRefuseWhenBusy(t *testing.T) {
	app, _ := newEncTestApp(t, "BUSY-OP")
	app.mu.Lock()
	app.backupInProgress["BUSY-OP"] = true
	app.mu.Unlock()

	if err := app.SetBackupEncryption(context.Background(), "BUSY-OP", true, "pw"); err == nil {
		t.Error("设备忙时加密应拒绝")
	}
	if err := app.Restore(context.Background(), "BUSY-OP", "", restoreOptions{}); err == nil {
		t.Error("设备忙时恢复应拒绝")
	}
}

func TestP4OpsReleaseBackupSlot(t *testing.T) {
	app, _ := newEncTestApp(t, "REL")
	_ = app.SetBackupEncryption(context.Background(), "REL", true, "pw")
	app.mu.RLock()
	busy := app.backupInProgress["REL"]
	app.mu.RUnlock()
	if busy {
		t.Error("操作完成后应释放备份槽（backupInProgress 应已清）")
	}
}
