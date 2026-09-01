package app

import (
	"context"
	"strings"
	"testing"
)

// addOnlineDevice 注册一个在线设备 + 默认配置（测试辅助）。
func addOnlineDevice(app *application, udid, connDesc string) {
	app.mu.Lock()
	app.devices[udid] = &device{UDID: udid, Connection: connDesc, IsOnline: true}
	app.configs[udid] = &backupConfig{UDID: udid, BackupDirectory: "/backups"}
	app.mu.Unlock()
}

func TestBackupInfoCommand(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		return []byte("Backup version 3.3"), nil
	}
	addOnlineDevice(app, "USB-INFO", connectionTypeDesc(connectTypeUSB))

	out, err := app.BackupInfo(context.Background(), "USB-INFO")
	if err != nil {
		t.Fatalf("BackupInfo 失败: %v", err)
	}
	if !strings.Contains(string(out), "Backup version") {
		t.Errorf("应返回 info 输出，得到 %q", out)
	}

	call, ok := runner.lastCall()
	if !ok {
		t.Fatal("无调用记录")
	}
	if !strings.HasSuffix(call.name, "idevicebackup2") {
		t.Errorf("应调用 idevicebackup2，得到 %q", call.name)
	}
	if !argsHas(call.args, "info") {
		t.Errorf("args 应含 info，得到 %v", call.args)
	}
	if !argsHasPair(call.args, "-u", "USB-INFO") {
		t.Errorf("args 应含 -u USB-INFO，得到 %v", call.args)
	}
	if !argsHas(call.args, "/backups") {
		t.Errorf("args 应含备份目录 /backups，得到 %v", call.args)
	}
	if argsHas(call.args, "-n") {
		t.Errorf("USB 设备不应有 -n，得到 %v", call.args)
	}
}

func TestBackupListNetworkCommand(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	runner.outputFn = func(name string, args []string, env []string) ([]byte, error) {
		return []byte("a.db,domain,1024\nb.plist,domain,16\n"), nil
	}
	addOnlineDevice(app, "NET-LIST", connectionTypeDesc(connectTypeNetwork))

	entries, err := app.BackupList(context.Background(), "NET-LIST")
	if err != nil {
		t.Fatalf("BackupList 失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析出 2 行，得到 %d: %+v", len(entries), entries)
	}
	if len(entries[0].Fields) != 3 || entries[0].Fields[0] != "a.db" {
		t.Errorf("第一行解析错误: %+v", entries[0])
	}

	call, ok := runner.lastCall()
	if !ok {
		t.Fatal("无调用记录")
	}
	if !argsHas(call.args, "list") {
		t.Errorf("args 应含 list，得到 %v", call.args)
	}
	if !argsHas(call.args, "-n") {
		t.Errorf("网络设备应有 -n，得到 %v", call.args)
	}
	if !envHas(call.env, "USBMUXD_SOCKET_ADDRESS") {
		t.Errorf("网络设备应注入 netmuxd socket，env: %v", call.env)
	}
}

func TestUnbackUsesParentContextWithoutFixedDeadline(t *testing.T) {
	app, _ := newLocalUnbackTestApp(t, "USB-UNBACK")
	var deadlineSet bool
	app.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		_, deadlineSet = ctx.Deadline()
		return nil
	}

	if err := app.Unback(context.Background(), "USB-UNBACK"); err != nil {
		t.Fatalf("Unback 失败: %v", err)
	}
	if deadlineSet {
		t.Error("本地 unback 是长操作，不应自行覆盖父 context 的 deadline")
	}
}

func TestBackupCmdRefuseWhenBackingUp(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	addOnlineDevice(app, "BUSY", connectionTypeDesc(connectTypeUSB))
	app.mu.Lock()
	app.backupInProgress["BUSY"] = true
	app.mu.Unlock()

	if _, err := app.BackupInfo(context.Background(), "BUSY"); err == nil {
		t.Error("设备备份中时 BackupInfo 应拒绝")
	}
	if len(runner.allCalls()) != 0 {
		t.Errorf("备份中不应实际执行命令，调用: %v", runner.allCalls())
	}
}

