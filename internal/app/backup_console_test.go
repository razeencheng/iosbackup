package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
)

func consoleResponse(t *testing.T, a *application, h http.Handler, url string) map[string]any {
	t.Helper()
	w := historyRequest(t, a, h, http.MethodGet, url, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, w.Code, w.Body)
	}
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func consoleFixture(t *testing.T) (*application, http.Handler, backupstore.Point) {
	t.Helper()
	a := historyTestApp(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	historyCommand(t, a, "finished", "console backup", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	if err := a.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeHistoryHTTP)
	var p backupstore.Point
	for _, point := range historyPoints(t, a) {
		p = point
	}
	return a, a.setupRoutes(), p
}

func TestBackupConsolePointAndJobAssociation(t *testing.T) {
	a, h, p := consoleFixture(t)
	for _, url := range []string{"/api/backup-point/" + p.ID, "/api/backup-history/CONTROL"} {
		body := consoleResponse(t, a, h, url)
		point := body
		if list, ok := body["points"].([]any); ok {
			point = list[0].(map[string]any)
		}
		if nested, ok := body["point"].(map[string]any); ok {
			point = nested
		}
		if point["job_id"] != p.JobID {
			t.Errorf("point job association missing: %v", point["job_id"])
		}
	}
	records := a.backupJobRecords.list("CONTROL")
	data, _ := json.Marshal(records[0])
	var stored map[string]any
	_ = json.Unmarshal(data, &stored)
	if stored["point_id"] != p.ID || stored["history_committed"] != true || stored["history_phase"] != "completed" {
		t.Errorf("job did not retain its committed point: %s", data)
	}
	syncJobs(t, a.backupJobRecords)
	var persisted map[string]any
	raw, err := os.ReadFile(filepath.Join(a.backupJobRecords.root, p.JobID+".summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["point_id"] != p.ID {
		t.Errorf("link not persisted: %s", raw)
	}
	root := a.backupJobRecords.root
	if err := a.backupJobRecords.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := openJobTestStore(t, root, defaultBackupJobLimits())
	summary, _, ok := restored.events(p.JobID, 0, 1)
	if !ok || summary.PointID != p.ID || !summary.HistoryCommitted || summary.HistoryPhase != "completed" {
		t.Fatalf("committed association lost after restart: %+v", summary)
	}
}

func TestBackupConsoleLegacyJobAssociation(t *testing.T) {
	a, h, p := consoleFixture(t)
	// 模拟旧版本已有任务，但摘要尚未包含产出的点 ID。
	a.backupJobRecords.mu.Lock()
	a.backupJobRecords.records[p.JobID].Summary.PointID = ""
	a.backupJobRecords.records[p.JobID].Summary.HistoryCommitted = false
	a.backupJobRecords.records[p.JobID].Summary.HistoryPhase = ""
	a.backupJobRecords.records[p.JobID].Summary.PointLinkVersion = 0
	a.backupJobRecords.mu.Unlock()
	list := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	if list[0].(map[string]any)["point_id"] != p.ID {
		t.Error("old job not linked using point JobID")
	}
	events := consoleResponse(t, a, h, "/api/backup-job/"+p.JobID+"/events")["job"].(map[string]any)
	if events["point_id"] != p.ID {
		t.Error("old job events lost point link")
	}
	w := historyRequest(t, a, h, "GET", "/api/backup-job/"+p.JobID+"/export", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"point_id":"`+p.ID+`"`) {
		t.Error("old job export lost point link")
	}
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	if err := s.Delete(context.Background(), p.ID, c.Revision); err != nil {
		t.Fatal(err)
	}
	list = consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	if list[0].(map[string]any)["point_id"] != nil {
		t.Error("deleted point association guessed")
	}
}

func TestBackupConsoleCurrentSourceAndStorage(t *testing.T) {
	a, h, p := consoleFixture(t)
	body := consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source, _ := body["current_source"].(map[string]any)
	if source["kind"] != "point" || source["point_id"] != p.ID || source["store_id"] != p.StoreID || source["available"] != true {
		t.Fatalf("wrong current point: %#v", source)
	}
	storage, _ := body["storage"].(map[string]any)
	if storage["state"] != "available" || storage["available_bytes"] == nil || storage["checked_at"] == nil || storage["copy_strategy"] != "unknown" {
		t.Errorf("missing capacity: %#v", storage)
	}
	if err := a.applyHistoryPolicy(context.Background(), "CONTROL", backupstore.Policy{Days: 3}, nil); err != nil {
		t.Fatal(err)
	}
	body = consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source, _ = body["current_source"].(map[string]any)
	if source["kind"] != "current" || source["point_id"] != nil || source["available"] != true {
		t.Errorf("frozen history confused with writable current: %#v", source)
	}
	// 更换配置路径不应把旧存储的基线继续误称为当前来源。
	a.mu.Lock()
	a.configs["CONTROL"].BackupDirectory = t.TempDir()
	a.mu.Unlock()
	body = consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source, _ = body["current_source"].(map[string]any)
	if source["kind"] != "missing" || source["available"] != false || source["point_id"] != nil {
		t.Errorf("old root leaked into current: %#v", source)
	}
	a.mu.Lock()
	a.configs["CONTROL"].BackupDirectory = filepath.Join(t.TempDir(), "unmounted")
	a.mu.Unlock()
	body = consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source, _ = body["current_source"].(map[string]any)
	if source["kind"] != "current" || source["available"] != false || source["error_code"] != "api.history_unavailable" {
		t.Errorf("unmounted root misreported as missing data: %#v", source)
	}
	storage, _ = body["storage"].(map[string]any)
	if storage["state"] != "unavailable" || storage["available_bytes"] != nil || storage["error_code"] == nil {
		t.Errorf("unavailable capacity became zero: %#v", storage)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), a.paths.BackupsRoot) || strings.Contains(string(raw), "unmounted") {
		t.Fatal("private filesystem path exposed")
	}
}

func TestBackupConsoleCommittedReferenceFailure(t *testing.T) {
	a, h, old := consoleFixture(t)
	a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		var registry historyLocations
		if err := json.Unmarshal(data, &registry); err != nil {
			return err
		}
		if registry.Devices["CONTROL"].LatestPoint != old.ID {
			return errors.New("private storage path failure")
		}
		return persistence.WriteFileAtomic(path, data, mode)
	}
	historyCommand(t, a, "finished", "new version", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	latest := jobs[0].(map[string]any)
	if latest["history_committed"] != true || latest["history_phase"] != "reference" || latest["point_id"] == nil || latest["point_id"] == old.ID {
		t.Fatalf("committed point misreported as not saved: %#v", latest)
	}
	if latest["execution_state"] != "succeeded" {
		t.Errorf("execution result changed: %#v", latest)
	}
	if latest["history_state"] != "uncertain" {
		t.Errorf("lost persistence uncertainty: %#v", latest)
	}
}

func TestBackupConsoleCurrentSourceDoesNotUseOtherGenerationBaseline(t *testing.T) {
	a, h, current := consoleFixture(t)
	store, _ := a.backupStorage.store(current.StoreID)
	work, err := store.NewWork("bkp-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, work.Directory, "finished", "different generation")
	other := current
	other.Generation.ID, err = backupstore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	other.CompletedAt = a.nowTime()
	other.Day = other.CompletedAt.In(a.nowTime().Location()).Format("2006-01-02")
	other, err = store.Commit(context.Background(), work.ID, other)
	if err != nil {
		t.Fatal(err)
	}
	body := consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	points := body["points"].([]any)
	if len(points) != 2 || points[0].(map[string]any)["id"] != other.ID {
		t.Fatal("fixture must contain a newer point in another generation")
	}
	source := body["current_source"].(map[string]any)
	if source["point_id"] != current.ID {
		t.Errorf("selected another generation baseline: %#v", source)
	}
}

func TestBackupConsoleUnmanagedCurrentAndMissing(t *testing.T) {
	a := historyTestApp(t)
	a.configs["CONTROL"].History.Enabled = false
	if err := a.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	h := a.setupRoutes()
	body := consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source := body["current_source"].(map[string]any)
	if source["kind"] != "missing" || source["available"] != false {
		t.Errorf("empty source: %#v", source)
	}
	writeHistoryBackup(t, filepath.Join(a.paths.BackupsRoot, "CONTROL"), "finished", "ordinary current")
	body = consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source = body["current_source"].(map[string]any)
	if source["kind"] != "current" || source["available"] != true || source["point_id"] != nil {
		t.Errorf("ordinary current: %#v", source)
	}
}

func TestBackupConsoleUnavailablePointKeepsIdentity(t *testing.T) {
	a, h, point := consoleFixture(t)
	a.closeBackupIndex()
	a.backupStorage.mu.Lock()
	a.backupStorage.unavailable[point.StoreID] = errors.New("private missing mount path")
	a.backupStorage.mu.Unlock()
	body := consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source := body["current_source"].(map[string]any)
	if source["kind"] != "point" || source["point_id"] != point.ID || source["available"] != false || source["error_code"] == nil {
		t.Errorf("unavailable point lost identity: %#v", source)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "private missing") {
		t.Fatal("private error exposed")
	}
}

func TestBackupConsoleCommittedCleanupFailure(t *testing.T) {
	a, h, old := consoleFixture(t)
	missingID, err := backupstore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	a.backupStorage.write = func(path string, data []byte, mode os.FileMode) error {
		if err := persistence.WriteFileAtomic(path, data, mode); err != nil {
			return err
		}
		var registry historyLocations
		if err := json.Unmarshal(data, &registry); err != nil {
			return err
		}
		if registry.Devices["CONTROL"].LatestPoint != old.ID {
			// 引用已经写入后，模拟另一个历史存储被卸载，阻止清理。
			a.backupStorage.locations.Stores = append(a.backupStorage.locations.Stores, historyLocation{ID: missingID, UDID: "CONTROL"})
			a.backupStorage.unavailable[missingID] = os.ErrNotExist
		}
		return nil
	}
	historyCommand(t, a, "finished", "new version", 0, nil)
	if err := a.PerformBackup("CONTROL"); err != nil {
		t.Fatal(err)
	}
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	latest := jobs[0].(map[string]any)
	if latest["history_committed"] != true || latest["history_phase"] != "cleanup" || latest["point_id"] == nil || latest["point_id"] == old.ID || latest["history_state"] != "failed" {
		t.Fatalf("committed point lost after cleanup failure: %#v", latest)
	}
	body := consoleResponse(t, a, h, "/api/backup-history/CONTROL")
	source := body["current_source"].(map[string]any)
	if source["point_id"] != latest["point_id"] || source["available"] != true {
		t.Errorf("committed reference did not advance: %#v", source)
	}
}

func TestBackupConsoleCapacityUnknownDoesNotClaimZero(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capacity backupstore.Capacity
		err      error
	}{
		{name: "unsupported"},
		{name: "query failed", capacity: backupstore.Capacity{Bytes: 123, BytesKnown: true}, err: errors.New("private mount path")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := historyCapacityView(tc.capacity, tc.err, jobTestNow)
			if view.AvailableBytes != nil || view.State != "unavailable" || view.CopyStrategy != "unknown" || !view.CheckedAt.Equal(jobTestNow) {
				t.Errorf("unknown capacity: %+v", view)
			}
		})
	}
	zero := historyCapacityView(backupstore.Capacity{BytesKnown: true}, nil, jobTestNow)
	if zero.AvailableBytes == nil || *zero.AvailableBytes != 0 || zero.State != "available" {
		t.Errorf("known zero lost: %+v", zero)
	}
}

func TestBackupConsoleAmbiguousLegacyAssociationIsNotGuessed(t *testing.T) {
	a, h, point := consoleFixture(t)
	store, _ := a.backupStorage.store(point.StoreID)
	work, err := store.NewWork(point.JobID)
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryBackup(t, work.Directory, "finished", "import and backup share job")
	if _, err = store.Commit(context.Background(), work.ID, point); err != nil {
		t.Fatal(err)
	}
	a.backupJobRecords.mu.Lock()
	record := a.backupJobRecords.records[point.JobID]
	record.Summary.PointID, record.Summary.HistoryPhase, record.Summary.HistoryCommitted = "", "", false
	record.Summary.PointLinkVersion = 0
	a.backupJobRecords.mu.Unlock()
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	if jobs[0].(map[string]any)["point_id"] != nil {
		t.Error("ambiguous legacy output guessed")
	}
}

func TestBackupConsoleLegacyLinkDoesNotGuessAcrossUnavailableStores(t *testing.T) {
	a, h, point := consoleFixture(t)
	a.backupJobRecords.mu.Lock()
	record := a.backupJobRecords.records[point.JobID]
	record.Summary.PointID, record.Summary.HistoryPhase, record.Summary.HistoryCommitted = "", "", false
	record.Summary.PointLinkVersion = 0
	a.backupJobRecords.mu.Unlock()
	missingID, err := backupstore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	a.backupStorage.mu.Lock()
	a.backupStorage.locations.Stores = append(a.backupStorage.locations.Stores, historyLocation{ID: missingID, UDID: "CONTROL"})
	a.backupStorage.unavailable[missingID] = os.ErrNotExist
	a.backupStorage.mu.Unlock()
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	if jobs[0].(map[string]any)["point_id"] != nil {
		t.Error("legacy output guessed with another store unavailable")
	}
}

func TestBackupConsoleLegacyLinkRejectsChangedStoreMount(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "unmounted"
		if replacement {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			a, h, point := consoleFixture(t)
			a.closeBackupIndex()
			a.backupJobRecords.mu.Lock()
			record := a.backupJobRecords.records[point.JobID]
			record.Summary.PointID, record.Summary.HistoryPhase, record.Summary.HistoryCommitted = "", "", false
			record.Summary.PointLinkVersion = 0
			a.backupJobRecords.mu.Unlock()
			location := a.historyLocations("CONTROL")[0]
			if err := os.Rename(location.Path, location.Path+"-offline"); err != nil {
				t.Fatal(err)
			}
			if replacement {
				if err := os.Mkdir(location.Path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, url := range []string{"/api/backup-jobs/CONTROL", "/api/backup-job/" + point.JobID + "/events"} {
				response := consoleResponse(t, a, h, url)
				job, ok := response["job"].(map[string]any)
				if !ok {
					job = response["jobs"].([]any)[0].(map[string]any)
				}
				if job["point_id"] != nil {
					t.Errorf("%s linked an unavailable store through cached metadata", url)
				}
			}
		})
	}
}

func consoleImportFixture(t *testing.T, knownDate bool, fail bool) (*application, http.Handler, backupstore.Point) {
	t.Helper()
	a := historyTestApp(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	legacy := filepath.Join(a.paths.BackupsRoot, "CONTROL")
	writeHistoryBackup(t, legacy, "finished", "imported contents")
	if knownDate {
		stamp := a.nowTime().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
		status := `<plist><dict><key>SnapshotState</key><string>finished</string><key>Date</key><date>` + stamp + `</date></dict></plist>`
		if err := os.WriteFile(filepath.Join(legacy, "Status.plist"), []byte(status), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exit := 0
	if fail {
		exit = 1
	}
	historyCommand(t, a, "finished", "new backup", exit, nil)
	err := a.PerformBackup("CONTROL")
	if fail && err == nil || !fail && err != nil {
		t.Fatalf("unexpected backup outcome: %v", err)
	}
	if err := a.initializeSecurity(); err != nil {
		t.Fatal(err)
	}
	a.closeBackupIndex()
	job := a.backupJobRecords.list("CONTROL")[0]
	var imported backupstore.Point
	for _, point := range historyPoints(t, a) {
		if point.CompletionUnknown || point.CompletedAt.Before(job.StartedAt) {
			imported = point
		}
	}
	if imported.ID == "" {
		t.Fatal("fixture did not retain imported point")
	}
	return a, a.setupRoutes(), imported
}

func TestBackupConsoleFailedBackupDoesNotClaimImportedPoint(t *testing.T) {
	a, h, imported := consoleImportFixture(t, true, true)
	if len(historyPoints(t, a)) != 1 {
		t.Fatal("fixture must only retain imported point")
	}
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	job := jobs[0].(map[string]any)
	if job["execution_state"] != "failed" {
		t.Fatal("fixture backup did not fail")
	}
	if job["point_id"] != nil || job["history_committed"] == true {
		t.Errorf("failed backup claimed imported point %s: %#v", imported.ID, job)
	}
}

func TestBackupConsoleLegacyAssociationRejectsOnlyRemainingImport(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "unknown date"
		if known {
			name = "known old date"
		}
		t.Run(name, func(t *testing.T) {
			a, h, imported := consoleImportFixture(t, known, false)
			for _, point := range historyPoints(t, a) {
				if point.ID == imported.ID {
					continue
				}
				store, _ := a.backupStorage.store(point.StoreID)
				catalog, _ := store.Snapshot()
				if err := store.Delete(context.Background(), point.ID, catalog.Revision); err != nil {
					t.Fatal(err)
				}
			}
			a.backupJobRecords.mu.Lock()
			record := a.backupJobRecords.records[imported.JobID]
			data, _ := json.Marshal(record.Summary)
			var fields map[string]any
			_ = json.Unmarshal(data, &fields)
			delete(fields, "point_link_version")
			delete(fields, "point_id")
			delete(fields, "history_phase")
			delete(fields, "history_committed")
			data, _ = json.Marshal(fields)
			var legacy backupJobSummary
			if err := json.Unmarshal(data, &legacy); err != nil {
				t.Fatal(err)
			}
			record.Summary = legacy
			a.backupJobRecords.mu.Unlock()
			jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
			job := jobs[0].(map[string]any)
			if job["point_id"] != nil || job["history_committed"] == true {
				t.Errorf("legacy task claimed old imported point as its new output: %#v", job)
			}
		})
	}
}

func TestBackupConsolePointLinkVersionPersistsFromStart(t *testing.T) {
	root := t.TempDir()
	store := openJobTestStore(t, root, defaultBackupJobLimits())
	seed := jobFixture(91)
	store.start(seed)
	syncJobs(t, store)
	data, err := os.ReadFile(filepath.Join(root, seed.ID+".summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["point_link_version"] != float64(1) {
		t.Errorf("new running job has no association protocol marker: %s", data)
	}
	if err := store.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := openJobTestStore(t, root, defaultBackupJobLimits())
	summary, _, ok := restored.events(seed.ID, 0, 1)
	if !ok {
		t.Fatal("job did not reload")
	}
	data, err = json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	fields = map[string]any{}
	_ = json.Unmarshal(data, &fields)
	if fields["point_link_version"] != float64(1) {
		t.Errorf("association protocol marker lost at restart: %s", data)
	}
}

func TestBackupConsoleNewJobDoesNotInferUnconfirmedPoint(t *testing.T) {
	a, h, point := consoleFixture(t)
	a.backupJobRecords.mu.Lock()
	record := a.backupJobRecords.records[point.JobID]
	// 模拟点已发布但进程未记录可靠提交结果；新协议必须保留未确认状态。
	record.Summary.PointID = ""
	record.Summary.HistoryCommitted = false
	record.Summary.HistoryPhase = "commit"
	record.Summary.HistoryState = "uncertain"
	a.backupJobRecords.mu.Unlock()
	jobs := consoleResponse(t, a, h, "/api/backup-jobs/CONTROL")["jobs"].([]any)
	job := jobs[0].(map[string]any)
	if job["point_link_version"] != float64(backupJobPointLinkVersion) || job["point_id"] != nil || job["history_committed"] == true {
		t.Errorf("new task inferred an unconfirmed output: %#v", job)
	}
}
