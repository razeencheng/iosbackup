package backupstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// 本地诊断：在真实清理阶段使用已有故障注入屏障模拟慢磁盘，不访问测试服务器。
func TestHistoryReadDuringSlowGC(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w2, p2 := preparePoint(t, s)
	p2.Generation, p2.EpochID = p.Generation, p.EpochID
	if _, err = s.Commit(context.Background(), w2.ID, p2); err != nil {
		t.Fatal(err)
	}
	req := RetentionRequest{Policy: Policy{Enabled: true, Days: 3}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now().UTC()}
	plan, err := s.PlanGC(req)
	if err != nil || len(plan.Delete) != 1 || plan.Delete[0] != old.ID {
		t.Fatalf("unexpected fixture retention plan: %+v, %v", plan, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.step = func(stage string) error {
		if stage == "recycle-data.before" {
			close(entered)
			<-release
		}
		return nil
	}
	gcDone := make(chan error, 1)
	go func() { _, e := s.GC(context.Background(), req, plan.Revision); gcDone <- e }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("did not reach controlled GC pause")
	}
	readDone := make(chan error, 1)
	go func() { _, e := s.PlanGC(req); readDone <- e }()
	blocked := false
	select {
	case <-readDone:
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	close(release)
	if err = <-gcDone; err != nil {
		t.Fatal(err)
	}
	if blocked {
		if err = <-readDone; err != nil {
			t.Fatal(err)
		}
		t.Fatal("history read remained blocked throughout slow physical deletion; the HTTP history route waits on the same PlanGC catalog lock")
	}
}

func pausedRecycle(t *testing.T, explicit bool) (*Store, Point, Point, func(), <-chan struct{}) {
	t.Helper()
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	old, err := s.Commit(context.Background(), w.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	w, p2 := preparePoint(t, s)
	p2.Generation, p2.EpochID = p.Generation, p.EpochID
	latest, err := s.Commit(context.Background(), w.ID, p2)
	if err != nil {
		t.Fatal(err)
	}
	req := RetentionRequest{Policy: Policy{Enabled: true, Days: 3}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now().UTC()}
	plan, err := s.PlanGC(req)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	s.step = func(stage string) error {
		if stage == "recycle-data.before" {
			close(entered)
			<-release
		}
		return nil
	}
	t.Cleanup(func() { resume(); <-done })
	go func() {
		defer close(done)
		var err error
		if explicit {
			err = s.DeleteWithIntent(context.Background(), old.ID, plan.Revision, nil)
		} else {
			_, err = s.GC(context.Background(), req, plan.Revision)
		}
		if err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not reach barrier")
	}
	return s, old, latest, resume, done
}
func TestRecycleKeepsOtherPointsReadable(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			s, old, latest, _, _ := pausedRecycle(t, explicit)
			read := make(chan error, 1)
			go func() {
				c, err := s.ReadSnapshot()
				if err != nil {
					read <- err
					return
				}
				if _, ok := c.Points[old.ID]; ok {
					read <- fmt.Errorf("deleting point still published")
					return
				}
				if _, err = s.Acquire(old.ID); !errors.Is(err, os.ErrNotExist) {
					read <- fmt.Errorf("deleting point admitted: %v", err)
					return
				}
				lease, err := s.Acquire(latest.ID)
				if err != nil {
					read <- err
					return
				}
				data, err := readLeaseFile(lease, "Info.plist")
				lease.Close()
				if err == nil && string(data) != "original" {
					err = fmt.Errorf("retained data changed")
				}
				read <- err
			}()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("readers blocked by physical deletion")
			}
		})
	}
}
func TestRecycleSerializesCloseAndMutations(t *testing.T) {
	for _, operation := range []string{"close", "pin", "recover", "work"} {
		t.Run(operation, func(t *testing.T) {
			s, _, latest, resume, done := pausedRecycle(t, false)
			action := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				var err error
				switch operation {
				case "close":
					err = s.Close()
				case "pin":
					c, e := s.Snapshot()
					err = e
					if err == nil {
						err = s.SetPinned(context.Background(), latest.ID, true, c.Revision)
					}
				case "recover":
					_, err = s.Recover(context.Background())
				case "work":
					_, err = s.NewWork("after-gc")
				}
				action <- err
			}()
			<-started
			select {
			case err := <-action:
				t.Fatalf("%s raced physical deletion: %v", operation, err)
			case <-time.After(50 * time.Millisecond):
			}
			resume()
			<-done
			select {
			case err := <-action:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("operation stuck after cleanup")
			}
		})
	}
}

// 清理第一份时，第二份仍可被读取；再次检查租约后应延后它而非报保存失败。
func TestGCSkipsPointLeasedDuringEarlierDeletion(t *testing.T) {
	s := storeFixture(t)
	w, p := preparePoint(t, s)
	if _, err := s.Commit(context.Background(), w.ID, p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w, q := preparePoint(t, s)
		q.Generation, q.EpochID = p.Generation, p.EpochID
		if _, err := s.Commit(context.Background(), w.ID, q); err != nil {
			t.Fatal(err)
		}
	}
	req := RetentionRequest{Policy: Policy{Enabled: true, Days: 3}, EpochID: p.EpochID, GenerationID: p.Generation.ID, Now: time.Now().UTC()}
	plan, err := s.PlanGC(req)
	if err != nil || len(plan.Delete) != 2 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.step = func(stage string) error {
		if stage == "recycle-data.before" {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	var removed []string
	var gcErr error
	go func() { defer close(done); removed, gcErr = s.GC(context.Background(), req, plan.Revision) }()
	<-entered
	lease, err := s.Acquire(plan.Delete[1])
	if err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	close(release)
	<-done
	defer lease.Close()
	if gcErr != nil || len(removed) != 1 || removed[0] != plan.Delete[0] {
		t.Fatalf("new reader should defer cleanup: removed=%v err=%v", removed, gcErr)
	}
	if data, err := readLeaseFile(lease, "Info.plist"); err != nil || string(data) != "original" {
		t.Fatalf("reader data changed: %s %v", data, err)
	}
}
