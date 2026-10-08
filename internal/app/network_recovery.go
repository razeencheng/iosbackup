package app

import (
	"context"
	"net"
	"time"
)

const (
	networkRecoveryInterval = 15 * time.Second
	networkRecoveryMaxDelay = 2 * time.Minute
	networkAddressMaxAge    = 24 * time.Hour
)

type networkRecoveryState struct {
	ip           string
	seen         time.Time
	configuredIP string
	nextAttempt  time.Time
	delay        time.Duration
}

// networkRecoveryLoop 独立于自动备份和浏览器刷新；手机恢复可访问后无需新的 mDNS 事件。
func (app *application) networkRecoveryLoop(ctx context.Context) {
	if app.usesUSBMuxd2WiFi() {
		return
	}
	app.recoverNetworkDevices(ctx, time.Now())
	ticker := time.NewTicker(networkRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			app.recoverNetworkDevices(ctx, now)
		}
	}
}

// recoverNetworkDevices 只缓存成功注册的地址，不保存为手动 IP，也不直接设置在线状态。
// 缓存最多保留一天；mDNS 后续发现新地址时替换它。失败按 15/30/60/120 秒退避。
func (app *application) recoverNetworkDevices(ctx context.Context, now time.Time) {
	if ctx.Err() != nil || app.usesUSBMuxd2WiFi() || !app.networkRecoveryMu.TryLock() {
		return
	}
	defer app.networkRecoveryMu.Unlock()
	online, err := app.lookupNetworkIPs(ctx)
	if err != nil || ctx.Err() != nil {
		return
	} // 查询失败不能当作全部设备掉线。

	app.mu.Lock()
	if app.networkRecovery == nil {
		app.networkRecovery = make(map[string]networkRecoveryState)
	}
	for udid := range app.networkRecovery {
		if app.configs[udid] == nil || app.deviceRemovalBlockedUnsafe(udid) != nil {
			delete(app.networkRecovery, udid)
		}
	}
	candidates := make(map[string]string)
	for udid, cfg := range app.configs {
		if cfg == nil || app.deviceRemovalBlockedUnsafe(udid) != nil {
			continue
		}
		state := app.networkRecovery[udid]
		if state.configuredIP != cfg.NetworkAddress {
			state = networkRecoveryState{configuredIP: cfg.NetworkAddress}
		}
		if ip, exists := online[udid]; exists {
			if net.ParseIP(ip) != nil {
				state.ip, state.seen = ip, now
			}
			state.delay, state.nextAttempt = 0, now.Add(networkRecoveryInterval)
		} else if !app.deviceRemovalBusyUnsafe(udid) && !now.Before(state.nextAttempt) {
			ip := cfg.NetworkAddress
			if ip == "" && now.Sub(state.seen) < networkAddressMaxAge {
				ip = state.ip
			}
			if net.ParseIP(ip) != nil {
				candidates[udid] = ip
			}
		}
		app.networkRecovery[udid] = state
	}
	app.mu.Unlock()

	for udid, ip := range candidates {
		if ctx.Err() != nil {
			return
		}
		// 网络查询后配置可能已改变；每次操作前重新检查。
		app.mu.RLock()
		cfg := app.configs[udid]
		state := app.networkRecovery[udid]
		eligible := cfg != nil && cfg.NetworkAddress == state.configuredIP &&
			app.deviceRemovalBlockedUnsafe(udid) == nil && !app.deviceRemovalBusyUnsafe(udid)
		app.mu.RUnlock()
		if !eligible {
			continue
		}
		app.addDebugLog(udid, "Wi-Fi 设备已不在注册表，尝试恢复连接")
		err := app.callAddDeviceContext(ctx, udid, ip)
		app.mu.Lock()
		if state.delay == 0 || err == nil {
			state.delay = networkRecoveryInterval
		} else {
			state.delay *= 2
		}
		if state.delay > networkRecoveryMaxDelay {
			state.delay = networkRecoveryMaxDelay
		}
		state.nextAttempt = now.Add(state.delay)
		app.networkRecovery[udid] = state
		app.mu.Unlock()
	}
}
