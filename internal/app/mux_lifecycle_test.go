package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countedMuxProcess struct {
	ctx    context.Context
	active *atomic.Int32
	max    *atomic.Int32
}

func (p *countedMuxProcess) Start() error {
	active := p.active.Add(1)
	for {
		current := p.max.Load()
		if active <= current || p.max.CompareAndSwap(current, active) {
			break
		}
	}
	return nil
}

func (p *countedMuxProcess) Wait() error {
	<-p.ctx.Done()
	p.active.Add(-1)
	return nil
}

func newCountedMuxApp(active, max *atomic.Int32) *application {
	app := newApplication()
	app.watchdogBaseDelay = time.Millisecond
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
	app.muxProcFactory = func(ctx context.Context, _ string, _ ...string) muxProcess {
		return &countedMuxProcess{ctx: ctx, active: active, max: max}
	}
	return app
}

func TestConcurrentMuxRestartHasOneSupervisor(t *testing.T) {
	var active, max atomic.Int32
	app := newCountedMuxApp(&active, &max)
	if err := app.StartUSBMuxD(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return active.Load() == 1 }, time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := app.RestartUSBMuxD(); err != nil {
				t.Errorf("RestartUSBMuxD: %v", err)
			}
		}()
	}
	wg.Wait()
	app.StopUSBMuxD()

	if got := active.Load(); got != 0 {
		t.Fatalf("Stop 后仍有 %d 个 mux child 存活", got)
	}
	if got := max.Load(); got > 1 {
		t.Fatalf("并发 restart 产生了 %d 个同时存活的 mux child", got)
	}
}

func TestStopMuxLeavesNoChildOrSupervisor(t *testing.T) {
	var active, max atomic.Int32
	app := newCountedMuxApp(&active, &max)
	for i := 0; i < 100; i++ {
		if err := app.StartUSBMuxD(); err != nil {
			t.Fatalf("第 %d 次 Start: %v", i, err)
		}
		waitFor(t, func() bool { return active.Load() == 1 }, time.Second)
		app.StopUSBMuxD()
		if got := active.Load(); got != 0 {
			t.Fatalf("第 %d 次 Stop 后仍有 %d 个 child", i, got)
		}
	}
}
