package app

import (
	"strings"
	"testing"
)

func TestBackupProgressRecommendedMarkupContract(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, want := range []string{
		`.backup-progress-panel{`,
		`.backup-overall-track{`,
		`.backup-current-file{`,
		`role="progressbar"`,
		`aria-valuemin="0"`,
		`aria-valuemax="100"`,
		`id="bkprogress-current-{{$d.UDID}}" class="backup-current-file hidden"`,
		`@media(prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("正式页面缺少推荐进度组件约束 %q", want)
		}
	}
	if strings.Contains(markup, "文件切换时这里会从零重新计算") {
		t.Fatal("正式页面不应使用容易误解的旧说明")
	}
	if strings.Contains(markup, `class="backup-file-track"`) {
		t.Fatal("推荐版只应保留一条总体进度条，当前文件使用次级文字")
	}
}

func TestBackupProgressRendererCoversApprovedStateMatrix(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, mode := range []string{
		`return 'preparing'`,
		`return 'transferring'`,
		`return 'overall-only'`,
		`return 'reconnecting'`,
		`return 'interrupted'`,
		`return 'completed'`,
		`return 'failed'`,
	} {
		if !strings.Contains(markup, mode) {
			t.Errorf("进度渲染器缺少状态模式 %q", mode)
		}
	}
	for _, behavior := range []string{
		`function backupProgressMode(progress)`,
		`function renderBackupProgress(d)`,
		`track.removeAttribute('aria-valuenow')`,
		`current.classList.toggle('hidden',!hasCurrentFile)`,
		`currentValue.textContent=hasCurrentFile?`,
		`formatBackupBytes(progress.current_bytes)`,
	} {
		if !strings.Contains(markup, behavior) {
			t.Errorf("进度渲染器缺少行为 %q", behavior)
		}
	}
}

func TestBackupProgressCopyIsBilingualAndActionable(t *testing.T) {
	markup := readEmbeddedPage(t, "index.html")
	for _, copy := range []string{
		`currentFile:{zh:"当前文件",en:"Current file"}`,
		`currentFileHint:{zh:"当前正在传输的文件进度",en:"Progress of the file currently being transferred"}`,
		`separator:{zh:"。 ",en:". "}`,
		`reconnectingTitle:{zh:"等待设备重新连接",en:"Waiting for the device to reconnect"}`,
		`interruptedHint:{zh:"设备持续离线，本次备份已安全停止。重新连接后可再次开始备份。",en:"The device stayed offline, so this backup stopped safely. Reconnect it to start a new backup."}`,
		`storageHint:{zh:"请检查保存位置的剩余空间和写入权限。",en:"Check free space and write access for the backup location."}`,
	} {
		if !strings.Contains(markup, copy) {
			t.Errorf("进度组件缺少双语文案 %q", copy)
		}
	}
}

func TestBackupProgressServerFallbackShowsPreparingWithoutFakeBytes(t *testing.T) {
	dev := &device{UDID: "U-PROGRESS-UI", Name: "很长的设备名称 iPhone for Family Backup", DeviceType: "iPhone", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}
	cfg := &backupConfig{UDID: dev.UDID, BackupDirectory: "/backups"}
	markup := renderIndex(t, homePayload{
		Devices:        []*device{dev},
		Configs:        map[string]*backupConfig{dev.UDID: cfg},
		BackupStatuses: map[string]bool{dev.UDID: true},
	})

	if !strings.Contains(markup, `id="bkbar-U-PROGRESS-UI" class="backup-progress-panel"`) {
		t.Fatal("服务端初始渲染应在任务运行时显示准备状态")
	}
	if strings.Contains(markup, "209 MB") || strings.Contains(markup, "440 MB") {
		t.Fatal("没有真实 SSE 数据时不能伪造当前文件字节")
	}
	if !strings.Contains(markup, `class="device-name"`) {
		t.Fatal("长设备名应使用可收缩、截断的语义样式")
	}
}
