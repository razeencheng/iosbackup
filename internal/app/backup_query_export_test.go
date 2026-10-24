package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func csvCapacityRunner(rows int) streamRunnerFunc {
	return func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		row := strings.Repeat("x", 1014) + ",domain,1\n"
		for i := 0; i < rows; i++ {
			if _, err := io.WriteString(out, row); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestBackupQueryCSVLargeList(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	const rows = 100000
	row := strings.Repeat("x", 80) + ",domain,1\n"
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		for i := 0; i < rows; i++ {
			if _, err := io.WriteString(out, row); err != nil {
				return err
			}
		}
		return nil
	})
	started := time.Now()
	file, size, err := app.prepareBackupCSV(context.Background(), "QUERY", false)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want := int64(rows * len(row))
	actual, err := io.Copy(io.Discard, file)
	if err != nil || size != want || actual != want {
		t.Fatalf("size=%d actual=%d want=%d error=%v", size, actual, want, err)
	}
	t.Logf("rows=%d csv_bytes=%d elapsed=%s", rows, size, time.Since(started))
}

func TestBackupQueryCSVDefaultCapacity(t *testing.T) {
	for _, rows := range []int{32768, 32769} {
		app, _ := queryHTTPFixture(t)
		app.streamCmdRunner = csvCapacityRunner(rows)
		file, size, err := app.prepareBackupCSV(context.Background(), "QUERY", false)
		if rows == 32768 {
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if size != maxBackupCSVBytes {
				t.Fatalf("size=%d", size)
			}
		} else {
			if !errors.Is(err, errQueryOutputLimit) || file != nil {
				t.Fatalf("oversize produced an export: size=%d error=%v", size, err)
			}
		}
	}
}

func TestBackupQueryCSVSlowClientDeadline(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	app.streamCmdRunner = csvCapacityRunner(32768)
	app.backupCSV.writeTimeout = 30 * time.Millisecond
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); app.handleBackupList(w, r) }))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	io.WriteString(conn, "GET /api/backup-list/QUERY?format=csv HTTP/1.1\r\nHost: localhost\r\n\r\n")
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	// 不消费响应正文，让真实 TCP 写入被响应级期限中止。
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow client retained export slot")
	}
	if app.backupCSV.gate.inUse() != 0 {
		t.Fatal("write deadline did not release slot")
	}
}

type csvTestResponse struct{ *httptest.ResponseRecorder }

func (csvTestResponse) SetWriteDeadline(time.Time) error { return nil }

func TestBackupQueryCSVPreservesAdmissionError(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	app.connectionRecoveryActive = true
	w := csvTestResponse{httptest.NewRecorder()}
	app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusConflict || payload["code"] != "api.connection_restarting" || payload["diagnostic"] != nil {
		t.Fatalf("admission error reclassified as CSV storage failure: %d %s", w.Code, w.Body)
	}
	if app.backupCSV.gate.inUse() != 0 {
		t.Fatal("admission failure retained export slot")
	}
}

func TestBackupQueryCSVFailureBeforeHeaders(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		io.WriteString(out, "one,domain,1\ntwo,domain,2\nERROR: Could not perform list (ErrorCode 207)\n")
		return errors.New("exit status 49")
	})
	w := csvTestResponse{httptest.NewRecorder()}
	app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
	if w.Code == 200 || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("failure started a download: %d %v %s", w.Code, w.Header(), w.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failure is not JSON: %s", w.Body)
	}
	if payload["diagnostic"] == nil {
		t.Fatalf("missing diagnostic: %s", w.Body)
	}
}

type failingCSVFile struct {
	backupCSVFile
	writeError, seekError error
	closed                bool
}

func (f *failingCSVFile) Write(p []byte) (int, error) {
	if f.writeError != nil {
		return 0, f.writeError
	}
	return f.backupCSVFile.Write(p)
}
func (f *failingCSVFile) Seek(n int64, whence int) (int64, error) {
	if f.seekError != nil {
		return 0, f.seekError
	}
	return f.backupCSVFile.Seek(n, whence)
}
func (f *failingCSVFile) Close() error { f.closed = true; return f.backupCSVFile.Close() }

