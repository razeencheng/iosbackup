package app

import (
	"fmt"
	"sync"
	"testing"
)

func TestRefreshReturnsDeepCopiedDevices(t *testing.T) {
	app := newApplication()
	app.devices["device-1"] = &device{UDID: "device-1", Name: "original"}

	snapshot := app.devicesSnapshot()
	snapshot["device-1"].Name = "mutated"
	delete(snapshot, "device-1")

	app.mu.RLock()
	defer app.mu.RUnlock()
	if got := app.devices["device-1"].Name; got != "original" {
		t.Fatalf("外部快照修改污染内部设备状态: %q", got)
	}
}

func TestNotificationRulesAreCopied(t *testing.T) {
	nm := newNotificationManagerWithLimits(1, 1)
	defer nm.Close()

	original := map[string][]string{"backup_success": {"one"}}
	nm.SetNotificationRules(original)
	original["backup_success"][0] = "mutated"
	if got := nm.GetNotificationRules()["backup_success"][0]; got != "one" {
		t.Fatalf("SetNotificationRules 保留了调用方切片: %q", got)
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			nm.SetNotificationRules(map[string][]string{"backup_success": {fmt.Sprint(i)}})
		}(i)
		go func() {
			defer wg.Done()
			rules := nm.GetNotificationRules()
			if values := rules["backup_success"]; len(values) > 0 {
				values[0] = "caller mutation"
			}
		}()
	}
	wg.Wait()
}
