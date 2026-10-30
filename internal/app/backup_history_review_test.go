package app

import (
	"context"
	"encoding/json"
	"iosbackup/internal/backupstore"
	"testing"
	"time"
)

func TestHistoryReviewEvidenceSurvivesRestart(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/check", map[string]any{"revision": c.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	var response struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	service := a.historyService()
	service.mu.Lock()
	run := service.reviews[p.ID]
	service.mu.Unlock()
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		t.Fatal("check not completed")
	}
	c, _ = s.Snapshot()
	if c.Points[p.ID].Review.Reason == "" || !c.Points[p.ID].Review.MetadataComplete {
		t.Fatalf("lost detailed evidence: %+v", c.Points[p.ID].Review)
	}
	jobs := a.backupJobRecords.list(p.UDID)
	if len(jobs) != 1 || jobs[0].PointID != p.ID || jobs[0].Check == nil {
		t.Fatal("historical check missing from task records", jobs)
	}
	if c.Points[p.ID].State != p.State || c.Baselines[p.Generation.ID] != p.ID {
		t.Fatal("review changed baseline")
	}
	var loc historyLocation
	for _, v := range a.historyLocations(p.UDID) {
		if v.ID == p.StoreID {
			loc = v
		}
	}
	s.Close()
	reopened, e := backupstore.Open(loc.Path, p.UDID, p.StoreID)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, e = reopened.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	again, e := reopened.Snapshot()
	if e != nil || again.Points[p.ID].Review != c.Points[p.ID].Review {
		t.Fatal("review lost after reopen", e)
	}
}
func TestHistoricalReviewDoesNotReplaceCurrentCheck(t *testing.T) {
	a, _, p := historyHTTPFixture(t)
	a.backupJobRecords = openJobTestStore(t, t.TempDir(), defaultBackupJobLimits())
	seed := jobFixture(44)
	seed.PointID = p.ID
	seed.UDID = p.UDID
	seed.Operation = "check"
	a.backupJobRecords.start(seed)
	result := backupCheckResult{Kind: "manifest", State: "issues", Reason: "missing_objects", Freshness: "current", StartedAt: a.nowTime(), CompletedAt: a.nowTime()}
	a.backupJobRecords.setCheck(seed.ID, result)
	a.backupJobRecords.staleChecks(p.UDID)
	jobs := a.backupJobRecords.list(p.UDID)
	if jobs[0].Check.Freshness != "current" {
		t.Fatal("new current write invalidated immutable history evidence")
	}
	a.mu.Lock()
	delete(a.backupCheckResults, p.UDID)
	current := a.backupCheckSnapshotUnsafe(p.UDID)
	a.mu.Unlock()
	if current != nil {
		t.Fatal("history review presented as current backup check")
	}
}
