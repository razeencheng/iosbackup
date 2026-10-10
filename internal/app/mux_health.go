package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	connectionUSB          = 0
	connectionWiFi         = 1
	connectionListTimeout  = 3 * time.Second
	connectionStartupGrace = 15 * time.Second
)

var errConnectionRestarting = errors.New("连接服务正在恢复，请稍后重试")
var errConnectionUnavailable = errors.New("连接服务异常，设备状态暂时无法确认")

type connectionScan struct {
	devices              map[string]*device
	networkDevices       map[string]*device
	err                  error
	code                 string
	sequence, generation uint64
	completed            time.Time
}

type connectionFlight struct {
	done   chan struct{}
	result connectionScan
}

type connectionService struct {
	scan                                               connectionScan
	generation, sequence                               uint64
	failures                                           int
	health, phase, code                                string
	lastSuccess, healthySince, graceUntil, nextAttempt time.Time
	attempts                                           []time.Time
	backoff                                            int
	configRestart                                      bool
	notified                                           bool
}

type connectionServiceDTO struct {
	Connection  string `json:"connection"`
	Backend     string `json:"backend"`
	Health      string `json:"health"`
	Recovery    string `json:"recovery"`
	ErrorCode   string `json:"error_code,omitempty"`
	LastSuccess string `json:"last_success,omitempty"`
	NextAttempt string `json:"next_attempt,omitempty"`
	Attempts    int    `json:"attempts"`
	Reason      string `json:"reason,omitempty"`
}

// 状态、准入和代次均由 app.mu 保护；外部命令不持该锁。
func (app *application) connectionBackend(logical int) int {
	if logical == connectionWiFi && app.usesUSBMuxd2WiFi() {
		return connectionUSB
	}
	return logical
}

func (app *application) connectionServicesUnsafe() []connectionServiceDTO {
	out := make([]connectionServiceDTO, 0, 2)
	for logical, name := range []string{"usb", "wifi"} {
		backend := app.connectionBackend(logical)
		s := app.connectionServices[backend]
		dto := connectionServiceDTO{Connection: name, Backend: "usbmuxd2", Health: s.health, Recovery: s.phase, ErrorCode: s.code, Attempts: len(s.attempts)}
		if backend == connectionWiFi {
			dto.Backend = "netmuxd"
		}
		if dto.Health == "" {
			dto.Health = "unknown"
		}
		if dto.Recovery == "" {
			dto.Recovery = "idle"
		}
		if !s.lastSuccess.IsZero() {
			dto.LastSuccess = toBeijingTime(s.lastSuccess).Format(time.RFC3339)
		}
		if !s.nextAttempt.IsZero() {
			dto.NextAttempt = toBeijingTime(s.nextAttempt).Format(time.RFC3339)
		}
		if dto.Recovery == "waiting_busy" {
			dto.Reason = "device_tasks"
		}
		out = append(out, dto)
	}
	return out
}

func parseConnectionLists(output []byte) (map[string]*device, map[string]*device, error) {
	usb, network := make(map[string]*device), make(map[string]*device)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) > 2 || !udidPattern.MatchString(fields[0]) {
			return nil, nil, fmt.Errorf("无效设备列表格式")
		}
		conn := connectTypeUSB
		if len(fields) == 2 {
			switch strings.ToLower(fields[1]) {
			case "(usb)":
			case "(network)":
				conn = connectTypeNetwork
			default:
				return nil, nil, fmt.Errorf("无效连接类型")
			}
		}
		d := &device{UDID: fields[0], Connection: connectionTypeDesc(conn), IsOnline: true}
		if conn == connectTypeUSB {
			usb[d.UDID] = d
		} else {
			network[d.UDID] = d
		}
	}
	return mergeDeviceLists(usb, network), network, nil
}

func connectionErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "query_timeout"
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, exec.ErrNotFound) {
		return "tool_unavailable"
	}
	return "query_failed"
}

// 同一物理后端只允许一个枚举在途；并发请求共享观测，不重复计数。
func (app *application) scanConnection(parent context.Context, backend int, recovery bool) connectionScan {
	app.mu.Lock()
	if parent.Err() != nil {
		app.mu.Unlock()
		return connectionScan{err: parent.Err()}
	}
	if app.connectionRecoveryActive && !recovery {
		app.mu.Unlock()
		return connectionScan{err: errConnectionRestarting}
	}
	if flight := app.connectionFlights[backend]; flight != nil {
		app.mu.Unlock()
		select {
		case <-parent.Done():
			return connectionScan{err: parent.Err()}
		case <-flight.done:
			return flight.result
		}
	}
	flight := &connectionFlight{done: make(chan struct{})}
	app.connectionFlights[backend] = flight
	s := &app.connectionServices[backend]
	s.sequence++
	result := connectionScan{sequence: s.sequence, generation: s.generation}
	app.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, connectionListTimeout)
	stop := context.AfterFunc(app.rootCtx, cancel)
	defer stop()
	args := []string{"-l"}
	env := os.Environ()
	// 移除外部继承的 socket 覆盖，USB 始终使用默认服务。
	env = withoutEnv(env, "USBMUXD_SOCKET_ADDRESS")
	if backend == connectionWiFi {
		env = append(env, app.networkIdeviceEnvVars()...)
		args = append(args, "-n")
	} else if app.usesUSBMuxd2WiFi() {
		args = append(args, "-n")
	}
	runner := app.cmdRunner
	if runner == nil {
		runner = defaultConnectionListRunner
	}
	out, err := runner(ctx, cmdIdeviceID, args, env)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		result.devices, result.networkDevices, err = parseConnectionLists(out)
		if err != nil {
			result.code = "invalid_response"
		}
	}
	cancel()
	result.err = err
	if result.code == "" {
		result.code = connectionErrorCode(err)
	}
	result.completed = nowBeijing()
	if parent.Err() == nil && app.rootCtx.Err() == nil && !errors.Is(err, context.Canceled) {
		app.publishConnectionScan(backend, result)
	}
	app.mu.Lock()
	if result.generation != app.connectionServices[backend].generation {
		result.err = errConnectionUnavailable
		result.devices = nil
		result.networkDevices = nil
	}
	flight.result = result
	app.connectionFlights[backend] = nil
	close(flight.done)
	app.mu.Unlock()
	return result
}

