package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsExplainBackupProgressSemantics(t *testing.T) {
	tests := []struct {
		path    string
		markers []string
	}{
		{
			path: "docs/manual/states.md",
			markers: []string{
				"## Backup progress semantics",
				"overall progress",
				"current file progress",
				"does not display the filename",
				"does not estimate remaining time",
				"waiting for device reconnection",
				"not resumed automatically",
			},
		},
		{
			path: "docs/manual/states.zh-CN.md",
			markers: []string{
				"## 备份进度说明",
				"整次备份的总体进度",
				"当前文件进度",
				"不显示文件名",
				"不估算剩余时间",
				"等待设备重新连接",
				"不会自动续传",
			},
		},
	}
	for _, test := range tests {
		content, err := os.ReadFile(filepath.Join(findModuleRoot(t), filepath.FromSlash(test.path)))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range test.markers {
			if !strings.Contains(string(content), want) {
				t.Errorf("%s missing backup progress semantics %q", test.path, want)
			}
		}
	}
}
