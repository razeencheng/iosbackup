package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type pairingFailure struct {
	code, message string
}

type pairingAlertState struct {
	since    time.Time
	failures int
	notified bool
}

// 调用者持有 app.mu。只对到期自动备份的明确配对失效累计证据。
func (app *application) pairingNotificationUnsafe(d *device, state *deviceOperationState, now time.Time) *notificationMessage {
	alert := &state.pairingAlert
	config := app.configs[d.UDID]
	if config == nil || !config.AutoBackupEnabled || config.RemovedAt != nil {
		*alert = pairingAlertState{}
		return nil
	}
	if state.PairingState == pairingStateChecking {
		return nil
	}
	if state.PairingErrorCode != "pairing_required" {
		alert.since, alert.failures = time.Time{}, 0
		return nil
	}
	if alert.since.IsZero() {
		alert.since = now
	}
	if alert.failures < 3 {
		alert.failures++
	}
	if alert.notified || alert.failures < 3 || now.Sub(alert.since) < 5*time.Minute || app.backupInProgress[d.UDID] {
		return nil
	}
	alert.notified = true
	name := d.Name
	if name == "" || name == "Unknown Device" {
		name = config.Name
	}
	if name == "" || name == "Unknown Device" {
		name = "iOS 设备"
	}
	return &notificationMessage{
		Type: notificationSystemError, Level: notificationLevelWarning,
		Title:      fmt.Sprintf("⚠️ 设备需要重新配对: %s", name),
		Content:    fmt.Sprintf("设备 %s 的自动备份检查持续无法通过配对校验。请通过 USB 连接设备，解锁并确认“信任此电脑”，然后重试。", name),
		DeviceName: name, DeviceUDID: d.UDID,
	}
}

func (app *application) checkAutomaticBackupPairing(d *device) bool {
	release, err := app.beginConnectionTask(d)
	if err != nil {
		return false
	}
	defer release()
	epoch := app.connectionEpoch(d)
	state := app.attemptPairingInTask(d, app.pairingPublisher(d, true), false)
	app.mu.RLock()
	defer app.mu.RUnlock()
	current := app.devices[d.UDID]
	return state == pairingStatePaired && app.connectionEpochUnsafe(d) == epoch &&
		app.deviceOperationStates[d.UDID].PairingState == pairingStatePaired &&
		app.connectionAdmissionUnsafe(d) == nil && app.deviceRemovalBlockedUnsafe(d.UDID) == nil &&
		current != nil && current.IsOnline && !current.PresenceUnknown && current.Connection == d.Connection
}

// 校验失败不等于未配对；只有工具明确确认信任记录无效时才允许 USB pair。
func classifyPairingFailure(output []byte, err error, validating bool) pairingFailure {
	text := strings.ToLower(strings.TrimSpace(string(output)))
	if text == "" && err != nil {
		text = strings.ToLower(err.Error())
	}
	switch {
	case strings.Contains(text, "no device found"):
		return pairingFailure{"device_unavailable", "当前连接中找不到设备，请确认设备连接后重试。"}
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(text, "timed out"), strings.Contains(text, "timeout"):
		return pairingFailure{"connection_timeout", "设备连接超时，请稍后重试。"}
	case strings.Contains(text, "passcode is set"):
		return pairingFailure{"device_locked", "设备已锁定。请解锁设备并输入锁屏密码，然后重试。"}
	case strings.Contains(text, "please accept the trust dialog"):
		return pairingFailure{"trust_required", "请在设备上点击“信任此电脑”并输入设备锁屏密码"}
	case strings.Contains(text, "denied the trust dialog"):
		return pairingFailure{"trust_denied", "设备拒绝了信任请求。请通过 USB 连接设备，解锁后重新确认信任。"}
	case strings.Contains(text, "pairing is not possible over this connection"):
		return pairingFailure{"pairing_not_supported", "当前连接不支持配对。如需重新配对，请通过 USB 连接设备并确认信任。"}
	case strings.Contains(text, "not paired"), strings.Contains(text, "invalid host id"), strings.Contains(text, "invalidhostid"):
		return pairingFailure{"pairing_required", "设备需要重新配对。请通过 USB 连接设备，解锁并确认“信任此电脑”。"}
	case strings.Contains(text, "could not connect"), strings.Contains(text, "connection refused"), strings.Contains(text, "connection reset"), errors.Is(err, errConnectionUnavailable), errors.Is(err, errConnectionRestarting):
		return pairingFailure{"connection_unavailable", "暂时无法连接设备，请检查连接状态后重试。"}
	}
	if validating {
		return pairingFailure{"pair_validation_failed", "暂时无法验证设备配对状态，请稍后重试或查看日志。"}
	}
	return pairingFailure{"pair_failed", "配对失败。请确认设备已解锁并保持 USB 连接，然后重试。"}
}

