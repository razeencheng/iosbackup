package backupstore

import (
	"testing"
	"time"
)

func TestHistoryPolicyDefaults(t *testing.T) {
	for _, days := range []int{0, 1, 3, 7, 30, 365} {
		p, err := (Policy{Days: days}).Normalize()
		want := days
		if want == 0 {
			want = 3
		}
		if err != nil || p.Enabled || p.Days != want {
			t.Fatalf("days %d: %+v %v", days, p, err)
		}
	}
	for _, days := range []int{-1, 366} {
		if _, err := (Policy{Days: days}).Normalize(); err == nil {
			t.Fatalf("accepted %d", days)
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
