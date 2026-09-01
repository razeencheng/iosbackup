package app

import (
	"context"
	"testing"
	"time"
)

// backup-list/info 会从设备下载清单，耗时随备份大小增长（30GB/2万条记录实测 ~21s）。
// 必须用「中等」超时（分钟级），不能用 12s 短超时，否则大备份/Wi-Fi 下会被 SIGKILL：
// "列出备份失败: signal: killed"。
func TestBackupQueriesUseMediumTimeout(t *testing.T) {
	cases := []struct {
		name string
		run  func(app *application, ctx context.Context) error
	}{
		{"list", func(app *application, ctx context.Context) error { _, err := app.BackupList(ctx, "U-T"); return err }},
		{"info", func(app *application, ctx context.Context) error { _, err := app.BackupInfo(ctx, "U-T"); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := newApplication()
			app.devices["U-T"] = &device{UDID: "U-T", IsOnline: true, Connection: connectionTypeDesc(connectTypeNetwork)}

			var remaining time.Duration
			var hadDeadline bool
			app.cmdRunner = func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
				if dl, ok := ctx.Deadline(); ok {
					remaining = time.Until(dl)
					hadDeadline = true
				}
				return []byte("Manifest.plist - 1.0 MB\n"), nil
			}

			if err := c.run(app, context.Background()); err != nil {
				t.Fatalf("%s 不应出错: %v", c.name, err)
			}
			if !hadDeadline {
				t.Fatalf("%s 应有有界超时（deadline）", c.name)
			}
			// >1min 证明用的是中等超时，而非 12s 短超时
			if remaining < time.Minute {
				t.Errorf("%s 应使用分钟级中等超时，实测剩余 %v（疑似仍是 12s 短超时）", c.name, remaining)
			}
		})
	}
}
