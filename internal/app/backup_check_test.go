package app

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func checkFixture(t *testing.T) (*application, backupIndexSource, string) {
	t.Helper()
	app, dir := indexSourceTestApp(t)
	for name, data := range map[string]string{
		"Manifest.plist": `<plist><dict><key>IsEncrypted</key><false/></dict></plist>`,
		"Info.plist":     `<plist><dict><key>Device Name</key><string>Phone</string></dict></plist>`,
		"Status.plist":   `<plist><dict><key>SnapshotState</key><string>finished</string></dict></plist>`,
		"Manifest.db":    "SQLite format 3\x00fixture",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	src, err := app.resolveBackupIndexSource(context.Background(), "OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	return app, src, dir
}
func checkRows(rows ...backupManifestRow) backupManifestRowStreamer {
	return func(_ context.Context, _ string, emit func(backupManifestRow) error) error {
		for _, row := range rows {
			if err := emit(row); err != nil {
				return err
			}
		}
		return nil
	}
}
func TestBackupCheckQuickAndDetailedCoverage(t *testing.T) {
	_, src, dir := checkFixture(t)
	quick := runBackupCheck(context.Background(), src, "quick", nil, nil)
	if quick.State != "passed" || !quick.MetadataComplete || quick.ManifestComplete {
		t.Fatalf("quick=%+v", quick)
	}
	id := strings.Repeat("a", 40)
	if err := os.Mkdir(filepath.Join(dir, "aa"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aa", id), []byte("bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	full := runBackupCheck(context.Background(), src, "manifest", nil, checkRows(backupManifestRow{FileID: id, Flags: 1}, backupManifestRow{FileID: strings.Repeat("b", 40), Flags: 2}, backupManifestRow{FileID: strings.Repeat("c", 40), Flags: 4}))
	if full.State != "passed" || !full.ManifestComplete || full.RecordsChecked != 3 || full.ObjectsChecked != 1 {
		t.Fatalf("full=%+v", full)
	}
	if err := os.Remove(filepath.Join(dir, "aa", id)); err != nil {
		t.Fatal(err)
	}
	missing := runBackupCheck(context.Background(), src, "manifest", nil, checkRows(backupManifestRow{FileID: id, Flags: 1}))
	if missing.State != "issues" || missing.MissingObjects != 1 || !missing.ManifestComplete {
		t.Fatalf("missing=%+v", missing)
	}
}
func TestBackupCheckMetadataDistinguishesMissingFalseAndIncomplete(t *testing.T) {
	for _, tc := range []struct{ name, file, body, state string }{
		{"missing encryption", "Manifest.plist", `<plist><dict/></plist>`, "issues"},
		{"bad encryption", "Manifest.plist", `<plist><dict><key>IsEncrypted</key><string>false</string></dict></plist>`, "issues"},
		{"unfinished", "Status.plist", `<plist><dict><key>SnapshotState</key><string>in_progress</string></dict></plist>`, "issues"},
		{"missing state", "Status.plist", `<plist><dict/></plist>`, "issues"},
		{"invalid xml", "Info.plist", `broken`, "issues"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, src, dir := checkFixture(t)
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			got := runBackupCheck(context.Background(), src, "quick", nil, nil)
			if got.State != tc.state {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}
func TestBackupCheckEncryptedAndUnsupportedRecords(t *testing.T) {
	_, src, dir := checkFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "Manifest.plist"), []byte(`<plist><dict><key>IsEncrypted</key><true/></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := runBackupCheck(context.Background(), src, "quick", nil, nil); got.State != "passed" {
		t.Fatalf("encrypted quick=%+v", got)
	}
	if got := runBackupCheck(context.Background(), src, "manifest", nil, nil); got.State != "unavailable" || got.Reason != "encrypted_manifest" || !got.MetadataComplete {
		t.Fatalf("encrypted scan=%+v", got)
	}
	_, src, _ = checkFixture(t)
	got := runBackupCheck(context.Background(), src, "manifest", nil, checkRows(backupManifestRow{FileID: strings.Repeat("a", 40), Flags: 8}))
	if got.State != "unavailable" || got.UnsupportedRecords != 1 || got.ManifestComplete {
		t.Fatalf("unknown flags=%+v", got)
	}
	got = runBackupCheck(context.Background(), src, "manifest", nil, checkRows(backupManifestRow{FileID: "../escape", Flags: 1}))
	if got.State != "issues" || got.InvalidRecords != 1 {
		t.Fatalf("invalid fileID=%+v", got)
	}
}
func TestBackupCheckCancellationAndSourceSafety(t *testing.T) {
	_, src, dir := checkFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := runBackupCheck(ctx, src, "manifest", nil, nil); got.State != "unavailable" || got.Reason != "cancelled" || got.ManifestComplete {
		t.Fatalf("cancel=%+v", got)
	}
	if err := os.Remove(filepath.Join(dir, "Info.plist")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(dir, "Info.plist")); err != nil {
		t.Fatal(err)
	}
	if got := runBackupCheck(context.Background(), src, "quick", nil, nil); got.State == "passed" {
		t.Fatalf("unsafe=%+v", got)
	}
}

func TestBackupCheckBinaryMetadataAndBoundedRead(t *testing.T) {
	_, src, dir := checkFixture(t)
	xml, err := os.ReadFile(filepath.Join(dir, "Status.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "Status.plist"), []byte("bplist00fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	converted := 0
	converter := func(ctx context.Context, data []byte) ([]byte, error) { converted++; return xml, nil }
	if got := runBackupCheck(context.Background(), src, "quick", converter, nil); got.State != "passed" || converted != 1 {
		t.Fatalf("binary=%+v conversions=%d", got, converted)
	}
	converter = func(ctx context.Context, data []byte) ([]byte, error) {
		return make([]byte, backupIndexMaxPlistBytes+1), nil
	}
	if got := runBackupCheck(context.Background(), src, "quick", converter, nil); got.State != "unavailable" || got.Reason != "resource_limit" {
		t.Fatalf("oversize=%+v", got)
	}
}

func TestBackupCheckPartialScanAndChangedSource(t *testing.T) {
	_, src, dir := checkFixture(t)
	streamer := func(ctx context.Context, _ string, emit func(backupManifestRow) error) error {
		if err := emit(backupManifestRow{FileID: strings.Repeat("a", 40), Flags: 2}); err != nil {
			return err
		}
		return context.DeadlineExceeded
	}
	got := runBackupCheck(context.Background(), src, "manifest", nil, streamer)
	if got.State != "unavailable" || got.Reason != "timeout" || got.RecordsChecked != 1 || got.ManifestComplete {
		t.Fatalf("partial=%+v", got)
	}
	streamer = func(ctx context.Context, _ string, emit func(backupManifestRow) error) error {
		return os.WriteFile(filepath.Join(dir, "Status.plist"), []byte(`<plist><dict><key>SnapshotState</key><string>unfinished</string></dict></plist>`), 0600)
	}
	got = runBackupCheck(context.Background(), src, "manifest", nil, streamer)
	if got.State != "unavailable" || got.Reason != "source_changed" || got.Freshness != "stale" {
		t.Fatalf("changed=%+v", got)
	}
}

func TestBackupCheckManualBlocksWritesAndCanCancel(t *testing.T) {
	app, _, _ := checkFixture(t)
	t.Cleanup(app.closeBackupIndex)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	started := make(chan struct{})
	app.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	id, err := app.startBackupCheck("OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, _, err := app.acquireLocalBackupOp("OFFLINE", true); err == nil {
		t.Fatal("write admitted during check")
	}
	app.mu.RLock()
	run := app.backupChecks["OFFLINE"]
	app.mu.RUnlock()
	if run.id != id {
		t.Fatal("identity changed")
	}
	run.cancel()
	<-run.done
	app.mu.RLock()
	busy := app.backupInProgress["OFFLINE"]
	result := app.backupCheckResults["OFFLINE"]
	app.mu.RUnlock()
	if busy || result.State != "unavailable" || result.Reason != "cancelled" {
		t.Fatalf("busy=%t result=%+v", busy, result)
	}
	syncJobs(t, app.backupJobRecords)
	records := app.backupJobRecords.list("OFFLINE")
	if len(records) != 1 || records[0].Operation != "check" || records[0].Check == nil {
		t.Fatalf("record=%+v", records)
	}
}

func TestBackupCheckShutdownCancelsManualScan(t *testing.T) {
	app, _, _ := checkFixture(t)
	started := make(chan struct{})
	app.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := app.startBackupCheck("OFFLINE"); err != nil {
		t.Fatal(err)
	}
	<-started
	app.closeBackupIndex()
	app.mu.RLock()
	defer app.mu.RUnlock()
	if app.backupInProgress["OFFLINE"] {
		t.Fatal("shutdown left check busy")
	}
}

func TestBackupCheckMissingToolsAreUnavailable(t *testing.T) {
	_, src, dir := checkFixture(t)
	missing := &os.PathError{Op: "exec", Path: "missing-tool", Err: os.ErrNotExist}
	got := runBackupCheck(context.Background(), src, "manifest", nil, func(context.Context, string, func(backupManifestRow) error) error { return missing })
	if got.State != "unavailable" || got.Reason != "read_failed" {
		t.Fatalf("sqlite missing=%+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "Status.plist"), []byte("bplist00fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	got = runBackupCheck(context.Background(), src, "quick", func(context.Context, []byte) ([]byte, error) { return nil, missing }, nil)
	if got.State != "unavailable" || got.Reason != "read_failed" {
		t.Fatalf("plistutil missing=%+v", got)
	}
}

func TestBackupCheckKeepsFrozenBackupSource(t *testing.T) {
	app, src, _ := checkFixture(t)
	original := cloneBackupConfig(app.configs["OFFLINE"])
	app.configs["OFFLINE"].BackupDirectory = t.TempDir()
	result := app.quickCheckBackup(&backupJob{id: "bkp-000000000001", udid: "OFFLINE", config: original})
	if result.State != "passed" || result.SourceKey != src.Key || result.Freshness != "stale" {
		t.Fatalf("frozen source=%+v", result)
	}
}

func TestBackupCheckSourceSwitchDuringManualScan(t *testing.T) {
	app, src, _ := checkFixture(t)
	t.Cleanup(app.closeBackupIndex)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	started, release := make(chan struct{}), make(chan struct{})
	app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error {
		close(started)
		<-release
		return nil
	}
	id, err := app.startBackupCheck("OFFLINE")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	app.mu.Lock()
	run := app.backupChecks["OFFLINE"]
	app.configs["OFFLINE"].BackupDirectory = t.TempDir()
	app.mu.Unlock()
	app.invalidateBackupIndexDevice("OFFLINE")
	close(release)
	<-run.done
	records := app.backupJobRecords.list("OFFLINE")
	if len(records) != 1 || records[0].ID != id || records[0].Check.SourceKey != src.Key || records[0].Check.Freshness != "stale" {
		t.Fatalf("records=%+v", records)
	}
}

func TestBackupCheckSubprocessCancellationPreservesReason(t *testing.T) {
	_, src, dir := checkFixture(t)
	tool := filepath.Join(t.TempDir(), "wait-sqlite")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ntrap 'exit 1' INT\nwhile :; do sleep 0.02; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := runBackupCheck(ctx, src, "manifest", nil, func(ctx context.Context, _ string, emit func(backupManifestRow) error) error {
		return streamBackupManifestRowsCommand(ctx, tool, filepath.Join(dir, "Manifest.db"), emit)
	})
	if result.State != "unavailable" || result.Reason != "timeout" {
		t.Fatalf("subprocess=%+v", result)
	}
}

func TestBackupCheckHTTPProtectionAndIdentity(t *testing.T) {
	app, _, _ := checkFixture(t)
	t.Cleanup(app.closeBackupIndex)
	app.authManager = testAuthManager("check-test")
	handler := app.setupRoutes()
	for _, authorized := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/api/backup-check/OFFLINE", strings.NewReader(`{"action":"start"}`))
		if authorized {
			req.SetBasicAuth("iosbackup", "check-test")
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		want := 401
		if authorized {
			want = 403
		}
		if rr.Code != want {
			t.Fatalf("auth status=%d", rr.Code)
		}
	}
	rr := backupControlRequest(t, app, "/api/backup-check/OFFLINE", `{"action":"cancel","id":"bkp-000000000001"}`)
	if rr.Code != 409 {
		t.Fatalf("stale cancel=%d", rr.Code)
	}
}

func TestBackupCheckCorruptSQLiteIsAnIssue(t *testing.T) {
	_, src, dir := checkFixture(t)
	tool := filepath.Join(t.TempDir(), "bad-sqlite")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'Error: database disk image is malformed\\n' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	result := runBackupCheck(context.Background(), src, "manifest", nil, func(ctx context.Context, _ string, emit func(backupManifestRow) error) error {
		return streamBackupManifestRowsCommand(ctx, tool, filepath.Join(dir, "Manifest.db"), emit)
	})
	if result.State != "issues" || result.Reason != "metadata_invalid" || result.ManifestComplete {
		t.Fatalf("corrupt=%+v", result)
	}
}