func TestBackupQueryCSVResourceFailures(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		limit                        int64
		createErr, writeErr, seekErr error
		status                       int
	}{
		{"create", 1024, syscall.ENOSPC, nil, nil, 507},
		{"write", 1024, nil, syscall.ENOSPC, nil, 507},
		{"seek", 1024, nil, nil, errors.New("seek failed"), 500},
		{"limit", 4, nil, nil, nil, 507},
		{"exact_limit", 13, nil, nil, nil, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := queryHTTPFixture(t)
			app.backupCSV.limit = tc.limit
			var file *failingCSVFile
			app.backupCSV.create = func() (backupCSVFile, error) {
				if tc.createErr != nil {
					return nil, tc.createErr
				}
				f, err := createPrivateBackupCSV()
				if err != nil {
					return nil, err
				}
				file = &failingCSVFile{backupCSVFile: f, writeError: tc.writeErr, seekError: tc.seekErr}
				return file, nil
			}
			app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
				return []byte("one,domain,1\n"), nil
			}
			w := csvTestResponse{httptest.NewRecorder()}
			app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body)
			}
			if w.Code != 200 && w.Header().Get("Content-Disposition") != "" {
				t.Fatal("failure leaked download headers")
			}
			if file != nil && !file.closed {
				t.Fatal("temporary file not closed")
			}
			if app.backupCSV.gate.inUse() != 0 {
				t.Fatal("export slot not released")
			}
		})
	}
}

func TestBackupQueryCSVPrivateTemporaryFile(t *testing.T) {
	f, err := createPrivateBackupCSV()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	file := f.(*os.File)
	stat, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v", stat.Mode())
	}
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary path remains visible: %v", err)
	}
}

func TestBackupQueryCSVBusyCancellationAndReuse(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	started := make(chan struct{})
	app.streamCmdRunner = streamRunnerFunc(func(ctx context.Context, _ string, _, _ []string, _, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.handleBackupList(csvTestResponse{httptest.NewRecorder()}, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil).WithContext(ctx))
	}()
	<-started
	w := csvTestResponse{httptest.NewRecorder()}
	app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("missing bounded rejection: %d", w.Code)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled export did not finish")
	}
	app.streamCmdRunner = streamRunnerFunc(func(context.Context, string, []string, []string, io.Writer, io.Writer) error { return nil })
	w = csvTestResponse{httptest.NewRecorder()}
	app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
	if w.Code != 200 || w.Header().Get("Content-Length") != "0" || app.backupCSV.gate.inUse() != 0 {
		t.Fatalf("slot not reusable: %d %v", w.Code, w.Header())
	}
}

func TestBackupQueryCSVClientDisconnect(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		row := strings.Repeat("x", 1024) + ",domain,1\n"
		for i := 0; i < 8000; i++ {
			if _, err := io.WriteString(out, row); err != nil {
				return err
			}
		}
		return nil
	})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); app.handleBackupList(w, r) }))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/backup-list/QUERY?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	var prefix [128]byte
	response.Body.Read(prefix[:])
	response.Body.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected export did not release resources")
	}
	if app.backupCSV.gate.inUse() != 0 {
		t.Fatal("disconnected client retained export slot")
	}
}

func TestBackupQueryCSVWaitsForCompletion(t *testing.T) {
	app, _ := queryHTTPFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	app.streamCmdRunner = streamRunnerFunc(func(_ context.Context, _ string, _, _ []string, out, _ io.Writer) error {
		io.WriteString(out, "one,domain,1\n")
		close(started)
		<-release
		return nil
	})
	w := csvTestResponse{httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.handleBackupList(w, httptest.NewRequest("GET", "/api/backup-list/QUERY?format=csv", nil))
	}()
	<-started
	// runner 在此时阻塞，查询已经输出数据但尚未成功。
	if w.Body.Len() != 0 || w.Header().Get("Content-Disposition") != "" {
		close(release)
		<-done
		t.Fatal("CSV was exposed before completion")
	}
	close(release)
	<-done
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "one,domain,1" || w.Header().Get("Content-Length") != "13" {
		t.Fatalf("bad completed download: %d %v %s", w.Code, w.Header(), w.Body)
	}
}
