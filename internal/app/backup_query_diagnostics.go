package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const queryRecordLimit = 64 << 10
const querySummaryLimit = 4 << 10

var errQueryOutputLimit = errors.New("backup query output exceeds limit")
var errQueryParse = errors.New("invalid backup query record")
var errQueryIncomplete = fmt.Errorf("%w: incomplete record", errQueryParse)

var queryDiagnosticLine = regexp.MustCompile(`(?i)^(?:ERROR: (?:Could not |Unable to |Failed |Invalid |Cannot |No |Device |Backup |Password |Error)|ErrorCode[: =]+-?\d+|Could not perform (?:list|info):|Could not perform backup protocol version exchange, error code |Error requesting backup (?:info|list) from device, error code |Could not read Info\.plist|Failed to (?:read|open|connect)|Invalid password)`)
var queryDeviceCode = regexp.MustCompile(`(?i)(?:ErrorCode\s*[:=]?\s*|MBErrorDomain/)(-?\d+)`)
var queryDomainCode = regexp.MustCompile(`MBErrorDomain/-?\d+`)
var queryNamedPath = regexp.MustCompile(`(?i)\b(?:file|path|directory)\s*[:=]\s*[^\r\n]+`)
var queryBareFile = regexp.MustCompile(`[\pL\pN_-]+\.[\pL][\pL\pN_-]*`)

// 在原文上合并所有匹配区间后一次遮盖，避免先替换短值而拆开相交的秘密。
func redactQueryValues(line string, values []string) string {
	covered := make([]bool, len(line))
	for _, value := range values {
		if value == "" {
			continue
		}
		for offset := 0; offset < len(line); {
			n := strings.Index(line[offset:], value)
			if n < 0 {
				break
			}
			n += offset
			for i := n; i < n+len(value); i++ {
				covered[i] = true
			}
			offset = n + 1
		}
	}
	var result strings.Builder
	for i := 0; i < len(line); {
		if !covered[i] {
			result.WriteByte(line[i])
			i++
			continue
		}
		result.WriteString("[redacted]")
		for i < len(line) && covered[i] {
			i++
		}
	}
	return result.String()
}

type backupQueryDiagnostic struct {
	Operation       string `json:"operation"`
	Stage           string `json:"stage"`
	Reason          string `json:"reason"`
	Connection      string `json:"connection,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	DeviceErrorCode *int64 `json:"device_error_code,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
	Summary         string `json:"summary,omitempty"`
	OutputTruncated bool   `json:"output_truncated"`
}

type backupQueryError struct {
	diagnostic backupQueryDiagnostic
	cause      error
}

func (e *backupQueryError) Error() string {
	return fmt.Sprintf("Backup %s failed (%s): %s", e.diagnostic.Operation, e.diagnostic.Reason, e.diagnostic.Summary)
}
func (e *backupQueryError) Unwrap() error { return e.cause }

// 每个通道独占一个采集器；命令和消费协程退出后才汇总，避免共享可变缓冲。
type queryDiagnostics struct {
	line          []byte
	lineTruncated bool
	truncated     bool
	failed        bool
	encrypted     bool
	code          *int64
	lines         []string
	redactions    []string
	omitText      bool
}

func newQueryDiagnostics(udid, dir string, env []string) *queryDiagnostics {
	redactions := []string{udid, dir}
	omitText := false
	for _, entry := range env {
		if _, value, ok := strings.Cut(entry, "="); ok && value != "" {
			redactions = append(redactions, value)
			// 控制字符会让秘密跨诊断行或在 ANSI 清洗后变形，保守省略原文。
			omitText = omitText || strings.IndexFunc(value, unicode.IsControl) >= 0
		}
	}
	return &queryDiagnostics{redactions: redactions, omitText: omitText}
}

func (q *queryDiagnostics) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\r' || b == '\n' {
			q.flush()
			continue
		}
		if len(q.line) < queryRecordLimit {
			q.line = append(q.line, b)
		} else {
			q.lineTruncated = true
		}
	}
	return len(p), nil
}

func (q *queryDiagnostics) flush() {
	q.observe(string(q.line), q.lineTruncated)
	q.line = q.line[:0]
	q.lineTruncated = false
}

