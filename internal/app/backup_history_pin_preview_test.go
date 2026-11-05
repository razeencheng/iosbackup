package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestHistoryUnpinPreviewTracksActualCleanupImpact(t *testing.T) {
	a, h, first := historyHTTPFixture(t)
	waitIndexJob(t, a.backupIndexManager(), "CONTROL")
	s, _ := a.backupStorage.store(first.StoreID)
	c, _ := s.Snapshot()
	if err := s.SetPinned(context.Background(), first.ID, true, c.Revision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := a.PerformBackup("CONTROL"); err != nil {
			t.Fatal(err)
		}
	}
	waitIndexJob(t, a.backupIndexManager(), "CONTROL")
	c, _ = s.Snapshot()
	base := "/api/backup-point/" + first.ID
	type previewResponse struct {
		Token         string `json:"token"`
		AffectedCount int    `json:"affected_count"`
	}
	preview := func(want int) previewResponse {
		t.Helper()
		w := historyRequest(t, a, h, http.MethodPost, base+"/pin-preview", map[string]any{"revision": c.Revision, "pinned": false})
		if w.Code != 200 {
			t.Fatalf("preview %d %s", w.Code, w.Body)
		}
		var p previewResponse
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Token == "" || p.AffectedCount != want {
			t.Fatalf("preview %+v want %d", p, want)
		}
		unchanged, _ := s.Snapshot()
		if !unchanged.Points[first.ID].Pinned || unchanged.Revision != c.Revision {
			t.Fatal("preview mutated point")
		}
		return p
	}
	lease, err := s.Acquire(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := preview(0)
	lease.Close()
	w := historyRequest(t, a, h, http.MethodPost, base+"/pin", map[string]any{"revision": c.Revision, "pinned": false, "token": old.Token})
	if w.Code != 409 {
		t.Fatalf("lease impact changed but stale preview accepted: %d %s", w.Code, w.Body)
	}
	w = historyRequest(t, a, h, http.MethodPost, base+"/pin", map[string]any{"revision": c.Revision, "pinned": false})
	if w.Code != 409 {
		t.Fatalf("unpin without preview accepted: %d", w.Code)
	}
	fresh := preview(1)
	w = historyRequest(t, a, h, http.MethodPost, base+"/pin", map[string]any{"revision": c.Revision, "pinned": false, "token": fresh.Token})
	if w.Code != 200 {
		t.Fatalf("unpin %d %s", w.Code, w.Body)
	}
	after, _ := s.Snapshot()
	if after.Points[first.ID].Pinned || len(after.Points) != len(c.Points) {
		t.Fatal("unpin must not immediately delete data")
	}
	w = historyRequest(t, a, h, http.MethodPost, base+"/pin", map[string]any{"revision": after.Revision, "pinned": false, "token": fresh.Token})
	if w.Code != 409 {
		t.Fatal("consumed preview replay accepted", w.Code)
	}
}
func TestHistoryPolicyAPIRejectsInvalidCount(t *testing.T) {
	a, h, _ := historyHTTPFixture(t)
	for _, count := range []int{-1, 0, 366} {
		w := historyRequest(t, a, h, http.MethodPost, "/api/backup-history/CONTROL/policy-preview", map[string]any{"policy": map[string]any{"enabled": true, "count": count}})
		if w.Code != 400 {
			t.Fatalf("count %d returned %d", count, w.Code)
		}
	}
}
