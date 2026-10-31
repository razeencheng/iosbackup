package app

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http/httptest"
	"testing"
	"time"
)

func activeOperationJSON(t *testing.T, a *application, udid string) map[string]any {
	t.Helper()
	for _, device := range a.buildStatusSnapshot().Devices {
		if device.UDID != udid {
			continue
		}
		raw, err := json.Marshal(device)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Fatal("device absent from status")
	return nil
}

func waitOperationIdle(t *testing.T, a *application, udid string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		value := activeOperationJSON(t, a, udid)
		if value["backing_up"] == false {
			if value["active_operation"] != "" {
				t.Errorf("operation marker retained after release: %#v", value)
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("operation did not release")
		case <-tick.C:
		}
	}
}

func TestHistoryReviewActiveOperationKeepsBusyAndSSRAccurate(t *testing.T) {
	a, h, point := historyHTTPFixture(t)
	manager := a.backupIndexManager()
	select {
	case manager.slot <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("index worker did not finish")
	}
	defer func() { <-manager.slot }()
	a.hub = newEventHub()
	updates := a.hub.Subscribe()
	defer a.hub.Unsubscribe(updates)
	store, _ := a.backupStorage.store(point.StoreID)
	catalog, _ := store.Snapshot()
	response := historyRequest(t, a, h, "POST", "/api/backup-point/"+point.ID+"/check", map[string]any{"revision": catalog.Revision})
	if response.Code != 202 {
		t.Fatalf("review start %d: %s", response.Code, response.Body)
	}
	service := a.historyService()
	service.mu.Lock()
	run := service.reviews[point.ID]
	service.mu.Unlock()
	defer run.cancel()
	value := activeOperationJSON(t, a, "CONTROL")
	if value["active_operation"] != "check" || value["backing_up"] != true || value["backup_job_active"] != false {
		t.Errorf("read-only history review misclassified: %#v", value)
	}
	if _, err := a.beginBackupJob("CONTROL", "", "", false); err == nil {
		t.Error("review no longer blocks backup admission")
	}
	if _, err := a.acquireHistoryMutation("CONTROL"); err == nil {
		t.Error("review no longer blocks history mutation")
	}
	a.htmlTemplate = template.Must(template.New("operation").Parse(`{{index .ActiveOperations "CONTROL"}}`))
	home := httptest.NewRecorder()
	a.handleHome(home, httptest.NewRequest("GET", "/", nil))
	if home.Code != 200 || home.Body.String() != "check" {
		t.Errorf("SSR operation differs: %d %s", home.Code, home.Body)
	}
	found := false
	for len(updates) > 0 {
		var snapshot struct {
			Devices []map[string]any `json:"devices"`
		}
		if err := json.Unmarshal(<-updates, &snapshot); err != nil {
			t.Fatal(err)
		}
		for _, device := range snapshot.Devices {
			if device["udid"] == "CONTROL" && device["active_operation"] == "check" {
				found = true
			}
		}
	}
	if !found {
		t.Error("review start did not broadcast check operation")
	}
	run.cancel()
	<-run.done
	waitOperationIdle(t, a, "CONTROL")
	release, err := a.acquireHistoryMutation("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	value = activeOperationJSON(t, a, "CONTROL")
	if value["active_operation"] != "local" {
		t.Errorf("completed review contaminated the next operation: %#v", value)
	}
	release()
	waitOperationIdle(t, a, "CONTROL")
}

func TestCurrentBackupCheckActiveOperation(t *testing.T) {
	a, _, _ := checkFixture(t)
	a.paths.ConfigsRoot = t.TempDir()
	a.devices["OFFLINE"] = &device{UDID: "OFFLINE", Name: "Offline test phone"}
	t.Cleanup(a.closeBackupIndex)
	started := make(chan struct{})
	a.manifestRows = func(ctx context.Context, _ string, _ func(backupManifestRow) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := a.startBackupCheck("OFFLINE"); err != nil {
		t.Fatal(err)
	}
	<-started
	value := activeOperationJSON(t, a, "OFFLINE")
	if value["active_operation"] != "check" || value["backing_up"] != true || value["backup_check_active"] != true {
		t.Errorf("current check misclassified: %#v", value)
	}
	a.mu.RLock()
	run := a.backupChecks["OFFLINE"]
	a.mu.RUnlock()
	run.cancel()
	<-run.done
	waitOperationIdle(t, a, "OFFLINE")
}

func TestActiveOperationDistinguishesBackupAndOtherBusyWork(t *testing.T) {
	a := newBackupControlTestApp(t)
	job, err := a.beginBackupJob("CONTROL", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	value := activeOperationJSON(t, a, "CONTROL")
	if value["active_operation"] != "backup" || value["backing_up"] != true || value["backup_job_active"] != true {
		t.Errorf("actual backup misclassified: %#v", value)
	}
	if err = a.runBackupJob(job); err != nil {
		t.Fatal(err)
	}
	waitOperationIdle(t, a, "CONTROL")
	_, release, err := a.acquireLocalBackupOp("CONTROL")
	if err != nil {
		t.Fatal(err)
	}
	value = activeOperationJSON(t, a, "CONTROL")
	if value["active_operation"] != "local" || value["backing_up"] != true || value["backup_job_active"] != false {
		t.Errorf("other operation confused with previous backup: %#v", value)
	}
	release()
	waitOperationIdle(t, a, "CONTROL")
}
