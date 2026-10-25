package app

import (
	"io"
	"time"
)

// 部署时区在启动时确定；历史时间转换只改变表示，不改变实际发生时刻。
func (app *application) deploymentLocation() *time.Location {
	if app.runtimeConfig.Location != nil {
		return app.runtimeConfig.Location
	}
	return beijingLocation
}
func (app *application) nowTime() time.Time { return time.Now().In(app.deploymentLocation()) }
func (app *application) inDeploymentTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.In(app.deploymentLocation())
}
func (app *application) formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return app.inDeploymentTime(value).Format(time.RFC3339)
}

// 时间窗按当地钟点计算；间隔另用 time.Sub 保持实际经过的小时数。
func inBackupWindow(now time.Time, start, end string) bool {
	first, err := time.Parse("15:04", start)
	if err != nil {
		return false
	}
	last, err := time.Parse("15:04", end)
	if err != nil {
		return false
	}
	currentMinute := now.Hour()*60 + now.Minute()
	firstMinute := first.Hour()*60 + first.Minute()
	lastMinute := last.Hour()*60 + last.Minute()
	if firstMinute < lastMinute {
		return currentMinute >= firstMinute && currentMinute < lastMinute
	}
	return currentMinute >= firstMinute || currentMinute < lastMinute
}

// 标准日志统一输出带偏移的部署时间，避免宿主机前缀与业务时间混用。
type deploymentLogWriter struct {
	output io.Writer
	now    func() time.Time
}

func (w deploymentLogWriter) Write(p []byte) (int, error) {
	prefix := w.now().Format(time.RFC3339) + " "
	n, err := w.output.Write(append([]byte(prefix), p...))
	n -= len(prefix)
	if n < 0 {
		n = 0
	}
	return n, err
}
