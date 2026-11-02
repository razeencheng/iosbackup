package backupstore

import (
	"testing"
	"time"
)

func TestHistoryPolicyDefaults(t *testing.T) {
	for _, count := range []int{0, 1, 3, 7, 30, 365} {
		p, err := (Policy{Count: count}).Normalize()
		want := count
		if want == 0 {
			want = 2
		}
		if err != nil || p.Enabled || p.Count != want {
			t.Fatalf("count %d: %+v %v", count, p, err)
		}
	}
	for _, count := range []int{-1, 366} {
		if _, err := (Policy{Count: count}).Normalize(); err == nil {
			t.Fatalf("accepted %d", count)
		}
	}
}

func TestCatalogIdentityAndReferences(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(id) || id == other || ValidID("../escape") {
		t.Fatal("invalid or duplicate identities")
	}
	identity := Identity{Schema: 1, ID: id, UDID: "TEST-PHONE", CreatedAt: time.Now()}
	c := Catalog{Schema: 1, StoreID: id, Revision: 1, Points: map[string]Point{}, Baselines: map[string]string{}}
	if err := c.Validate(identity); err != nil {
		t.Fatal(err)
	}
	c.StoreID = other
	if c.Validate(identity) == nil {
		t.Fatal("accepted wrong store")
	}
	c.StoreID = id
	c.Baselines[id] = other
	if c.Validate(identity) == nil {
		t.Fatal("accepted dangling baseline")
	}
}