func (q *queryDiagnostics) observe(line string, truncated bool) bool {
	raw := line
	line = strings.TrimSpace(ansiRE.ReplaceAllString(line, ""))
	if !queryDiagnosticLine.MatchString(line) {
		q.truncated = q.truncated || truncated
		return false
	}
	if match := queryDeviceCode.FindStringSubmatch(line); len(match) == 2 {
		if code, err := strconv.ParseInt(match[1], 10, 64); err == nil && code != 0 && q.code == nil {
			q.code = &code
		}
		// ErrorCode 0 自身不是失败证据。
		if match[1] == "0" && strings.HasPrefix(strings.ToLower(line), "errorcode") {
			return true
		}
	}
	q.failed = true
	lower := strings.ToLower(line)
	q.encrypted = q.encrypted || strings.Contains(lower, "invalid password") || strings.Contains(lower, "could not decrypt") || strings.Contains(lower, "cannot decrypt")
	if q.omitText || truncated || len(line) > 1024 || len(q.lines) >= 16 {
		q.truncated = true
		return true
	}
	// 先删除完整秘密再截断；截断的原始行不投影，防止只剩密码前缀。
	line = strings.TrimSpace(ansiRE.ReplaceAllString(redactQueryValues(raw, q.redactions), ""))
	probe := queryDomainCode.ReplaceAllString(line, "")
	words := strings.Fields(probe)
	for i, word := range words {
		// 仅允许完整的工具格式名与服务名，不能用子串放行私人文件名。
		switch strings.Trim(word, "()[]:;,.\"'") {
		case "Info.plist", "Manifest.plist", "Status.plist", "Manifest.db", "com.apple.mobilebackup2", "com.apple.mobilebackup":
			words[i] = ""
		}
	}
	probe = strings.Join(words, " ")
	// 任意相对路径的起点可能包含空格，不能只遮盖第一个斜线之后的部分。
	if strings.ContainsAny(probe, "/\\\"'") || queryBareFile.MatchString(probe) {
		q.truncated = true
		q.lines = append(q.lines, "Tool diagnostic contained private text; original text omitted.")
		return true
	}
	line = queryNamedPath.ReplaceAllString(line, "path=[omitted]")
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(line, "�"))
	q.lines = append(q.lines, line)
	return true
}

func (app *application) finishBackupQuery(op, udid, connection string, started time.Time, runErr, consumeErr error, collectors ...*queryDiagnostics) error {
	// EOF 不完整记录可能由进程提前退出造成；请求取消也不是解析失败。
	if errors.Is(consumeErr, context.Canceled) || errors.Is(consumeErr, context.DeadlineExceeded) {
		runErr = errors.Join(runErr, consumeErr)
		consumeErr = nil
	} else if runErr != nil && (errors.Is(consumeErr, errQueryIncomplete) || ((errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) && !errors.Is(runErr, consumeErr))) {
		consumeErr = nil
	}
	d := backupQueryDiagnostic{Operation: op, Stage: "command", Connection: connection, DurationMS: time.Since(started).Milliseconds()}
	var lines []string
	failed, encrypted := false, false
	for _, q := range collectors {
		q.flush()
		failed = failed || q.failed
		encrypted = encrypted || q.encrypted
		d.OutputTruncated = d.OutputTruncated || q.truncated
		if d.DeviceErrorCode == nil {
			d.DeviceErrorCode = q.code
		}
		lines = append(lines, q.lines...)
	}
	if runErr == nil && consumeErr == nil && !failed {
		app.addDebugLog(udid, fmt.Sprintf("event=backup_query.completed operation=%s duration_ms=%d", op, d.DurationMS))
		return nil
	}
	cause := runErr
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() >= 0 {
		code := exit.ExitCode()
		d.ExitCode = &code
	}
	var pathErr *os.PathError
	var execErr *exec.Error
	var prepareErr *csvPreparationError
	switch {
	case errors.As(consumeErr, &prepareErr):
		cause = consumeErr
		d.Stage = "export_prepare"
		d.Reason = prepareErr.reason
		d.OutputTruncated = d.OutputTruncated || errors.Is(consumeErr, errQueryOutputLimit)
	case consumeErr != nil:
		cause = consumeErr
		d.Stage = "parse"
		d.Reason = "parse_failed"
		if errors.Is(consumeErr, errQueryOutputLimit) {
			d.Reason = "output_limit"
			d.OutputTruncated = true
		}
	case errors.Is(runErr, context.DeadlineExceeded):
		d.Reason = "timeout"
	case errors.Is(runErr, context.Canceled):
		d.Reason = "cancelled"
	case encrypted:
		d.Reason = "encrypted_query_failed"
	case d.DeviceErrorCode != nil:
		d.Reason = "device_error"
	case errors.As(runErr, &pathErr), errors.As(runErr, &execErr):
		d.Reason = "tool_unavailable"
	default:
		d.Reason = "command_failed"
	}
	if d.DeviceErrorCode != nil {
		lines = append([]string{fmt.Sprintf("Device ErrorCode %d", *d.DeviceErrorCode)}, lines...)
	}
	if len(lines) == 0 {
		lines = append(lines, "No recognized diagnostic was provided.")
	}
	d.Summary = strings.Join(lines, "\n")
	if len(d.Summary) > querySummaryLimit {
		d.Summary = strings.ToValidUTF8(d.Summary[:querySummaryLimit-3], "") + "..."
		d.OutputTruncated = true
	}
	queryErr := &backupQueryError{diagnostic: d, cause: cause}
	event := "backup_query.failed"
	if d.Stage == "export_prepare" {
		event = "backup_export.failed"
	}
	if d.Reason == "cancelled" {
		event = "backup_query.cancelled"
	}
	message := fmt.Sprintf("event=%s operation=%s stage=%s reason=%s connection=%s duration_ms=%d output_truncated=%t summary=%q", event, op, d.Stage, d.Reason, connection, d.DurationMS, d.OutputTruncated, d.Summary)
	if d.ExitCode != nil {
		message += fmt.Sprintf(" exit_code=%d", *d.ExitCode)
	}
	if d.DeviceErrorCode != nil {
		message += fmt.Sprintf(" device_error_code=%d", *d.DeviceErrorCode)
	}
	if d.Reason == "cancelled" {
		app.addDebugLog(udid, message)
	} else {
		app.addWarnLog(udid, message)
	}
	return queryErr
}
