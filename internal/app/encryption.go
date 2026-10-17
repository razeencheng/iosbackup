package app

import (
	"context"
	"fmt"

	"iosbackup/internal/i18n"
)

// RestoreOptions 恢复选项（映射 idevicebackup2 restore 的 --flag）。
type restoreOptions struct {
	System   bool `json:"system"`    // --system  恢复系统文件
	Settings bool `json:"settings"`  // --settings 恢复设备设置
	Reboot   bool `json:"reboot"`    // 默认 true；false → --no-reboot
	Remove   bool `json:"remove"`    // --remove  移除未恢复项
	SkipApps bool `json:"skip_apps"` // --skip-apps 不重装 App
	Copy     bool `json:"copy"`      // --copy    恢复前复制备份目录
}

// acquireDeviceOp 校验设备可用并占用备份槽（防与自动备份/其它设备操作并发）。
// 返回设备副本、备份目录、释放函数。调用方务必 defer release()。
func (app *application) acquireDeviceOp(udid string) (device *device, dir string, release func(), err error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		return nil, "", nil, err
	}
	if app.backupInProgress[udid] {
		return nil, "", nil, i18n.NewError(i18n.Busy, nil)
	}
	dev, ok := app.devices[udid]
	if !ok {
		return nil, "", nil, i18n.NewError(i18n.DeviceNotFound, nil)
	}
	if !dev.IsOnline {
		return nil, "", nil, i18n.NewError(i18n.DeviceOffline, nil)
	}

	if err := app.connectionAdmissionUnsafe(dev); err != nil {
		return nil, "", nil, err
	}
	dir = dirBackups
	if cfg, ok := app.configs[udid]; ok && cfg.BackupDirectory != "" {
		dir = cfg.BackupDirectory
	}

	app.backupInProgress[udid] = true
	devCopy := *dev
	release = func() {
		app.mu.Lock()
		delete(app.backupInProgress, udid)
		app.mu.Unlock()
	}
	return &devCopy, dir, release, nil
}

// SetBackupEncryption 开/关设备备份加密。密码经 env BACKUP_PASSWORD（绝不进 argv/日志）。
// 开启时把密码存入 secretStore（故需 secretStore 可用）；关闭成功后删除存储的密码。
func (app *application) SetBackupEncryption(ctx context.Context, udid string, enable bool, password string) error {
	if password == "" {
		return i18n.NewError(i18n.EncryptionPasswordRequired, nil)
	}
	// 开启加密要存密码 → 必须有可用 secretStore（否则密码无处安全保存）
	if enable && (app.secretStore == nil || !app.secretStore.Available()) {
		return errEncryptionUnavailable
	}

	device, _, release, err := app.acquireDeviceOp(udid)
	if err != nil {
		return err
	}
	defer release()

	state := "off"
	if enable {
		state = "on"
	}
	app.addWarnLog(udid, fmt.Sprintf("event=encryption.started state=%s", state))

	env := []string{"BACKUP_PASSWORD=" + password}
	out, err := app.runIdeviceCmdBoundedEnv(ctx, cmdKindLong, device, env, cmdIdevicebackup2, "encryption", state)
	if err != nil {
		return operationError(i18n.EncryptionFailed, err, cleanBackupOutput(out))
	}

	// 同步 secretStore
	if enable {
		if e := app.secretStore.SetBackupPassword(udid, password); e != nil {
			message := fmt.Sprintf("event=encryption.password_store_failed Encryption enabled but password could not be stored error=%v", e)
			app.addErrorLog(udid, message)
			app.notifySystemEvent("encryption_password_store_failed", map[string]string{"device_name": device.Name, "diagnostics": message})
		}
	} else if app.secretStore != nil && app.secretStore.Available() {
		_ = app.secretStore.DeleteBackupPassword(udid)
	}
	app.addInfoLog(udid, fmt.Sprintf("event=encryption.completed state=%s", map[bool]string{true: "on", false: "off"}[enable]))
	return nil
}

// ChangeBackupPassword 修改设备备份密码。旧/新密码经 env（BACKUP_PASSWORD/BACKUP_PASSWORD_NEW），不进 argv。
func (app *application) ChangeBackupPassword(ctx context.Context, udid, oldPw, newPw string) error {
	if oldPw == "" || newPw == "" {
		return i18n.NewError(i18n.PasswordsRequired, nil)
	}
	if app.secretStore == nil || !app.secretStore.Available() {
		return errEncryptionUnavailable
	}

	device, _, release, err := app.acquireDeviceOp(udid)
	if err != nil {
		return err
	}
	defer release()

	app.addWarnLog(udid, "event=encryption.password_change_started")
	env := []string{"BACKUP_PASSWORD=" + oldPw, "BACKUP_PASSWORD_NEW=" + newPw}
	out, err := app.runIdeviceCmdBoundedEnv(ctx, cmdKindLong, device, env, cmdIdevicebackup2, "changepw")
	if err != nil {
		return operationError(i18n.PasswordChangeFailed, err, cleanBackupOutput(out))
	}

	if e := app.secretStore.SetBackupPassword(udid, newPw); e != nil {
		message := fmt.Sprintf("event=encryption.password_store_failed Password changed but new password could not be stored error=%v", e)
		app.addErrorLog(udid, message)
		app.notifySystemEvent("password_store_failed", map[string]string{"device_name": device.Name, "diagnostics": message})
	}
	app.addInfoLog(udid, "event=encryption.password_changed")
	return nil
}

// Restore 把备份恢复到设备（**破坏性**，长操作）。password 经 env（加密备份需要）。
// 注意：调用前必须已通过多重确认与 RestoreEnabled 检查（见 handleRestore）。
func (app *application) Restore(ctx context.Context, udid, password string, opts restoreOptions) error {
	device, dir, release, err := app.acquireDeviceOp(udid)
	if err != nil {
		return err
	}
	defer release()

	args := []string{"restore", dir}
	if opts.System {
		args = append(args, "--system")
	}
	if opts.Settings {
		args = append(args, "--settings")
	}
	if !opts.Reboot {
		args = append(args, "--no-reboot")
	}
	if opts.Remove {
		args = append(args, "--remove")
	}
	if opts.SkipApps {
		args = append(args, "--skip-apps")
	}
	if opts.Copy {
		args = append(args, "--copy")
	}

	var env []string
	if password != "" {
		env = append(env, "BACKUP_PASSWORD="+password)
	}

	app.addWarnLog(udid, fmt.Sprintf("event=restore.started Destructive restore directory=%s system=%v settings=%v reboot=%v",
		dir, opts.System, opts.Settings, opts.Reboot))
	out, err := app.runIdeviceCmdBoundedEnv(ctx, cmdKindLong, device, env, cmdIdevicebackup2, args...)
	if err != nil {
		return operationError(i18n.RestoreFailed, err, cleanBackupOutput(out))
	}
	app.addWarnLog(udid, "event=restore.completed")
	return nil
}
