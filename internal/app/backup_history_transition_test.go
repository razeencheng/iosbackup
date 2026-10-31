package app

import (
	"context"
	"encoding/json"
	"errors"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func interruptInitialHistory(t *testing.T, app *application) {
	t.Helper()
	app.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(app.paths.BackupsRoot, "CONTROL"), "finished", "original")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		err := persistence.WriteFileAtomic(path, data, mode)
		if err == nil && app.backupStorage.locations.Devices["CONTROL"].Phase == "enabling" {
			cancel()
		}
		return err
	}
	err := app.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil)
	app.backupStorage.write = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupt: %v", err)
	}
}

func TestHistoryEnablePersistsIntentAndKeepsOriginalSource(t *testing.T) {
	app := historyTestApp(t)
	interruptInitialHistory(t, app)
	loaded, err := app.configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded["CONTROL"].History.Enabled {
		t.Fatal("requested policy lost on interrupted enable")
	}
	root, release, err := app.acquireHistoryRead("CONTROL", app.paths.BackupsRoot)
	if err != nil {
		t.Fatal("original source unavailable during pending transition", err)
	}
	release()
	data, err := os.ReadFile(filepath.Join(root, "CONTROL", "payload"))
	if err != nil || string(data) != "original" {
		t.Fatal("original source lost", err)
	}
	if err = app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if len(historyPoints(t, app)) == 0 {
		t.Fatal("retry did not preserve the original backup")
	}
}

func TestHistoryOldInterruptedEnableDoesNotSwitchToEmptyCurrent(t *testing.T) {
	app := historyTestApp(t)
	interruptInitialHistory(t, app)
	// 模拟修复前留下的配置关闭、迁移未完成状态。
	app.configs["CONTROL"].History.Enabled = false
	job := &backupJob{udid: "CONTROL", id: newBackupSessionID(), config: cloneBackupConfig(app.configs["CONTROL"]), ctx: context.Background(), request: backupRequest{Mode: "auto"}, historyPolicyOnly: true}
	run, err := app.prepareBackupHistory(job, app.paths.BackupsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(run.work.Directory, "payload"))
	if err != nil || string(data) != "original" {
		t.Fatal("interrupted enable replaced the current source with an empty copy", err)
	}
	if run.state.WritableGeneration.Kind != "unencrypted" {
		t.Fatal("legacy source encryption metadata lost during recovery")
	}
}

func TestHistoryMigrationDoesNotBlockOtherSettings(t *testing.T) {
	app := historyTestApp(t)
	app.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(app.paths.BackupsRoot, "CONTROL"), "finished", "original")
	app.backupStorage.slot <- struct{}{}
	started := make(chan struct{})
	app.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		err := persistence.WriteFileAtomic(path, data, mode)
		if app.backupStorage.locations.Devices["CONTROL"].Phase == "enabling" {
			select {
			case <-started:
			default:
				close(started)
			}
		}
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not start")
	}
	saved := make(chan int, 1)
	go func() {
		saved <- backupControlRequest(t, app, "/api/save-config/CONTROL", `{"min_battery_level":42}`).Code
	}()
	blocked := false
	select {
	case code := <-saved:
		if code != http.StatusOK {
			t.Errorf("save: %d", code)
		}
	case <-time.After(300 * time.Millisecond):
		blocked = true
	}
	<-app.backupStorage.slot
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if blocked {
		<-saved
		t.Fatal("long migration held the configuration lock")
	}
	if app.configs["CONTROL"].MinBatteryLevel != 42 {
		t.Fatal("migration overwrote concurrently saved settings")
	}
}

func TestHistoryPolicyHTTPReturnsBeforeCopyFinishes(t *testing.T) {
	app := historyTestApp(t)
	app.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(app.paths.BackupsRoot, "CONTROL"), "finished", "original")
	if err := app.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeHistoryHTTP)
	h := app.setupRoutes()
	body := map[string]any{"policy": backupstore.Policy{Enabled: true, Days: 3}}
	w := historyRequest(t, app, h, "POST", "/api/backup-history/CONTROL/policy-preview", body)
	var preview struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	body["token"] = preview.Token
	app.backupStorage.slot <- struct{}{}
	responses := make(chan int, 1)
	go func() { responses <- historyRequest(t, app, h, "PUT", "/api/backup-history/CONTROL/policy", body).Code }()
	select {
	case code := <-responses:
		<-app.backupStorage.slot
		if code != http.StatusAccepted {
			t.Fatalf("expected background acceptance, got %d", code)
		}
	case <-time.After(300 * time.Millisecond):
		<-app.backupStorage.slot
		<-responses
		t.Fatal("policy request waited for full backup copy")
	}
}

