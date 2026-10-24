package app

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"iosbackup/internal/i18n"
)

const maxBackupCSVBytes int64 = 32 << 20

type backupCSVFile interface {
	io.ReadWriteSeeker
	io.Closer
}
type backupCSVExporter struct {
	gate         *backupSem
	limit        int64
	writeTimeout time.Duration
	create       func() (backupCSVFile, error)
}

func newBackupCSVExporter() *backupCSVExporter {
	return &backupCSVExporter{gate: newBackupSem(1), limit: maxBackupCSVBytes, writeTimeout: 2 * time.Minute, create: createPrivateBackupCSV}
}

func createPrivateBackupCSV() (backupCSVFile, error) {
	f, err := os.CreateTemp(os.TempDir(), "iosbackup-list-*.csv")
	if err != nil {
		return nil, err
	}
	// Linux/macOS 上立即删除目录项，正常关闭或进程崩溃都会回收临时清单。
	if err = os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

type csvPreparationError struct {
	reason string
	cause  error
}

func (e *csvPreparationError) Error() string { return "CSV preparation failed: " + e.reason }
func (e *csvPreparationError) Unwrap() error { return e.cause }

type limitedCSVWriter struct {
	output             io.Writer
	remaining, written int64
}

func (w *limitedCSVWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, &csvPreparationError{reason: "output_limit", cause: errQueryOutputLimit}
	}
	n, err := w.output.Write(p)
	w.remaining -= int64(n)
	w.written += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return n, &csvPreparationError{reason: "export_storage_failed", cause: err}
	}
	return n, nil
}

func (app *application) prepareBackupCSV(ctx context.Context, udid string, empty bool) (backupCSVFile, int64, error) {
	f, err := app.backupCSV.create()
	if err != nil {
		return nil, 0, &csvPreparationError{reason: "export_storage_failed", cause: err}
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	output := &limitedCSVWriter{output: f, remaining: app.backupCSV.limit}
	w := csv.NewWriter(output)
	if !empty {
		_, err = app.streamBackupList(ctx, udid, func(fields []string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return w.Write(fields)
		})
		if err != nil {
			return nil, 0, err
		}
	}
	w.Flush()
	if err = w.Error(); err != nil {
		return nil, 0, err
	}
	if err = ctx.Err(); err != nil {
		return nil, 0, err
	}
	position, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, 0, &csvPreparationError{reason: "export_storage_failed", cause: err}
	}
	if position != 0 {
		return nil, 0, &csvPreparationError{reason: "export_storage_failed", cause: errors.New("CSV seek did not return to start")}
	}
	ok = true
	return f, output.written, nil
}

func (app *application) writeBackupCSVError(w http.ResponseWriter, r *http.Request, udid string, err error) {
	if r.Context().Err() != nil {
		return
	}
	var queryErr *backupQueryError
	if !errors.As(err, &queryErr) {
		var prepareErr *csvPreparationError
		if !errors.As(err, &prepareErr) {
			// 查询准入错误沿用原有机器码，不归类为临时存储故障。
			writeOperationError(w, http.StatusConflict, err, i18n.BackupListFailed)
			return
		}
		reason := prepareErr.reason
		queryErr = &backupQueryError{diagnostic: backupQueryDiagnostic{Operation: "export", Stage: "export_prepare", Reason: reason, Summary: "CSV preparation did not complete."}, cause: err}
		app.addWarnLog(udid, fmt.Sprintf("event=backup_export.failed reason=%s stage=export_prepare", reason))
	}
	status := http.StatusConflict
	if queryErr.diagnostic.Stage == "export_prepare" {
		status = http.StatusInternalServerError
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) || errors.Is(err, errQueryOutputLimit) {
		status = http.StatusInsufficientStorage
	}
	writeOperationError(w, status, queryErr, i18n.BackupListFailed)
}

type csvResponseWriter struct {
	context  context.Context
	response http.ResponseWriter
}

func (w csvResponseWriter) Write(p []byte) (int, error) {
	if err := w.context.Err(); err != nil {
		return 0, err
	}
	return w.response.Write(p)
}

func (app *application) serveBackupCSV(w http.ResponseWriter, r *http.Request, udid string, empty bool) {
	if !app.backupCSV.gate.tryAcquire() {
		w.Header().Set("Retry-After", "5")
		writeOperationError(w, http.StatusServiceUnavailable, &backupQueryError{diagnostic: backupQueryDiagnostic{Operation: "export", Stage: "admission", Reason: "export_busy"}}, i18n.BackupListFailed)
		return
	}
	defer app.backupCSV.gate.release()
	f, size, err := app.prepareBackupCSV(r.Context(), udid, empty)
	if err != nil {
		app.writeBackupCSVError(w, r, udid, err)
		return
	}
	defer f.Close()
	controller := http.NewResponseController(w)
	if err = controller.SetWriteDeadline(time.Now().Add(app.backupCSV.writeTimeout)); err != nil {
		app.writeBackupCSVError(w, r, udid, &csvPreparationError{reason: "export_storage_failed", cause: err})
		return
	}
	defer controller.SetWriteDeadline(time.Time{})
	if r.Context().Err() != nil {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "backup-list-" + udid + ".csv"}))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	n, err := io.CopyBuffer(csvResponseWriter{r.Context(), w}, io.LimitReader(f, size), make([]byte, 32<<10))
	// 显式 Flush 能观察到 net/http 缓冲区直到 handler 返回才出现的写入错误。
	if err == nil {
		err = controller.Flush()
	}
	if err != nil || n != size {
		message := fmt.Sprintf("event=backup_export.interrupted stage=download written=%d expected=%d", n, size)
		if r.Context().Err() != nil {
			app.addDebugLog(udid, message)
		} else {
			app.addWarnLog(udid, message)
		}
	}
}
