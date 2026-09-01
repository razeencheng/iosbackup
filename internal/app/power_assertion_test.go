package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakePowerAssertionLease struct {
	done        chan error
	releaseOnce sync.Once
	releases    int
	mu          sync.Mutex
	onRelease   func()
}

func newFakePowerAssertionLease() *fakePowerAssertionLease {
	return &fakePowerAssertionLease{done: make(chan error, 1)}
}

func (l *fakePowerAssertionLease) Done() <-chan error { return l.done }

func (l *fakePowerAssertionLease) Release() {
	l.releaseOnce.Do(func() {
		l.mu.Lock()
		l.releases++
		l.mu.Unlock()
		if l.onRelease != nil {
			l.onRelease()
		}
	})
}

func (l *fakePowerAssertionLease) releaseCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releases
}

type fakePowerAssertionStarter struct {
	mu      sync.Mutex
	starts  int
	startFn func(int) (powerAssertionLease, error)
}

func (s *fakePowerAssertionStarter) Start(_ context.Context, _ string, _ []string) (powerAssertionLease, error) {
	s.mu.Lock()
	s.starts++
	call := s.starts
	s.mu.Unlock()
	return s.startFn(call)
}

func (s *fakePowerAssertionStarter) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

func waitForAssertion(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待 power assertion 状态超时")
}

func TestWiFiPowerAssertionOnlyStartsWhenEnabledForNetwork(t *testing.T) {
	lease := newFakePowerAssertionLease()
	starter := &fakePowerAssertionStarter{startFn: func(int) (powerAssertionLease, error) {
		return lease, nil
	}}

	disabledCfg := defaultRuntimeConfig()
	disabledCfg.WiFiPowerAssertion = false
	app := newApplicationWithRuntime(context.Background(), disabledCfg)
	app.powerAssertionStarter = starter
	if session, err := app.startWiFiPowerAssertion(context.Background(), "NET-DISABLED", true); err != nil || session != nil {
		t.Fatalf("disabled network session=%v err=%v", session, err)
	}
	if starter.startCount() != 0 {
		t.Fatal("disabled assertion must not start helper")
	}

	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app = newApplicationWithRuntime(context.Background(), cfg)
	app.powerAssertionStarter = starter
	if session, err := app.startWiFiPowerAssertion(context.Background(), "USB", false); err != nil || session != nil {
		t.Fatalf("USB session=%v err=%v", session, err)
	}
	if starter.startCount() != 0 {
		t.Fatal("USB backup must not start assertion helper")
	}
}

func TestWiFiPowerAssertionAcquireFailureAborts(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	want := errors.New("assertion rejected")
	app.powerAssertionStarter = &fakePowerAssertionStarter{startFn: func(int) (powerAssertionLease, error) {
		return nil, want
	}}

	if session, err := app.startWiFiPowerAssertion(context.Background(), "NET-FAIL", true); session != nil || !errors.Is(err, want) {
		t.Fatalf("session=%v err=%v", session, err)
	}
}

func TestWiFiPowerAssertionReleaseStopsCurrentLease(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	lease := newFakePowerAssertionLease()
	app.powerAssertionStarter = &fakePowerAssertionStarter{startFn: func(int) (powerAssertionLease, error) {
		return lease, nil
	}}

	session, err := app.startWiFiPowerAssertion(context.Background(), "NET-RELEASE", true)
	if err != nil {
		t.Fatal(err)
	}
	session.Release()
	if lease.releaseCount() != 1 {
		t.Fatalf("release count=%d", lease.releaseCount())
	}
}

func TestWiFiPowerAssertionUnexpectedExitEndsSession(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	lease := newFakePowerAssertionLease()
	app.powerAssertionStarter = &fakePowerAssertionStarter{startFn: func(int) (powerAssertionLease, error) {
		return lease, nil
	}}

	session, err := app.startWiFiPowerAssertion(context.Background(), "NET-EXIT", true)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("helper exited")
	lease.done <- want
	if got := <-session.Done(); !errors.Is(got, want) {
		t.Fatalf("session error=%v", got)
	}
}

func TestWiFiPowerAssertionUnexpectedExitCancelsBackup(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	lease := newFakePowerAssertionLease()
	app.powerAssertionStarter = &fakePowerAssertionStarter{startFn: func(int) (powerAssertionLease, error) {
		return lease, nil
	}}

	backupCtx, cancel := context.WithCancelCause(context.Background())
	release, err := app.guardWiFiBackupPowerAssertion(backupCtx, cancel, "NET-CANCEL", true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease.done <- errors.New("helper exited")
	waitForAssertion(t, func() bool { return errors.Is(context.Cause(backupCtx), errPowerAssertionLost) })
}

func TestWiFiPowerAssertionRenewsBeforeReleasingOldLease(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.powerAssertionRenewInterval = 5 * time.Millisecond
	first := newFakePowerAssertionLease()
	second := newFakePowerAssertionLease()
	var orderMu sync.Mutex
	var order []string
	first.onRelease = func() {
		orderMu.Lock()
		order = append(order, "release-1")
		orderMu.Unlock()
	}
	starter := &fakePowerAssertionStarter{startFn: func(call int) (powerAssertionLease, error) {
		orderMu.Lock()
		order = append(order, "start-"+string(rune('0'+call)))
		orderMu.Unlock()
		if call == 1 {
			return first, nil
		}
		return second, nil
	}}
	app.powerAssertionStarter = starter

	session, err := app.startWiFiPowerAssertion(context.Background(), "NET-RENEW", true)
	if err != nil {
		t.Fatal(err)
	}
	waitForAssertion(t, func() bool { return starter.startCount() >= 2 && first.releaseCount() == 1 })
	if second.releaseCount() != 0 {
		t.Fatal("new lease must remain active after renewal")
	}
	orderMu.Lock()
	gotOrder := append([]string(nil), order...)
	orderMu.Unlock()
	if len(gotOrder) < 3 || gotOrder[0] != "start-1" || gotOrder[1] != "start-2" || gotOrder[2] != "release-1" {
		t.Fatalf("renewal order=%v", gotOrder)
	}
	session.Release()
}

func TestWiFiPowerAssertionRenewFailureKeepsOldLease(t *testing.T) {
	cfg := defaultRuntimeConfig()
	cfg.WiFiPowerAssertion = true
	app := newApplicationWithRuntime(context.Background(), cfg)
	app.powerAssertionRenewInterval = 5 * time.Millisecond
	first := newFakePowerAssertionLease()
	starter := &fakePowerAssertionStarter{startFn: func(call int) (powerAssertionLease, error) {
		if call == 1 {
			return first, nil
		}
		return nil, errors.New("renew failed")
	}}
	app.powerAssertionStarter = starter

	session, err := app.startWiFiPowerAssertion(context.Background(), "NET-KEEP", true)
	if err != nil {
		t.Fatal(err)
	}
	waitForAssertion(t, func() bool { return starter.startCount() >= 2 })
	if first.releaseCount() != 0 {
		t.Fatal("failed renewal must keep old lease")
	}
	session.Release()
	if first.releaseCount() != 1 {
		t.Fatal("session release must close retained lease")
	}
}
