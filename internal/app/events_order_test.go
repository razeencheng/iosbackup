package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// 真实调用者并发修改状态并广播；客户端不应在收到新状态后又收到旧状态。
func TestConcurrentStatusNeverRegresses(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	app := newApplication()
	app.hub = newEventHub()
	app.devices["CONTROL"] = &device{UDID: "CONTROL", Name: "iPhone", IsOnline: true}
	ch := app.hub.Subscribe()
	var wg sync.WaitGroup
	var counter int
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	var received int
	var regressions int
	var newest, stale string
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := time.Time{}
		for payload := range ch {
			var snapshot statusSnapshot
			if err := json.Unmarshal(payload, &snapshot); err != nil {
				t.Error(err)
				continue
			}
			at, err := time.Parse(time.RFC3339, snapshot.Devices[0].LastBackupAt)
			if err != nil {
				t.Error(err)
				continue
			}
			received++
			if at.Before(last) {
				regressions++
				newest, stale = last.Format(time.RFC3339), at.Format(time.RFC3339)
			}
			if at.After(last) {
				last = at
			}
		}
	}()
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				app.mu.Lock()
				counter++
				app.devices["CONTROL"].LastBackup = base.Add(time.Duration(counter) * time.Second)
				app.mu.Unlock()
				app.broadcastStatus()
			}
		}()
	}
	wg.Wait()
	app.hub.Unsubscribe(ch)
	<-done
	if received == 0 {
		t.Fatal("no snapshots received")
	}
	if regressions != 0 {
		t.Fatalf("out-of-order status snapshots: regressions=%d received=%d newest=%s later_stale=%s", regressions, received, newest, stale)
	}
}

// 慢客户端恢复读取时，最后一帧仍应是终态。
func TestSlowStatusSubscriberRetainsLatest(t *testing.T) {
	h := newEventHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)
	for i := 0; i < 30; i++ {
		h.Broadcast([]byte("running"))
	}
	h.Broadcast([]byte("completed"))
	var last string
	for len(ch) > 0 {
		last = string(<-ch)
	}
	if last != "completed" {
		t.Fatalf("terminal snapshot lost: %q", last)
	}
}

type blockedStatusWriter struct {
	*httptest.ResponseRecorder
	entered, release chan struct{}
	once             sync.Once
}

func (w *blockedStatusWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.ResponseRecorder.Write(p)
}

// 首帧网络写入慢，不能持有全局发布锁；重连应得到当下状态。
func TestStatusFirstFrameDoesNotBlockPublishAndReconnect(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	app.devices["CONTROL"] = &device{UDID: "CONTROL", Name: "iPhone", LastBackup: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &blockedStatusWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		app.handleEvents(w, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx))
		close(done)
	}()
	<-w.entered
	app.mu.Lock()
	app.devices["CONTROL"].LastBackup = base.Add(time.Hour)
	app.mu.Unlock()
	published := make(chan struct{})
	go func() { app.broadcastStatus(); close(published) }()
	select {
	case <-published:
	case <-time.After(time.Second):
		close(w.release)
		cancel()
		<-done
		t.Fatal("first network write blocked publishing")
	}
	close(w.release)
	cancel()
	<-done
	// 重连即使没有新的广播也必须得到最新首帧。
	ctx2, cancel2 := context.WithCancel(context.Background())
	rr := httptest.NewRecorder()
	cancel2()
	app.handleEvents(rr, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx2))
	line := strings.TrimPrefix(strings.Split(rr.Body.String(), "\n")[0], "data: ")
	var snapshot statusSnapshot
	if err := json.Unmarshal([]byte(line), &snapshot); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, snapshot.Devices[0].LastBackupAt)
	if err != nil || !at.Equal(base.Add(time.Hour)) {
		t.Fatalf("stale reconnect: %s", line)
	}
	first := strings.TrimPrefix(strings.Split(w.Body.String(), "\n")[0], "data: ")
	if err := json.Unmarshal([]byte(first), &snapshot); err != nil {
		t.Fatal(err)
	}
	at, err = time.Parse(time.RFC3339, snapshot.Devices[0].LastBackupAt)
	if err != nil || !at.Equal(base) {
		t.Fatal("initial frame missing")
	}

	if app.hub.clientCount() != 0 {
		t.Fatal("disconnected subscriber leaked")
	}
}

func TestConcurrentSSEConnectionsStayOrdered(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	app.devices["CONTROL"] = &device{UDID: "CONTROL", LastBackup: base}
	counter := 0
	for round := 0; round < 10; round++ {
		ctx, cancel := context.WithCancel(context.Background())
		var readers, writers sync.WaitGroup
		records := make([]*httptest.ResponseRecorder, 4)
		for i := range records {
			records[i] = httptest.NewRecorder()
			readers.Add(1)
			go func(rr *httptest.ResponseRecorder) {
				defer readers.Done()
				app.handleEvents(rr, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx))
			}(records[i])
		}
		for i := 0; i < 4; i++ {
			writers.Add(1)
			go func() {
				defer writers.Done()
				for j := 0; j < 100; j++ {
					app.mu.Lock()
					counter++
					app.devices["CONTROL"].LastBackup = base.Add(time.Duration(counter) * time.Second)
					app.mu.Unlock()
					app.broadcastStatus()
				}
			}()
		}
		writers.Wait()
		cancel()
		readers.Wait()
		for _, rr := range records {
			last := time.Time{}
			count := 0
			for _, line := range strings.Split(rr.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var snapshot statusSnapshot
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot); err != nil {
					t.Fatal(err)
				}
				at, err := time.Parse(time.RFC3339, snapshot.Devices[0].LastBackupAt)
				if err != nil {
					t.Fatal(err)
				}
				if at.Before(last) {
					t.Fatalf("SSE connection regressed: %v -> %v", last, at)
				}
				last = at
				count++
			}
			if count == 0 {
				t.Fatalf("missing initial frame: %s", rr.Body.String())
			}
		}
	}
}
