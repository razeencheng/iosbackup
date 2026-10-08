package app

import (
	"log"
	"os"
	"strings"
	"time"
)

// 日志级别定义
type logLevel int

const (
	logLevelDebug logLevel = iota
	logLevelInfo
	logLevelWarn
	logLevelError
)

// 日志级别字符串映射
var logLevelNames = map[logLevel]string{
	logLevelDebug: "DEBUG",
	logLevelInfo:  "INFO",
	logLevelWarn:  "WARN",
	logLevelError: "ERROR",
}

// 全局日志级别配置
var currentLogLevel logLevel

// 初始化日志级别（从环境变量读取）
func initLogLevel() {
	logLevelStr := strings.ToUpper(os.Getenv("LOG_LEVEL"))
	switch logLevelStr {
	case "DEBUG":
		currentLogLevel = logLevelDebug
	case "INFO":
		currentLogLevel = logLevelInfo
	case "WARN":
		currentLogLevel = logLevelWarn
	case "ERROR":
		currentLogLevel = logLevelError
	default:
		currentLogLevel = logLevelInfo // 默认级别
	}
	log.Printf("日志级别设置为: %s", logLevelNames[currentLogLevel])
}

// 设置东八区时区
var beijingLocation *time.Location

func init() {
	var err error
	beijingLocation, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 如果加载时区失败，手动创建东八区时区
		beijingLocation = time.FixedZone("CST", 8*60*60)
	}
}

// 获取北京时间的now
func nowBeijing() time.Time {
	return time.Now().In(beijingLocation)
}

// 将任意时间转换为北京时间
func toBeijingTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(beijingLocation)
}

// addLog 添加日志并输出到标准输出
func (app *application) addLog(udid, message string) {
	app.addLogWithLevel(udid, message, logLevelInfo)
}

// addLogWithLevel 添加指定级别的日志
func (app *application) addLogWithLevel(udid, message string, level logLevel) {
	defer func() {
		// 防止日志记录导致panic
		if r := recover(); r != nil {
			log.Printf("日志记录发生panic: %v", r)
		}
	}()

	// 检查日志级别过滤
	if level < currentLogLevel {
		return
	}

	logEntry := backupLogEntry{
		UDID:      udid,
		Timestamp: nowBeijing(),
		Status:    logLevelNames[level],
		Message:   message,
	}

	// 输出到标准输出（log包本身是线程安全的）
	log.Printf("[%s] %s %s: %s",
		logEntry.Timestamp.Format("2006-01-02 15:04:05"),
		logLevelNames[level],
		udid,
		message)
}

// 便捷方法
func (app *application) addDebugLog(udid, message string) {
	app.addLogWithLevel(udid, message, logLevelDebug)
}

func (app *application) addInfoLog(udid, message string) {
	app.addLogWithLevel(udid, message, logLevelInfo)
}

func (app *application) addWarnLog(udid, message string) {
	app.addLogWithLevel(udid, message, logLevelWarn)
}

func (app *application) addErrorLog(udid, message string) {
	app.addLogWithLevel(udid, message, logLevelError)
}