// pairDevice 的结果写入结构化状态，由 /api/events 发布，不因单次失败推送通知。
func (app *application) pairDevice(device *device) {
	release, err := app.beginConnectionTask(device)
	if err != nil {
		return
	}
	defer release()
	app.pairDeviceInTask(device)
}

func (app *application) pairDeviceInTask(device *device) {
	if device == nil || device.UDID == "" || app.deviceRemovalBlock(device.UDID) != nil {
		return
	}
	setState := app.pairingStatePublisher(device)
	setState(device.UDID, pairingStateChecking, "", "")
	if app.attemptPairingInTask(device, setState, true) == pairingStateWaitingForTrust {
		go app.retryPairAfterTrust(device.UDID)
	}
}

// 一次校验/配对流程，供手动操作和信任重试共用。网络连接始终只校验。
func (app *application) attemptPairingInTask(device *device, setState func(string, string, string, string) bool, allowPair bool) string {
	output, err := app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdevicePair, "validate")
	if err == nil {
		if !setState(device.UDID, pairingStatePaired, "", "") {
			return ""
		}
		return pairingStatePaired
	}
	if errors.Is(err, context.Canceled) || app.rootCtx.Err() != nil {
		return ""
	}
	failure := classifyPairingFailure(output, err, true)
	if !allowPair || device.Connection != connectionTypeDesc(connectTypeUSB) || failure.code != "pairing_required" {
		return app.publishPairingFailure(device, setState, failure, output, err)
	}

	app.addInfoLog(device.UDID, "设备尚未配对，开始 USB 配对...")
	output, err = app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdevicePair, "pair")
	if errors.Is(err, context.Canceled) || app.rootCtx.Err() != nil {
		return ""
	}
	if err != nil && !strings.Contains(strings.ToLower(string(output)), "already paired") {
		return app.publishPairingFailure(device, setState, classifyPairingFailure(output, err, false), output, err)
	}

	// pair 成功或声称 already paired 都必须再 validate，不能提前清除错误。
	output, err = app.runIdeviceCmd(app.rootCtx, cmdKindShort, device, cmdIdevicePair, "validate")
	if err != nil {
		if errors.Is(err, context.Canceled) || app.rootCtx.Err() != nil {
			return ""
		}
		return app.publishPairingFailure(device, setState, classifyPairingFailure(output, err, true), output, err)
	}
	app.addInfoLog(device.UDID, "设备配对验证成功")
	if !setState(device.UDID, pairingStatePaired, "", "") {
		return ""
	}
	return pairingStatePaired
}

func (app *application) publishPairingFailure(device *device, setState func(string, string, string, string) bool, failure pairingFailure, output []byte, err error) string {
	detail := strings.TrimSpace(string(output))
	if detail == "" && err != nil {
		detail = err.Error()
	}
	app.addWarnLog(device.UDID, fmt.Sprintf("配对检查未完成 (%s): %s", failure.code, detail))
	state := pairingStateFailed
	if failure.code == "trust_required" {
		state = pairingStateWaitingForTrust
	}
	if !setState(device.UDID, state, failure.code, failure.message) {
		return ""
	}
	return state
}

func (app *application) retryPairAfterTrust(udid string) {
	app.retryPairAfterTrustWithSchedule(udid, 10*time.Second, 6)
}

func (app *application) retryPairAfterTrustWithSchedule(udid string, interval time.Duration, maxRetries int) {
	app.mu.RLock()
	device := cloneDevice(app.devices[udid])
	app.mu.RUnlock()
	if device == nil {
		return
	}
	release, err := app.beginConnectionTask(device)
	if err != nil {
		return
	}
	defer release()
	epoch := app.connectionEpoch(device)
	setState := app.pairingStatePublisher(device)
	for i := 0; i < maxRetries; i++ {
		if !sleepOrDone(app.rootCtx, interval) || app.connectionEpoch(device) != epoch {
			return
		}
		if app.attemptPairingInTask(device, setState, true) != pairingStateWaitingForTrust {
			return
		}
	}
	setState(udid, pairingStateFailed, "pair_timeout", "等待设备确认信任超时，请确认后重试")
}
