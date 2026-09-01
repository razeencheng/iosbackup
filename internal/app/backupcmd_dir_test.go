package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 取自测试 NAS（iosbk-p1test-run）容器真实 /proc/<pid>/mountinfo 的代表性子集：
// /backups、/configs、/var/lib/lockdown 为宿主 bind mount（btrfs，应持久化），
// 根 / 为 overlay、/run/udev 与 /dev 为 tmpfs（应判为不持久化）。
const fixtureMountinfo = `1 1 0:1 / / rw,relatime - overlay overlay rw,lowerdir=a,upperdir=b
20 1 0:5 / /proc rw,nosuid - proc proc rw
21 1 0:6 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k
30 21 0:7 / /dev/shm rw,nosuid - tmpfs shm rw
40 1 0:9 / /run/udev rw,relatime - tmpfs tmpfs rw
50 1 0:33 /vol1/1000/test/backups /backups rw,relatime - btrfs /dev/sda1 rw
51 1 0:33 /vol1/1000/test/configs /configs rw,relatime - btrfs /dev/sda1 rw
52 1 0:33 /vol1/1000/test/lockdown /var/lib/lockdown rw,relatime - btrfs /dev/sda1 rw`

func TestEnclosingMountFS(t *testing.T) {
	data := []byte(fixtureMountinfo)
	cases := []struct {
		path   string
		wantFS string
	}{
		{"/backups", "btrfs"},
		{"/backups/sub/dir", "btrfs"}, // 最长前缀仍是 /backups
		{"/configs", "btrfs"},
		{"/var/lib/lockdown/x", "btrfs"},
		{"/run/dev", "overlay"},      // /run 不是独立挂载点 → 落到根 overlay
		{"/run/udev/foo", "tmpfs"},   // /run/udev 是 tmpfs
		{"/tmp/whatever", "overlay"}, // 落到根
		{"/", "overlay"},
	}
	for _, c := range cases {
		fs, ok := enclosingMountFS(data, c.path)
		if !ok {
			t.Errorf("%s: 未匹配到挂载点", c.path)
			continue
		}
		if fs != c.wantFS {
			t.Errorf("%s: fstype = %q, 期望 %q", c.path, fs, c.wantFS)
		}
	}
}

// 验证「持久化判定」：btrfs 等真实 fs 算持久化，overlay/tmpfs 算临时。
func TestEphemeralClassification(t *testing.T) {
	persistent := []string{"btrfs", "ext4", "xfs", "zfs", "nfs4", "fuse.glusterfs"}
	for _, fs := range persistent {
		if ephemeralFS[fs] {
			t.Errorf("%s 不应被判为临时文件系统", fs)
		}
	}
	for _, fs := range []string{"overlay", "tmpfs", "devtmpfs", "proc", "sysfs", "cgroup2"} {
		if !ephemeralFS[fs] {
			t.Errorf("%s 应被判为临时文件系统", fs)
		}
	}
}

func TestPathHasMountPrefix(t *testing.T) {
	cases := []struct {
		path, mnt string
		want      bool
	}{
		{"/backups", "/", true},
		{"/backups", "/backups", true},
		{"/backups/x", "/backups", true},
		{"/backups2", "/backups", false}, // 不是 /backups 的子路径
		{"/run/dev", "/run/udev", false},
		{"/var/lib/lockdown/x", "/var/lib/lockdown", true},
	}
	for _, c := range cases {
		if got := pathHasMountPrefix(c.path, c.mnt); got != c.want {
			t.Errorf("pathHasMountPrefix(%q,%q)=%v, 期望 %v", c.path, c.mnt, got, c.want)
		}
	}
}

func TestUnescapeMountField(t *testing.T) {
	if got := unescapeMountField(`/mnt/My\040Disk`); got != "/mnt/My Disk" {
		t.Errorf("空格转义还原失败: %q", got)
	}
}

func TestHasLocalBackup(t *testing.T) {
	tmp := t.TempDir()
	app := &application{configs: map[string]*backupConfig{}}
	// 覆盖默认备份根目录到临时目录（deviceBackupPath 用 cfg.BackupDirectory 优先）
	udid := "TESTUDID"
	app.configs[udid] = &backupConfig{BackupDirectory: tmp}

	if app.hasLocalBackup(udid) {
		t.Fatal("空目录应判为「无备份」")
	}
	// 制造一个备份标记
	if err := os.MkdirAll(filepath.Join(tmp, udid), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, udid, "Status.plist"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !app.hasLocalBackup(udid) {
		t.Fatal("有 Status.plist 应判为「有备份」")
	}
}
