package backupstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestReviewCancellationPreservesCompletedEvidence(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	point, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	passed := Check{State: "passed", Scope: "manifest", CheckedAt: time.Now().UTC(), MetadataComplete: true, ManifestComplete: true}
	c, _ := s.Snapshot()
	if err = s.RecordReview(context.Background(), point.ID, passed, c.Revision); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Snapshot()
	cancelled := Check{State: "unavailable", Reason: "cancelled", Scope: "manifest", CheckedAt: passed.CheckedAt.Add(time.Second)}
	if err = s.RecordReview(context.Background(), point.ID, cancelled, c.Revision); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Snapshot()
	got := c.Points[point.ID]
	if got.Review != passed {
		t.Fatal("cancellation replaced completed evidence", got.Review)
	}
	data, _ := json.Marshal(got)
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	attempt, ok := decoded["review_attempt"].(map[string]any)
	if !ok || attempt["reason"] != "cancelled" {
		t.Fatal("cancellation hidden behind older passing check", decoded)
	}
	if _, err = s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}
