package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManualCheckTaskOutcomeMatchesAttempt(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		err         error
	}{
		{"passed", backupStateSucceeded, nil},
		{"issues", backupStateSucceeded, nil},
		{"cancelled", backupStateStopped, context.Canceled},
		{"timeout", backupStateInterrupted, context.DeadlineExceeded},
		{"read_failed", backupStateFailed, errors.New("unreadable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _, _ := checkFixture(t)
			t.Cleanup(app.closeBackupIndex)
			app.configs["OFFLINE"].Name = "Saved test phone"
			app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
			app.manifestRows = func(context.Context, string, func(backupManifestRow) error) error { return tc.err }
			if tc.name == "issues" {
				app.manifestRows = checkRows(backupManifestRow{FileID: strings.Repeat("a", 40), Flags: 1})
			}
			manager := app.backupIndexManager()
			manager.slot <- struct{}{}
			id, err := app.startBackupCheck("OFFLINE")
			if err != nil {
				t.Fatal(err)
			}
			app.mu.RLock()
			run := app.backupChecks["OFFLINE"]
			app.mu.RUnlock()
			<-manager.slot
			<-run.done
			summary, _, ok := app.backupJobRecords.events(id, 0, 100)
			if !ok || summary.State != tc.state {
				t.Fatal("wrong check task outcome", summary.State, summary.Check)
			}
			if summary.DeviceName != "Saved test phone" {
				t.Fatal("saved device name ignored", summary.DeviceName)
			}
			if tc.name == "issues" && (summary.Check.State != "issues" || summary.Check.MissingObjects != 1) {
				t.Fatal("completed scan lost its missing-file conclusion", summary.Check)
			}
		})
	}
}

func TestHistoryReviewTimeoutIsInterruptedAndKeepsReason(t *testing.T) {
	app, _, point := historyHTTPFixture(t)
	app.backupIndexManager().close()
	app.mu.Lock()
	app.backupIndex = nil
	app.mu.Unlock()
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	app.mu.Lock()
	app.devices[point.UDID] = nil
	app.configs[point.UDID].Name = "Offline history phone"
	app.mu.Unlock()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	app.rootCtx = ctx
	store, err := app.backupStorage.store(point.StoreID)
	if err != nil {
		t.Fatal(err)
	}
	release, err := app.acquireHistoryMutation(point.UDID)
	if err != nil {
		t.Fatal(err)
	}
	manager := app.backupIndexManager()
	manager.slot <- struct{}{}
	id, err := app.startHistoryReview(store, point, release)
	if err != nil {
		<-manager.slot
		release()
		t.Fatal(err)
	}
	service := app.historyService()
	service.mu.Lock()
	run := service.reviews[point.ID]
	service.mu.Unlock()
	<-run.done
	<-manager.slot
	summary, _, _ := app.backupJobRecords.events(id, 0, 100)
	if summary.State != backupStateInterrupted || summary.Check.Reason != "timeout" {
		t.Fatal("timeout was presented as cancellation or success", summary)
	}
	if summary.DeviceName != "Offline history phone" {
		t.Fatal("history check ignored saved device name", summary.DeviceName)
	}
}

func TestCancelledCurrentCheckRetainsCompletedEvidence(t *testing.T) {
	app, source, _ := checkFixture(t)
	app.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	job := jobFixture(40)
	job.UDID = "OFFLINE"
	job.Operation = "check"
	app.backupJobRecords.start(job)
	passed := backupCheckResult{ID: job.ID, Kind: "manifest", State: "passed", Freshness: "current", StartedAt: jobTestNow, CompletedAt: jobTestNow, SourceKey: source.Key, SourceIdentity: source.Identity, SourceFingerprint: source.Fingerprint}
	app.backupJobRecords.setCheck(job.ID, passed)
	app.backupJobRecords.finish(job.ID, backupStateSucceeded, "", jobTestNow)
	latest := passed
	latest.ID = "bkp-000000000099"
	latest.State = "unavailable"
	latest.Reason = "cancelled"
	app.backupCheckResults = map[string]backupCheckResult{"OFFLINE": latest}
	app.mu.RLock()
	view := app.backupCheckSnapshotUnsafe("OFFLINE")
	app.mu.RUnlock()
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	previous, ok := decoded["last_completed"].(map[string]any)
	if !ok || previous["state"] != "passed" || decoded["reason"] != "cancelled" {
		t.Fatal("latest attempt or prior evidence lost", decoded)
	}
}