func TestBackupCmdOfflineDevice(t *testing.T) {
	app := newApplication()
	runner := &mockRunner{}
	app.cmdRunner = runner.run
	app.mu.Lock()
	app.devices["OFF"] = &device{UDID: "OFF", Connection: connectionTypeDesc(connectTypeUSB), IsOnline: false}
	app.configs["OFF"] = &backupConfig{UDID: "OFF", BackupDirectory: "/backups"}
	app.mu.Unlock()

	if _, err := app.BackupInfo(context.Background(), "OFF"); err == nil {
		t.Error("离线设备 BackupInfo 应报错")
	}
}

func TestParseBackupListCSV(t *testing.T) {
	out := []byte("file1.db,AppDomain-x,2048\nfile2.plist,HomeDomain,64\n")
	entries := parseBackupList(out)
	if len(entries) != 2 {
		t.Fatalf("应解析 2 行，得到 %d", len(entries))
	}
	if entries[1].Fields[1] != "HomeDomain" {
		t.Errorf("第二行第二列应为 HomeDomain，得到 %q", entries[1].Fields[1])
	}
}

func TestParseBackupListFallbackNonCSV(t *testing.T) {
	out := []byte("just a plain line\nanother line\n")
	entries := parseBackupList(out)
	if len(entries) != 2 {
		t.Fatalf("非 CSV 应回退按行，得到 %d 行", len(entries))
	}
}

// TestParseBackupListStripsProgressNoise 用真机观测到的噪声格式：idevicebackup2 list 把
// 下载 manifest 的进度条 + ANSI 转义混进 stdout，真实 CSV 记录在其后。清洗后应只剩真记录。
func TestParseBackupListStripsProgressNoise(t *testing.T) {
	raw := "\x1b[s\x1b[u\x1b[1A\x1b[2K\rBackup     [..............................]   0%\n" +
		"\x1b[2K\rSending    TEST-DEVICE-ID/Manifest.db\n" +
		"\x1b[2K\r           [==============================] 100.0%   6.4 MB / 6.4 MB     \n" +
		"deadbeef, cafebabe, 100644, 19665, 25, 25, 2026-05-24 01:27:14, 2026-05-24 01:27:14, 2026-02-07 09:21:06, 45056, 4, WirelessDomain, Library/Databases/CellularUsage.db, , {}\n" +
		"Backup     [##############################] 100%\n" +
		"Status     \n"
	entries := parseBackupList([]byte(raw))
	if len(entries) != 1 {
		t.Fatalf("清洗后应只剩 1 条真实 CSV 记录，得到 %d: %+v", len(entries), entries)
	}
	joined := strings.Join(entries[0].Fields, "|")
	if !strings.Contains(joined, "CellularUsage.db") {
		t.Errorf("记录应含真实路径，得到 %v", entries[0].Fields)
	}
}

// TestCleanBackupOutputDropsAnsiAndProgress 校验 info 输出清洗：去 ANSI/进度、保留真实汇总行。
func TestCleanBackupOutputDropsAnsiAndProgress(t *testing.T) {
	raw := "\x1b[2K\rBackup     [..............................]   0%\n" +
		"   47   27619529 SysContainerDomain-com.apple.linkd\n" +
		"  1190  141390058 Total\n" +
		"Backup     [##############################] 100%\n" +
		"Sending    TEST-DEVICE-ID/Manifest.db\n" +
		"           [==============================] 100.0%   6.4 MB / 6.4 MB     \n"
	clean := cleanBackupOutput([]byte(raw))
	if strings.Contains(clean, "\x1b") {
		t.Error("不应残留 ANSI 转义")
	}
	if strings.Contains(clean, "Sending") || strings.Contains(clean, "100%") || strings.Contains(clean, "[") {
		t.Errorf("不应残留进度/状态行，得到:\n%s", clean)
	}
	if !strings.Contains(clean, "Total") || !strings.Contains(clean, "SysContainerDomain-com.apple.linkd") {
		t.Errorf("应保留真实数据行，得到:\n%s", clean)
	}
}

// TestCleanBackupOutputKeepsRealInfo 确保形如 "Backup version 3.3" 的真实信息不被误判为进度。
func TestCleanBackupOutputKeepsRealInfo(t *testing.T) {
	clean := cleanBackupOutput([]byte("Backup version 3.3\nIsEncrypted: No\n"))
	if !strings.Contains(clean, "Backup version 3.3") {
		t.Errorf("含 Backup 字样的真实信息行不应被误删，得到:\n%s", clean)
	}
	if !strings.Contains(clean, "IsEncrypted: No") {
		t.Errorf("真实信息行应保留，得到:\n%s", clean)
	}
}