func TestHistoryPolicyContinuesAfterRequestCancellation(t *testing.T) {
	app := historyTestApp(t)
	app.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(app.paths.BackupsRoot, "CONTROL"), "finished", "original")
	app.backupStorage.slot <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := app.startHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil)
	if err != nil {
		<-app.backupStorage.slot
		t.Fatal(err)
	}
	cancel()
	<-app.backupStorage.slot
	service := app.historyService()
	service.mu.Lock()
	done := service.policies["CONTROL"].done
	service.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("policy task did not finish")
	}
	if app.configs["CONTROL"].HistoryPolicyPending {
		t.Fatal("client cancellation interrupted the background task")
	}
	if status := app.historyPolicyStatus("CONTROL", app.configs["CONTROL"]); status.State != "succeeded" || status.ID == "" {
		t.Fatal("completed policy operation is not visible", status)
	}
	if len(historyPoints(t, app)) != 1 {
		t.Fatal("original backup not imported")
	}
}

func TestHistoryPendingPolicyResumesAfterRestart(t *testing.T) {
	app := historyTestApp(t)
	interruptInitialHistory(t, app)
	app.closeBackupIndex()
	if err := app.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := app.configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := loaded["CONTROL"]
	app.configs["CONTROL"] = &cfg
	if err = app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	job := &backupJob{udid: "CONTROL", id: newBackupSessionID(), config: cloneBackupConfig(&cfg), ctx: context.Background(), request: backupRequest{Mode: "auto"}}
	run, err := app.prepareBackupHistory(job, app.paths.BackupsRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer run.store.AbortWork(context.Background(), run.work.ID)
	if app.configs["CONTROL"].HistoryPolicyPending {
		t.Fatal("recovered policy did not finish")
	}
	if len(historyPoints(t, app)) != 1 {
		t.Fatal("original source lost during recovery")
	}
}

func TestHistoryPolicyFinalSaveFailureCanRetryWithoutDuplicateImport(t *testing.T) {
	app := historyTestApp(t)
	app.configs["CONTROL"].History = backupstore.Policy{Days: 3}
	writeHistoryBackup(t, filepath.Join(app.paths.BackupsRoot, "CONTROL"), "finished", "original")
	app.configStore.writeAtomic = func(path string, data []byte, mode os.FileMode) error {
		var envelope backupConfigEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		if !envelope.Configs["CONTROL"].HistoryPolicyPending {
			return errors.New("injected final config save failure")
		}
		return persistence.WriteFileAtomic(path, data, mode)
	}
	policy := backupstore.Policy{Enabled: true, Days: 3}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err == nil {
		t.Fatal("fault not reached")
	}
	app.configStore.writeAtomic = nil
	if !app.configs["CONTROL"].HistoryPolicyPending {
		t.Fatal("pending intent lost")
	}
	before := len(historyPoints(t, app))
	if before != 1 {
		t.Fatal("import was not durable", before)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	if len(historyPoints(t, app)) != before {
		t.Fatal("retry imported the same source twice")
	}
}

func TestHistoryPolicyRetryOnlyResumesSavedIntent(t *testing.T) {
	app := historyTestApp(t)
	interruptInitialHistory(t, app)
	if err := app.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeHistoryHTTP)
	h := app.setupRoutes()
	w := historyRequest(t, app, h, "POST", "/api/backup-history/CONTROL/policy-retry", map[string]any{})
	if w.Code != http.StatusAccepted {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	service := app.historyService()
	service.mu.Lock()
	done := service.policies["CONTROL"].done
	service.mu.Unlock()
	<-done
	if app.configs["CONTROL"].HistoryPolicyPending || len(historyPoints(t, app)) != 1 {
		t.Fatal("retry did not finish saved intent")
	}
	w = historyRequest(t, app, h, "POST", "/api/backup-history/CONTROL/policy-retry", map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatal("retry without pending intent", w.Code)
	}
}

func TestHistoryPolicyRecoversUncertainStoreOnlyWithoutReaders(t *testing.T) {
	app, _, point := historyHTTPFixture(t)
	store, err := app.backupStorage.store(point.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.Acquire(point.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	work, err := store.NewWork("interrupted-commit")
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, work.Directory, "finished", "pending work")
	path := filepath.Join(filepath.Dir(filepath.Dir(work.Parent)), "descriptors", work.ID+".json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(context.Background(), work.ID, point); !errors.Is(err, backupstore.ErrUncertain) {
		t.Fatal("fault did not interrupt commit", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	app.configs["CONTROL"].HistoryPolicyPending = true
	policy := app.configs["CONTROL"].History
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); !errors.Is(err, backupstore.ErrBusy) {
		t.Fatal("recovery must respect readers", err)
	}
	lease.Close()
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal("recovery required a server restart", err)
	}
	if len(historyPoints(t, app)) != 1 {
		t.Fatal("recovery changed committed points")
	}
	if err := store.AbortWork(context.Background(), work.ID); err != nil {
		t.Fatal("uncommitted work not retained", err)
	}
}
