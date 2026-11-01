package app

import (
	"context"
	"encoding/json"
	"iosbackup/internal/backupstore"
	"net/http"
	"testing"
	"time"
)

func TestHistoryCheckCapabilitiesRespectEncryption(t *testing.T) {
	for _, kind := range []string{"encrypted", "unknown", "unencrypted"} {
		t.Run(kind, func(t *testing.T) {
			a, h, seed := historyHTTPFixture(t)
			s, _ := a.backupStorage.store(seed.StoreID)
			w, err := s.NewWork("capability")
			if err != nil {
				t.Fatal(err)
			}
			writeHistoryBackup(t, w.Directory, "finished", "payload")
			p := seed
			p.ID = ""
			p.Generation.ID, _ = backupstore.NewID()
			p.Generation.Kind = kind
			p, err = s.Commit(context.Background(), w.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			res := historyRequest(t, a, h, http.MethodGet, "/api/backup-point/"+p.ID, nil)
			var view map[string]any
			if err = json.Unmarshal(res.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if view["can_check"] != (kind == "unencrypted") {
				t.Fatal("check capability misrepresents encryption", kind, view["can_check"])
			}
			if view["check"].(map[string]any)["state"] != "passed" {
				t.Fatal("basic check evidence hidden")
			}
			if kind != "unencrypted" {
				if view["check_scope"] != "manifest" || view["check_unavailable_reason"] == "" {
					t.Fatal("missing unsupported scope/reason")
				}
				c, _ := s.Snapshot()
				res = historyRequest(t, a, h, http.MethodPost, "/api/backup-point/"+p.ID+"/check", map[string]any{"revision": c.Revision, "action": "start"})
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatal("unsupported API request was admitted", res.Code)
				}
			}
		})
	}
}

func TestHistoryUnsavedReviewRemainsVisible(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	service := a.historyService()
	done := make(chan struct{})
	close(done)
	service.reviews[p.ID] = &historyReviewRun{ID: "bkp-000000000001", Result: backupCheckResult{Kind: "manifest", State: "issues", Reason: "missing_objects", CompletedAt: time.Now(), MissingObjects: 1}, Saved: false, done: done, cancel: func() {}}
	res := historyRequest(t, a, h, http.MethodGet, "/api/backup-point/"+p.ID, nil)
	var view historyPointView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.ReviewSaveFailed || view.ReviewAttempt.State != "issues" || view.Check.State != "passed" {
		t.Fatal("unsaved issue hidden or original evidence erased", view)
	}
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	if c.Points[p.ID].ReviewAttempt.State != "" {
		t.Fatal("overlay pretended to persist result")
	}
}
