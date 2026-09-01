package app

import (
	"regexp"
	"testing"
)

func TestNewBackupSessionIDIsShortAndUnique(t *testing.T) {
	first := newBackupSessionID()
	second := newBackupSessionID()
	pattern := regexp.MustCompile(`^bkp-[0-9a-f]{12}$`)
	if !pattern.MatchString(first) || !pattern.MatchString(second) {
		t.Fatalf("session id 格式错误: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("连续生成的 session id 不应相同: %q", first)
	}
}

func TestBackupSessionLogPrefixesEveryMessage(t *testing.T) {
	got := backupSessionLog("bkp-0123456789ab", "使用 Wi-Fi 连接进行备份")
	want := "[session=bkp-0123456789ab] 使用 Wi-Fi 连接进行备份"
	if got != want {
		t.Fatalf("backupSessionLog=%q, want %q", got, want)
	}
}
