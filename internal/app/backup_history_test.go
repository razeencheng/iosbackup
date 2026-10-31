package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func historyTestApp(t *testing.T) *application {
	t.Helper()
	app := newBackupControlTestApp(t)
	if err := app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.closeBackupIndex()
		if err := app.backupStorage.close(); err != nil {
			t.Error(err)
		}
	})
	app.configs["CONTROL"].History = backupstore.Policy{Enabled: true, Days: 3}
	return app
}
func writeHistoryBackup(t *testing.T, dir, status, payload string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"Manifest.plist": `<plist><dict><key>IsEncrypted</key><false/></dict></plist>`, "Info.plist": `<plist><dict><key>Device Name</key><string>Phone</string></dict></plist>`, "Status.plist": `<plist><dict><key>SnapshotState</key><string>` + status + `</string></dict></plist>`, "Manifest.db": "SQLite format 3\x00fixture", "payload": payload} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func historyPoints(t *testing.T, app *application) map[string]backupstore.Point {
	t.Helper()
	all := map[string]backupstore.Point{}
	for _, s := range app.backupStorage.stores {
		c, e := s.Snapshot()
		if e != nil {
			t.Fatal(e)
		}
		for id, p := range c.Points {
			all[id] = p
		}
	}
	return all
}
func historyCommand(t *testing.T, app *application, status, payload string, exit int, seen *string) {
	t.Helper()
	app.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
		dest := args[len(args)-1]
		if seen != nil {
			*seen = dest
		}
		if !strings.Contains(dest, string(filepath.Separator)+"work"+string(filepath.Separator)) {
			t.Errorf("command not isolated: %s", dest)
		}
		writeHistoryBackup(t, filepath.Join(dest, "CONTROL"), status, payload)
		command := "printf 'Backup Successful.\\n'"
		if exit != 0 {
			command = "exit 1"
		}
		return newExecCmd(ctx, "/bin/sh", "-c", command)
	}
}
func TestBackupHistoryPipeline(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, mode := range []string{"auto", "full"} {
			t.Run(mode+map[bool]string{false: "/manual", true: "/automatic"}[automatic], func(t *testing.T) {
				app := historyTestApp(t)
				app.configs["CONTROL"].AutoBackupEnabled = true
				var first string
				historyCommand(t, app, "finished", "first", 0, &first)
				job, err := app.beginBackupJob("CONTROL", "", "", automatic, backupRequest{Mode: mode})
				if err != nil {
					t.Fatal(err)
				}
				if err = app.runBackupJob(job); err != nil {
					t.Fatal(err)
				}
				points := historyPoints(t, app)
				if len(points) != 1 {
					t.Fatalf("no published point: %+v", points)
				}
				var old backupstore.Point
				for _, p := range points {
					old = p
					if p.State != "normal" {
						t.Fatalf("%+v", p)
					}
				}
				store := app.backupStorage.stores[old.StoreID]
				c, _ := store.Snapshot()
				if err = store.SetPinned(context.Background(), old.ID, true, c.Revision); err != nil {
					t.Fatal(err)
				}
				var second string
				historyCommand(t, app, "finished", "second", 0, &second)
				if err = app.PerformBackup("CONTROL"); err != nil {
					t.Fatal(err)
				}
				if first == second {
					t.Fatal("work reused")
				}
				lease, err := store.Acquire(old.ID)
				if err != nil {
					t.Fatal(err)
				}
				f, err := lease.Root.Open("payload")
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(f)
				f.Close()
				lease.Close()
				if err != nil || string(data) != "first" {
					t.Fatal("old point overwritten", err)
				}
				src, err := app.resolveBackupIndexSource(context.Background(), "CONTROL")
				if err != nil {
					t.Fatal(err)
				}
				data, err = os.ReadFile(filepath.Join(src.Directory, "payload"))
				if err != nil || string(data) != "second" {
					t.Fatal("current source stale", err)
				}
			})
		}
	}
}
func TestBackupHistoryPendingAndFailed(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "good", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "in_progress", "pending", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "in_progress", "new pending", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	points := historyPoints(t, app)
	if len(points) != 2 {
		t.Fatalf("normal plus one pending wanted: %+v", points)
	}
	historyCommand(t, app, "finished", "failed", 1, nil)
	if err := app.PerformBackup("CONTROL"); err == nil {
		t.Fatal("failure lost")
	}
	if len(historyPoints(t, app)) != 2 {
		t.Fatal("failure changed points")
	}
	for _, s := range app.backupStorage.stores {
		r, err := s.Recover(context.Background())
		if err != nil || len(r.Uncommitted) != 0 {
			t.Fatalf("failed work not cleaned %+v %v", r, err)
		}
	}
}
func TestBackupHistoryDisableReenable(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "original", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	points := historyPoints(t, app)
	var old backupstore.Point
	for _, p := range points {
		old = p
	}
	app.configs["CONTROL"].History.Enabled = false
	historyCommand(t, app, "finished", "ordinary", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !historyPoints(t, app)[old.ID].Manual {
		t.Fatal("disable did not freeze")
	}
	app.configs["CONTROL"].History.Enabled = true
	historyCommand(t, app, "finished", "new epoch", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	points = historyPoints(t, app)
	if len(points) < 2 {
		t.Fatal("manual point removed")
	}
	for id, p := range points {
		if id != old.ID && p.EpochID == old.EpochID {
			t.Fatal("epoch reused")
		}
	}
	lease, err := app.backupStorage.stores[old.StoreID].Acquire(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	f, err := lease.Root.Open("payload")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "original" {
		t.Fatal("history mutated")
	}
}
func TestBackupHistoryShrinkNeedsPreview(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "good", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	app.configs["CONTROL"].History.Days = 1
	if err := app.PerformBackup("CONTROL"); !errors.Is(err, backupstore.ErrConflict) {
		t.Fatalf("unconfirmed shortening allowed: %v", err)
	}
}
func TestBackupHistoryLegacyMigration(t *testing.T) {
	app := historyTestApp(t)
	legacy := filepath.Join(app.paths.BackupsRoot, "CONTROL")
	writeHistoryBackup(t, legacy, "finished", "legacy")
	app.configs["CONTROL"].LastBackup = time.Now().AddDate(0, 0, -1)
	stamp := app.configs["CONTROL"].LastBackup.UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(legacy, "Status.plist"), []byte(`<plist><dict><key>SnapshotState</key><string>finished</string><key>Date</key><date>`+stamp+`</date></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "finished", "new", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if len(historyPoints(t, app)) != 2 {
		t.Fatal("legacy not adopted")
	}
	b, _ := os.ReadFile(filepath.Join(legacy, "payload"))
	if string(b) != "legacy" {
		t.Fatal("legacy overwritten")
	}
}

func TestBackupHistoryRestartReconcilesCommittedPoint(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "first", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	// Publish a complete point as if the process died before updating the app registry.
	st, _ := app.backupStorage.deviceState("CONTROL")
	s := app.backupStorage.stores[st.StoreID]
	work, _, err := app.backupStorage.prepare(context.Background(), st.StoreID, "crashed", st.LatestPoint, backupstore.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, work.Directory, "finished", "committed")
	c, _ := s.Snapshot()
	p := c.Points[st.LatestPoint]
	p.CompletedAt = time.Now()
	p.Day = p.CompletedAt.In(app.deploymentLocation()).Format("2006-01-02")
	committed, err := s.Commit(context.Background(), work.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	app.closeBackupIndex()
	if err = app.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	if err = app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := app.backupStorage.deviceState("CONTROL")
	if got.LatestPoint != committed.ID {
		t.Fatal("durable point not reconciled")
	}
	src, err := app.resolveBackupIndexSource(context.Background(), "CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(src.Directory, "payload"))
	if string(b) != "committed" {
		t.Fatal("recovered source is stale")
	}
}
func TestBackupHistoryLocalOperations(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "point", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	original := app.deviceBackupPath("CONTROL")
	if !strings.Contains(original, "/objects/") {
		t.Fatal("legacy source still exposed", original)
	}
	if err := app.DeleteDeviceBackup("CONTROL"); !errors.Is(err, backupstore.ErrConflict) {
		t.Fatal("legacy delete must not delete historical point", err)
	}
	app.manifestRows = checkRows()
	if err := app.Unback(context.Background(), "CONTROL"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(original, "_unback_")); !os.IsNotExist(err) {
		t.Fatal("unpack modified immutable point")
	}
	path, release, err := app.acquireLocalBackupOp("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if path != original {
		t.Fatal("manual check reads wrong source", path)
	}
}
func TestBackupHistoryRejectsManagedBackupRoot(t *testing.T) {
	app := historyTestApp(t)
	cfg := *app.configs["CONTROL"]
	cfg.BackupDirectory = filepath.Join(app.paths.BackupsRoot, ".iosbackup-history", "v1", "CONTROL", "objects", "point")
	if err := validateBackupConfig(cfg, []string{app.paths.BackupsRoot}); err == nil {
		t.Fatal("managed point accepted as writable root")
	}
}

func TestBackupHistoryStopDuringPreparation(t *testing.T) {
	app := historyTestApp(t)
	m := app.backupStorage
	m.slot <- struct{}{}
	reached := make(chan struct{})
	var once sync.Once
	m.write = func(path string, data []byte, mode os.FileMode) error {
		err := persistence.WriteFileAtomic(path, data, mode)
		if strings.Contains(string(data), `"phase":"active"`) {
			once.Do(func() { close(reached) })
		}
		return err
	}
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- app.runBackupJob(job) }()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation not reached")
	}
	if err = app.stopBackup("CONTROL", job.id); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, errBackupStoppedByUser) {
			t.Fatalf("preparation stop misclassified: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not finish")
	}
	<-m.slot
	if len(historyPoints(t, app)) != 0 {
		t.Fatal("stopped preparation published point")
	}
}
func TestBackupHistorySaveFailureKeepsExecutionSuccess(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "first", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	old, _ := app.backupStorage.deviceState("CONTROL")
	app.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		var registry historyLocations
		if err := json.Unmarshal(data, &registry); err != nil {
			return err
		}
		if registry.Devices["CONTROL"].LatestPoint != old.LatestPoint {
			return errors.New("disk full")
		}
		return persistence.WriteFileAtomic(path, data, mode)
	}
	historyCommand(t, app, "finished", "saved", 0, nil)
	job, err := app.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.runBackupJob(job); err != nil {
		t.Fatal("history failure replaced execution result", err)
	}
	if job.historyState != "uncertain" {
		t.Fatalf("state %s", job.historyState)
	}
	if len(historyPoints(t, app)) != 2 {
		t.Fatal("old point removed before application state persisted")
	}
}

func TestBackupHistoryBaselineAndNewGeneration(t *testing.T) {
	app := historyTestApp(t)
	count := 0
	app.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
		dir := filepath.Join(args[len(args)-1], "CONTROL")
		count++
		_, seedErr := os.Stat(filepath.Join(dir, "payload"))
		if count == 2 {
			if seedErr != nil || argsHas(args, "--full") {
				t.Error("compatible baseline was not reused")
			}
		} else {
			if !os.IsNotExist(seedErr) || !argsHas(args, "--full") {
				t.Error("missing/new generation baseline must start full and empty")
			}
		}
		writeHistoryBackup(t, dir, "finished", "data")
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	secrets, err := newAESSecretStore(testKey32(), filepath.Join(app.paths.ConfigsRoot, "secrets.enc"))
	if err != nil {
		t.Fatal(err)
	}
	app.secretStore = secrets
	app.backupGenerations.secrets = secrets
	app.cmdRunner = (&mockRunner{}).run
	if err = app.SetBackupEncryption(context.Background(), "CONTROL", true, "password"); err != nil {
		t.Fatal(err)
	}
	if err = app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
}
func TestBackupHistoryDisabledCurrentCanBeDeleted(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "point", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteDeviceBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if len(historyPoints(t, app)) != 1 {
		t.Fatal("current delete removed manual history")
	}
	if app.hasLocalBackup("CONTROL") {
		t.Fatal("deleted current still visible")
	}
	historyCommand(t, app, "finished", "new current", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !app.hasLocalBackup("CONTROL") {
		t.Fatal("could not back up after deleting current")
	}
}

func TestBackupHistoryRestoreWritesOnlyDisposableCopy(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "immutable", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	source := app.deviceBackupPath("CONTROL")
	var target string
	app.cmdRunner = func(_ context.Context, _ string, args, env []string) ([]byte, error) {
		for i, arg := range args {
			if arg == "restore" {
				target = filepath.Join(args[i+1], "CONTROL")
				if target == source {
					t.Error("restore received immutable source")
				}
				if err := os.WriteFile(filepath.Join(target, "payload"), []byte("changed by restore"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return nil, nil
	}
	if err := app.Restore(context.Background(), "CONTROL", "", restoreOptions{Reboot: true}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(source, "payload"))
	if err != nil || string(b) != "immutable" {
		t.Fatal("restore changed history", err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("restore work was not cleaned")
	}
}
func TestBackupHistoryDisabledPathChangeCopiesCurrent(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "finished", "newer current", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.configs["CONTROL"].BackupDirectory = filepath.Join(app.paths.BackupsRoot, "moved")
	app.mu.Unlock()
	app.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
		dir := filepath.Join(args[len(args)-1], "CONTROL")
		b, err := os.ReadFile(filepath.Join(dir, "payload"))
		if err != nil || string(b) != "newer current" {
			t.Errorf("path switch copied old point: %q %v", b, err)
		}
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
}
func TestBackupHistoryRestartKeepsDisabledCurrent(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "finished", "ordinary", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	app.closeBackupIndex()
	if err := app.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	if err := app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(app.deviceBackupPath("CONTROL"), "payload"))
	if err != nil || string(b) != "ordinary" {
		t.Fatal("disabled current lost", err)
	}
}

func TestBackupHistoryDisabledPathChangeRestartsFromSameSource(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "old history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	historyCommand(t, app, "finished", "latest current", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.configs["CONTROL"].BackupDirectory = filepath.Join(app.paths.BackupsRoot, "moved")
	app.mu.Unlock()
	app.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
			return err
		}
		if strings.Contains(string(data), `"phase":"disabling"`) {
			return errors.New("interrupted after durable intent")
		}
		return nil
	}
	if err := app.PerformBackup("CONTROL"); err == nil {
		t.Fatal("fault not injected")
	}
	app.closeBackupIndex()
	if err := app.backupStorage.close(); err != nil {
		t.Fatal(err)
	}
	if err := app.recoverBackupStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
		dir := filepath.Join(args[len(args)-1], "CONTROL")
		b, err := os.ReadFile(filepath.Join(dir, "payload"))
		if err != nil || string(b) != "latest current" {
			t.Errorf("restarted transition changed source: %q %v", b, err)
		}
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
}

func TestBackupHistoryReenableDoesNotResurrectDeletedCurrent(t *testing.T) {
	app := historyTestApp(t)
	ctx := context.Background()
	historyCommand(t, app, "finished", "history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteDeviceBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(ctx, "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if app.hasLocalBackup("CONTROL") {
		t.Fatal("reenabling resurrected deleted current from a retained history point")
	}
	if len(historyPoints(t, app)) != 1 {
		t.Fatal("retained history changed")
	}
}

func TestBackupHistoryDeletedCurrentSurvivesPathChangeAndReenable(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	policy := backupstore.Policy{Days: 3}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteDeviceBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(app.paths.BackupsRoot, "moved")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.configs["CONTROL"].BackupDirectory = target
	app.mu.Unlock()
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	policy.Enabled = true
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", policy, nil); err != nil {
		t.Fatal(err)
	}
	if app.hasLocalBackup("CONTROL") {
		t.Fatal("path change erased delete intent")
	}
	app.backupCommand = func(ctx context.Context, _ string, args ...string) *execCmd {
		dir := filepath.Join(args[len(args)-1], "CONTROL")
		if _, err := os.Stat(filepath.Join(dir, "payload")); !os.IsNotExist(err) {
			t.Error("deleted source reused as baseline")
		}
		if !argsHas(args, "--full") {
			t.Error("new baseline not full")
		}
		writeHistoryBackup(t, dir, "finished", "new")
		return newExecCmd(ctx, "/bin/sh", "-c", "printf 'Backup Successful.\\n'")
	}
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if !app.hasLocalBackup("CONTROL") {
		t.Fatal("new normal point not selected")
	}
}

func TestBackupHistoryImportUsesSourceDate(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "metadata"}[known], func(t *testing.T) {
			app := historyTestApp(t)
			dir := filepath.Join(app.paths.BackupsRoot, "CONTROL")
			writeHistoryBackup(t, dir, "finished", "legacy")
			actual := time.Now().AddDate(0, 0, -2).UTC().Truncate(time.Second)
			app.configs["CONTROL"].LastBackup = time.Now().AddDate(0, 0, -1)
			if known {
				if err := os.WriteFile(filepath.Join(dir, "Status.plist"), []byte(`<plist><dict><key>SnapshotState</key><string>finished</string><key>Date</key><date>`+actual.Format(time.RFC3339)+`</date></dict></plist>`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
				t.Fatal(err)
			}
			for _, p := range historyPoints(t, app) {
				if known {
					if p.State != "normal" || !p.CompletedAt.Equal(actual) {
						t.Fatalf("source date not used: %+v", p)
					}
				} else if p.State != "pending" || !p.Manual {
					t.Fatal("global LastBackup invented source completion time")
				}
			}
		})
	}
}

func TestBackupHistoryFailedOrdinaryWriteCannotImportOldCompletion(t *testing.T) {
	app := historyTestApp(t)
	historyCommand(t, app, "finished", "history", 0, nil)
	if err := app.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	// A failing external writer can leave old 'finished' metadata behind.
	historyCommand(t, app, "finished", "partially replaced", 1, nil)
	if err := app.PerformBackup("CONTROL"); err == nil {
		t.Fatal("failure not injected")
	}
	if err := app.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Enabled: true, Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := app.backupStorage.deviceState("CONTROL")
	found := false
	for _, p := range historyPoints(t, app) {
		if p.EpochID == st.EpochID {
			found = true
			if p.State != "pending" || !p.CompletionUnknown {
				t.Fatal("failed current write inherited old completion evidence")
			}
		}
	}
	if !found {
		t.Fatal("unknown source was not retained")
	}
}