func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(v, key+"=") {
			out = append(out, v)
		}
	}
	return out
}

func (app *application) publishConnectionScan(backend int, result connectionScan) {
	app.connectionPublishMu.Lock()
	defer app.connectionPublishMu.Unlock()
	app.mu.Lock()
	s := &app.connectionServices[backend]
	if result.generation != s.generation || result.sequence <= s.scan.sequence {
		app.mu.Unlock()
		return
	}
	previous := s.health
	s.scan = result
	if result.err == nil {
		if previous == "suspect" || previous == "unhealthy" {
			app.requestConnectionRefreshUnsafe()
		}
		s.health, s.code, s.failures = "healthy", "", 0
		if s.healthySince.IsZero() {
			s.healthySince = result.completed
		}
		if result.completed.Sub(s.healthySince) >= time.Minute {
			s.backoff = 0
			s.notified = false
		}
		s.lastSuccess = result.completed
		if s.phase != "restarting" && !s.configRestart {
			s.phase = "idle"
			s.nextAttempt = time.Time{}
		}
	} else {
		s.failures++
		s.healthySince = time.Time{}
		s.code, s.health = result.code, "suspect"
		if s.failures >= 3 {
			s.health = "unhealthy"
		}
		if s.code == "tool_unavailable" {
			s.phase = "manual_required"
		}
	}
	notifyFailure := result.err != nil && !errors.Is(result.err, context.Canceled) && app.rootCtx.Err() == nil &&
		s.failures >= 3 && !result.completed.Before(s.graceUntil) && !s.notified
	if notifyFailure {
		s.notified = true
	}
	app.connectionRevision++
	revision := app.connectionRevision
	usb, network, valid := app.connectionListsUnsafe()
	app.mu.Unlock()
	if notifyFailure {
		name := "USB 连接服务"
		if backend == connectionWiFi {
			name = "Wi-Fi 连接服务"
		} else if app.usesUSBMuxd2WiFi() {
			name = "USB / Wi-Fi 连接服务"
		}
		app.notifySystemError(name+"持续不可用", "设备列表已连续多次查询失败，暂时无法确认设备状态或开始新备份。请查看页面中的连接服务状态及日志；如自动恢复未成功，请检查容器和设备连接。")
	}
	_, created := app.applyPresenceSnapshotValid(mergeDeviceLists(usb, network), network, result.completed, valid, revision)
	if created {
		_ = app.saveConfigs()
	}
	if result.err != nil && previous != "suspect" && previous != "unhealthy" {
		app.addWarnLog("SYSTEM", fmt.Sprintf("连接服务查询异常：backend=%d generation=%d scan=%d code=%s", backend, result.generation, result.sequence, result.code))
	}
	app.broadcastStatus()
}

func (app *application) connectionListsUnsafe() (map[string]*device, map[string]*device, [2]bool) {
	lists := [2]map[string]*device{make(map[string]*device), make(map[string]*device)}
	valid := [2]bool{}
	for logical := 0; logical < 2; logical++ {
		s := &app.connectionServices[app.connectionBackend(logical)]
		valid[logical] = s.scan.sequence != 0 && s.scan.generation == s.generation && s.scan.err == nil
		if !valid[logical] {
			continue
		}
		devices := s.scan.devices
		if logical == connectionWiFi {
			devices = s.scan.networkDevices
		}
		for udid, d := range devices {
			network := isNetworkConnection(d.Connection)
			if (logical == connectionWiFi) == network {
				lists[logical][udid] = cloneDevice(d)
			}
		}
	}
	return lists[0], lists[1], valid
}

func (app *application) scanConnections(ctx context.Context) {
	var wg sync.WaitGroup
	count := 2
	if app.usesUSBMuxd2WiFi() {
		count = 1
	}
	for backend := 0; backend < count; backend++ {
		wg.Add(1)
		go func(backend int) { defer wg.Done(); app.scanConnection(ctx, backend, false) }(backend)
	}
	wg.Wait()
}

// 列表查询只允许短暂收尾，不能沿用备份命令的 3 秒退出宽限。
func defaultConnectionListRunner(ctx context.Context, name string, args, env []string) ([]byte, error) {
	output := newTailBuffer(1 << 20)
	cmd := newExecCmd(ctx, name, args...)
	cmd.SetEnv(env)
	cmd.SetStdout(output)
	cmd.SetStderr(output)
	cmd.SetGracefulCancel(100 * time.Millisecond)
	err := cmd.Run()
	return output.Bytes(), err
}
