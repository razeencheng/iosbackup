package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// ---- 常量 ----

const (
	// cmdNetmuxd netmuxd 二进制绝对路径（沿用 cmd* 绝对路径风格）
	cmdNetmuxd = "/usr/local/bin/netmuxd"

	// cmdAddDevice add_device 二进制绝对路径
	cmdAddDevice = "/usr/local/bin/add_device"

	// netmuxdAddr netmuxd 监听地址（注入到 USBMUXD_SOCKET_ADDRESS 的值）
	netmuxdAddr = "127.0.0.1:27015"

	// opensslConfPath OpenSSL3 兼容配置（放宽设备 TLS 弱签名/seclevel）。
	// 仅对网络设备命令注入 OPENSSL_CONF，不设全局 ENV（见 Dockerfile final-prep）。
	opensslConfPath = "/etc/ssl/iosbk-openssl.cnf"

	// 看门狗参数
	watchdogInitDelay = 1 * time.Second
	watchdogMaxDelay  = 60 * time.Second
)

// ---- muxProcess：可监督的子进程抽象（便于测试注入 fake）----

// muxProcess 抽象一个可启动/等待的子进程。*execCmd 已满足该接口。
type muxProcess interface {
	Start() error
	Wait() error
}

// muxProcessEnv 是真实进程和需要检查环境变量的测试进程可选实现的能力。
// 不把它放进 muxProcess 主接口，避免纯监督测试必须关心进程环境。
type muxProcessEnv interface {
	SetEnv([]string)
}

// ---- 超时分级 ----

// cmdKind 命令超时分级
type cmdKind int

const (
	// cmdKindShort 短命令（idevice_id -l、ideviceinfo -k、idevicepair validate/pair）约 12s 超时
	cmdKindShort cmdKind = iota
	// cmdKindMedium 中等命令（idevicebackup2 info/list）：会从设备下载清单，耗时随备份大小增长，
	// 用分钟级有界超时——既容得下大备份/Wi-Fi，又不会真卡死时永久占用。
	cmdKindMedium
	// cmdKindLong 长命令（idevicebackup2 backup/restore/unback）无固定超时，随父 context 取消
	cmdKindLong
)

const (
	shortCmdTimeout  = 12 * time.Second
	mediumCmdTimeout = 5 * time.Minute
)

// ---- CmdRunner 类型：可注入的 exec 抽象 ----

// CmdRunner 是执行外部命令的可注入函数类型。
// ctx 已携带超时/取消语义；name 为二进制路径；args 为参数；env 为完整环境变量切片。
// 返回 stdout+stderr 合并输出和错误。
type cmdRunner func(ctx context.Context, name string, args []string, env []string) ([]byte, error)

// defaultCmdRunner 是生产环境的真实 exec runner。
func defaultCmdRunner(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	// 短命令同样设 1 MiB 硬上限，避免异常工具输出把 NAS 内存打满。
	output := newTailBuffer(1 << 20)
	err := (defaultStreamCmdRunner{}).Run(ctx, name, args, env, output, output)
	return output.Bytes(), err
}

// ---- muxSocketFor：根据设备连接类型返回 socket 地址 ----

// muxSocketFor 返回该设备应使用的 USBMUXD_SOCKET_ADDRESS。
// Network 设备 → netmuxdAddr；USB 设备 → ""（使用 usbmuxd2 默认 socket）。
func muxSocketFor(device *device) string {
	if device.Connection == connectionTypeDesc(connectTypeNetwork) {
		return netmuxdAddr
	}
	return ""
}

// ---- runIdeviceCmd：统一封装，socket 注入 + 超时分级 ----

// netmuxdEnvVars 返回网络设备命令需注入的环境变量：
//   - USBMUXD_SOCKET_ADDRESS → 连 netmuxd（Wi-Fi 路径）
//   - OPENSSL_CONF → 放宽设备 TLS 弱签名/seclevel（仅设备命令注入，非全局 ENV）
func netmuxdEnvVars() []string {
	return []string{
		"USBMUXD_SOCKET_ADDRESS=" + netmuxdAddr,
		"OPENSSL_CONF=" + opensslConfPath,
	}
}

func (app *application) usesUSBMuxd2WiFi() bool {
	return app.runtimeConfig.WiFiBackend == wifiBackendUSBMuxd2
}

// networkIdeviceEnvVars 返回网络设备命令使用的环境。
// netmuxd 模式连接 127.0.0.1:27015；usbmuxd2 诊断模式使用默认 unix socket。
// 两种模式都保留设备 TLS 兼容配置。
func (app *application) networkIdeviceEnvVars() []string {
	if app.usesUSBMuxd2WiFi() {
		return []string{"OPENSSL_CONF=" + opensslConfPath}
	}
	return netmuxdEnvVars()
}

// runIdeviceCmd 是所有 idevice* 命令的统一调用入口。
//
//   - ctx：父 context（长命令直接使用；短命令内部追加 timeout）
//   - kind：cmdKindShort / cmdKindLong
//   - device：决定 socket 注入和 -n/-u 参数；可为 nil（用于列表查询）
//   - bin：二进制绝对路径（用 cmd* 常量）
//   - extraArgs：业务参数（不含 -u/-n，由此函数注入）
//
// 网络设备自动注入：
//  1. USBMUXD_SOCKET_ADDRESS=127.0.0.1:27015（env）
//  2. -n 标志（args，放在其他参数前）
//
// 所有设备注入 -u <UDID>（仅当不是 idevice_id -l 列表命令时）。
func (app *application) runIdeviceCmd(ctx context.Context, kind cmdKind, device *device, bin string, extraArgs ...string) ([]byte, error) {
	return app.runIdeviceCmdEnv(ctx, kind, device, nil, bin, extraArgs...)
}

// runIdeviceCmdEnv 与 runIdeviceCmd 相同，但额外注入 extraEnv（如 BACKUP_PASSWORD/BACKUP_PASSWORD_NEW）。
// 密码类只走此通道（cmd.Env），绝不进 argv（避免 /proc/<pid>/cmdline 泄漏）。
func (app *application) runIdeviceCmdEnv(ctx context.Context, kind cmdKind, device *device, extraEnv []string, bin string, extraArgs ...string) ([]byte, error) {
	cmdCtx, cancel, args, env := app.ideviceCommand(ctx, kind, device, extraEnv, bin, extraArgs)
	if cancel != nil {
		defer cancel()
	}
	runner := app.cmdRunner
	if runner == nil {
		runner = defaultCmdRunner
	}

	return runner(cmdCtx, bin, args, env)
}

func (app *application) ideviceCommand(ctx context.Context, kind cmdKind, device *device, extraEnv []string, bin string, extraArgs []string) (context.Context, context.CancelFunc, []string, []string) {
	// 超时分级
	var cmdCtx context.Context
	var cancel context.CancelFunc
	switch kind {
	case cmdKindShort:
		cmdCtx, cancel = context.WithTimeout(ctx, shortCmdTimeout)
	case cmdKindMedium:
		cmdCtx, cancel = context.WithTimeout(ctx, mediumCmdTimeout)
	default:
		// 长命令：直接使用父 context（可取消，无固定超时）
		cmdCtx = ctx
	}

	// 判断是否是列表命令（idevice_id -l，不需要 -u）
	needUDID := device != nil && !isListCommand(bin, extraArgs)

	// 是否是网络设备
	isNetwork := device != nil && device.Connection == connectionTypeDesc(connectTypeNetwork)

	// 构造参数
	var args []string
	if isNetwork {
		args = append(args, "-n")
	}
	if needUDID {
		args = append(args, "-u", device.UDID)
	}
	args = append(args, extraArgs...)

	// 构造环境变量（一律 cmd.Env，禁用 os.Setenv 全局注入）
	env := os.Environ()
	if isNetwork {
		env = append(env, app.networkIdeviceEnvVars()...)
	}
	env = append(env, extraEnv...) // 密码等敏感值仅经 env

	return cmdCtx, cancel, args, env
}

func (app *application) runIdeviceCmdStreamEnv(ctx context.Context, kind cmdKind, device *device, extraEnv []string, bin string, stdout, stderr io.Writer, extraArgs ...string) error {
	cmdCtx, cancel, args, env := app.ideviceCommand(ctx, kind, device, extraEnv, bin, extraArgs)
	if cancel != nil {
		defer cancel()
	}
	if app.streamCmdRunner != nil {
		return app.streamCmdRunner.Run(cmdCtx, bin, args, env, stdout, stderr)
	}
	// 兼容现有注入式单元测试；生产路径不设置 cmdRunner，因此始终真正流式执行。
	if app.cmdRunner != nil {
		out, err := app.cmdRunner(cmdCtx, bin, args, env)
		if len(out) > 0 {
			_, _ = stdout.Write(out)
		}
		return err
	}
	return (defaultStreamCmdRunner{}).Run(cmdCtx, bin, args, env, stdout, stderr)
}

// isListCommand 判断是否是 idevice_id -l 列表命令（不需要 -u UDID）。
func isListCommand(bin string, args []string) bool {
	if !strings.HasSuffix(bin, "idevice_id") {
		return false
	}
	for _, a := range args {
		if a == "-l" {
			return true
		}
	}
	return false
}

// ---- nextWatchdogDelay：指数退避计算 ----

// nextWatchdogDelay 返回下一次退避延迟（翻倍但不超过 watchdogMaxDelay）。
func nextWatchdogDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > watchdogMaxDelay {
		return watchdogMaxDelay
	}
	return next
}

// ---- listDevicesFromBothSockets：双 socket 查询 ----

// listDevicesFromBothSockets 分别从 usbmuxd2 和 netmuxd 获取设备列表。
//
//   - USB 列表：默认 socket 的 idevice_id -l（不加 -n，避免带出 usbmuxd2 的 Network 设备）
//   - 网络列表：netmuxd env 的 idevice_id -l -n
func (app *application) listDevicesFromBothSockets() (usbDevices, netDevices map[string]*device) {
	usbDevices = make(map[string]*device)
	netDevices = make(map[string]*device)

	runner := app.cmdRunner
	if runner == nil {
		runner = defaultCmdRunner
	}

	// USB 列表：默认 socket，不加 -n
	ctx1, cancel1 := context.WithTimeout(context.Background(), shortCmdTimeout)
	defer cancel1()
	usbEnv := os.Environ() // 不注入 USBMUXD_SOCKET_ADDRESS
	usbOut, err := runner(ctx1, cmdIdeviceID, []string{"-l"}, usbEnv)
	if err == nil {
		usbDevices = parseDeviceList(string(usbOut))
	}

	// 网络列表：netmuxd 模式使用 TCP socket；usbmuxd2 模式使用默认 unix socket。
	ctx2, cancel2 := context.WithTimeout(context.Background(), shortCmdTimeout)
	defer cancel2()
	netEnv := append(os.Environ(), app.networkIdeviceEnvVars()...)
	netOut, err := runner(ctx2, cmdIdeviceID, []string{"-l", "-n"}, netEnv)
	if err == nil {
		netDevices = parseDeviceList(string(netOut))
	}

	return usbDevices, netDevices
}

// parseDeviceList 解析 idevice_id 输出，格式："UDID (ConnectionType)\n"
func parseDeviceList(output string) map[string]*device {
	result := make(map[string]*device)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		udid := parts[0]
		// idevice_id -l（USB 默认 socket，不加 -n）输出裸 UDID、无后缀 → 默认按 USB；
		// idevice_id -l -n（netmuxd）输出 "UDID (Network)" → 取括号内类型。
		connType := connectTypeUSB
		if len(parts) >= 2 {
			connType = strings.ToLower(strings.Trim(strings.Join(parts[1:], " "), "()"))
		}
		result[udid] = &device{
			UDID:       udid,
			Connection: connectionTypeDesc(connType),
			IsOnline:   true,
		}
	}
	return result
}

// mergeDeviceLists 合并 USB 和网络设备列表，同 UDID 时 USB 优先。
func mergeDeviceLists(usbDevices, netDevices map[string]*device) map[string]*device {
	merged := make(map[string]*device)

	// 先放入网络设备
	for udid, dev := range netDevices {
		merged[udid] = dev
	}
	// USB 设备覆盖（USB 优先）
	for udid, dev := range usbDevices {
		merged[udid] = dev
	}

	return merged
}

// netmuxd 同步动作（纯字符串常量，便于测试）。
const (
	netmuxdNoop   = ""
	netmuxdReplay = "replay"
	netmuxdReset  = "reset"
)

// netmuxdSyncDecision 根据设备 IP 的新旧值决定 netmuxd 同步动作：
//   - 旧 IP 非空且发生变更（改址或清空）→ reset：netmuxd 不会更新已存 UDID 的地址，
//     必须重启清掉旧条目，再重放当前所有 IP，否则旧 IP 残留。
//   - 新增 IP（旧空新非空）或不变但仍有 IP → replay：add_device 幂等。
//   - 否则 → noop。
func netmuxdSyncDecision(oldIP, newIP string) string {
	if oldIP != "" && newIP != oldIP {
		return netmuxdReset
	}
	if newIP != "" {
		return netmuxdReplay
	}
	return netmuxdNoop
}

// ---- replayAddDevice：add_device 重放 ----

// replayAddDevice 对所有「有 NetworkAddress 但当前不在 netmuxd Network 列表」的设备调用 add_device。
// 时机：① netmuxd 启动后 ② 保存含 IP 配置后 ③ netmuxd 看门狗重启后。
// 先查再加，不盲目重放（避免 helper panic 刷屏）。
func (app *application) replayAddDevice() {
	if app.usesUSBMuxd2WiFi() {
		return
	}
	// 1. 查询当前 netmuxd 列表
	currentNetDevices := app.currentNetmuxdDevices()

	// 2. 读取所有配置（持短读锁）
	app.mu.RLock()
	cfgs := make(map[string]*backupConfig, len(app.configs))
	for udid, cfg := range app.configs {
		cfgs[udid] = cloneBackupConfig(cfg)
	}
	app.mu.RUnlock()

	// 3. 对「有 IP 且不在当前列表」的设备调用 add_device
	for udid, cfg := range cfgs {
		if cfg == nil || cfg.NetworkAddress == "" || cfg.RemovedAt != nil {
			continue
		}
		if _, alreadyIn := currentNetDevices[udid]; alreadyIn {
			app.addDebugLog(udid, fmt.Sprintf("设备已在 netmuxd 列表，跳过 add_device（IP: %s）", cfg.NetworkAddress))
			continue
		}
		app.addInfoLog(udid, fmt.Sprintf("调用 add_device: udid=%s ip=%s", udid, cfg.NetworkAddress))
		app.callAddDevice(udid, cfg.NetworkAddress)
	}
}

// currentNetmuxdDevices 通过 idevice_id -l -n（netmuxd env）获取当前 netmuxd 设备列表。
func (app *application) currentNetmuxdDevices() map[string]*device {
	return app.currentNetmuxdDevicesContext(context.Background())
}

func (app *application) currentNetmuxdDevicesContext(parent context.Context) map[string]*device {
	runner := app.cmdRunner
	if runner == nil {
		runner = defaultCmdRunner
	}

	ctx, cancel := context.WithTimeout(parent, shortCmdTimeout)
	defer cancel()

	netEnv := withEnvOverride(os.Environ(), "USBMUXD_SOCKET_ADDRESS", netmuxdAddr)
	out, err := runner(ctx, cmdIdeviceID, []string{"-l", "-n"}, netEnv)
	if err != nil {
		return make(map[string]*device)
	}
	return parseDeviceList(string(out))
}

// wifiLockdownPort 设备在 Wi-Fi 上 lockdownd 的监听端口；端口可达不代表已注册到 netmuxd。
const wifiLockdownPort = "62078"

// reachableTCP 探测 addr 能否建立 TCP 连接，返回 (是否可达, 中文原因)。
func reachableTCP(addr string, timeout time.Duration) (bool, string) {
	return reachableTCPContext(context.Background(), addr, timeout)
}

func reachableTCPContext(ctx context.Context, addr string, timeout time.Duration) (bool, string) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err == nil {
		_ = conn.Close()
		return true, ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no route to host"):
		return false, "无法连接（无路由）——设备多半不在同一 Wi-Fi/网段，或 IP 已变"
	case strings.Contains(msg, "timeout"):
		return false, "连接超时——设备可能离线/休眠，或被网络隔离"
	case strings.Contains(msg, "refused"):
		return false, "连接被拒——IP 在网但该端口未监听（可能未开 Wi-Fi 同步）"
	default:
		return false, "连不上：" + err.Error()
	}
}

// wifiReachable 探测设备 IP 的 lockdown-over-Wi-Fi 端口（62078）是否可达。
func wifiReachable(ip string) (bool, string) {
	return reachableTCP(net.JoinHostPort(ip, wifiLockdownPort), 4*time.Second)
}

// probeReachable 经可注入探针判断 IP 可达性（测试可注入 stub，避免真实拨号）。
func (app *application) probeReachable(ip string) (bool, string) {
	if app.reachProbe != nil {
		return app.reachProbe(ip)
	}
	return wifiReachable(ip)
}

// callAddDevice 调用 add_device 二进制并处理 Failure/Success 语义。
// add_device 语义（已真机验证）：
//   - 首次注入 → "Success"
//   - 已存在设备再次调用 → helper 可能 panic 或一直等待响应，应先查注册表避免重复
//   - 死 IP → "Failure"
func (app *application) callAddDevice(udid, ip string) error {
	ctx := app.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return app.callAddDeviceContext(ctx, udid, ip)
}

func (app *application) callAddDeviceContext(parent context.Context, udid, ip string) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if app.usesUSBMuxd2WiFi() {
		app.addWarnLog(udid, fmt.Sprintf("usbmuxd2 Wi-Fi 模式仅支持同网段 mDNS 自动发现，忽略手工 IP %s", ip))
		return fmt.Errorf("当前 Wi-Fi 连接模式不支持手动 IP，请使用同网段自动发现或切换到 netmuxd")
	}
	app.mu.Lock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.Unlock()
		return err
	}
	if app.deviceRemovalBusyUnsafe(udid) {
		app.mu.Unlock()
		// 已注册时只是查询，不干扰正在进行的备份；未注册时禁止重连。
		if _, exists := app.currentNetmuxdDevicesContext(parent)[udid]; exists {
			return nil
		}
		return errDeviceBusy
	}
	if app.networkRegistrations == nil {
		app.networkRegistrations = make(map[string]bool)
	}
	app.networkRegistrations[udid] = true
	app.mu.Unlock()
	defer func() { app.mu.Lock(); delete(app.networkRegistrations, udid); app.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(parent, shortCmdTimeout)
	defer cancel()
	// 先快速探测可达性：避免 add_device 连不可达 IP 时耗尽超时。
	probe := app.reachProbe
	if probe == nil {
		probe = func(ip string) (bool, string) {
			return reachableTCPContext(ctx, net.JoinHostPort(ip, wifiLockdownPort), 4*time.Second)
		}
	}
	if ok, reason := probe(ip); !ok {
		app.addWarnLog(udid, fmt.Sprintf("add_device 跳过：IP %s 不可达（%s）", ip, reason))
		return fmt.Errorf("%s", reason)
	}
	// 已注册设备重复添加可能触发 helper panic。
	if _, exists := app.currentNetmuxdDevicesContext(ctx)[udid]; exists {
		return nil
	}

	runner := app.cmdRunner
	if runner == nil {
		runner = defaultCmdRunner
	}

	// helper 默认连接 USB unix socket，必须显式指向接收 AddDevice 的 netmuxd。
	env := withEnvOverride(os.Environ(), "USBMUXD_SOCKET_ADDRESS", netmuxdAddr)
	env = withEnvOverride(env, "OPENSSL_CONF", opensslConfPath)
	if err := ctx.Err(); err != nil {
		return err
	}
	out, err := runner(ctx, cmdAddDevice, []string{udid, ip}, env)
	output := strings.TrimSpace(string(out))
	if parent.Err() != nil {
		return parent.Err()
	}
	// helper 可能在注册已成功时仍等待响应；以注册表复核超时/未知响应。
	if err != nil || !strings.Contains(output, "Success") {
		verifyCtx, verifyCancel := context.WithTimeout(parent, 2*time.Second)
		_, registered := app.currentNetmuxdDevicesContext(verifyCtx)[udid]
		verifyCancel()
		if registered {
			app.addInfoLog(udid, fmt.Sprintf("add_device 响应未确认，但注册表已确认设备上线: ip=%s", ip))
			return nil
		}
	}

	if err != nil {
		app.addWarnLog(udid, fmt.Sprintf("add_device 执行失败且未查询到设备注册: ip=%s err=%v output=%q", ip, err, output))
		return fmt.Errorf("IP 可达，但连接服务注册失败，请检查 Wi-Fi 连接服务和设备配对状态")
	}

	switch {
	case strings.Contains(output, "Success"):
		app.addInfoLog(udid, fmt.Sprintf("add_device 成功: ip=%s", ip))
		return nil
	case strings.Contains(output, "Failure"):
		app.addWarnLog(udid, fmt.Sprintf("add_device 返回 Failure，设备未注册: ip=%s", ip))
		return fmt.Errorf("IP 可达，但设备未能注册到 Wi-Fi 连接服务，请确认 IP 属于此设备且已完成配对")
	default:
		app.addWarnLog(udid, fmt.Sprintf("add_device 输出未知且未查询到设备注册: ip=%s output=%q", ip, output))
		return fmt.Errorf("IP 可达，但 Wi-Fi 连接服务未确认注册成功，请重试并检查服务日志")
	}
}

// ---- 统一进程监督（supervisor）----

// backoffStart 返回看门狗退避基数（测试可通过 watchdogBaseDelay 调小）。
func (app *application) backoffStart() time.Duration {
	if app.watchdogBaseDelay > 0 {
		return app.watchdogBaseDelay
	}
	return watchdogInitDelay
}

// launchMux 创建一个受监督的子进程（生产用真实 exec，测试用注入的 fake）。
func (app *application) launchMux(ctx context.Context, name string, args ...string) muxProcess {
	var proc muxProcess
	if app.muxProcFactory != nil {
		proc = app.muxProcFactory(ctx, name, args...)
	} else {
		cmd := newExecCmd(ctx, name, args...)
		cmd.SetStdout(os.Stdout)
		cmd.SetStderr(os.Stderr)
		cmd.SetGracefulCancel(3 * time.Second)
		proc = cmd
	}

	if name == cmdNetmuxd {
		if configurable, ok := proc.(muxProcessEnv); ok {
			configurable.SetEnv(withEnvOverride(os.Environ(), "RUST_LOG", "netmuxd="+app.runtimeConfig.NetmuxdLogLevel))
		}
	}
	return proc
}

func withEnvOverride(env []string, key, value string) []string {
	prefix := key + "="
	overridden := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			overridden = append(overridden, item)
		}
	}
	return append(overridden, prefix+value)
}

// healthyRunThreshold 进程运行超过此时长视为「健康」，崩溃后重启用基础退避，不累积。
const healthyRunThreshold = 30 * time.Second

// sleepOrDone 等待 d，或在 ctx 取消时提前返回。返回 true 表示睡满，false 表示被取消。
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// superviseMux 在单个 goroutine 中监督 name+args 进程：意外退出则指数退避重启，
// 直到 ctx 被取消才返回。onRestart（非 nil）在每次「重启」（非首次启动）成功后异步触发。
//
// 关键：整个生命周期只有这一个 goroutine、一个 ctx。重启是循环内原地重新拉起，
// 不再调用会另起看门狗的函数 —— 杜绝旧实现「重启后泄漏 + 停不掉 + 抢 Wait」的 bug。
func (app *application) superviseMux(ctx context.Context, label, name string, args []string, onRestart func()) {
	delay := app.backoffStart()
	first := true

	for {
		proc := app.launchMux(ctx, name, args...)
		if err := proc.Start(); err != nil {
			app.addErrorLog("SYSTEM", fmt.Sprintf("%s 启动失败: %v", label, err))
			if !sleepOrDone(ctx, delay) {
				return
			}
			delay = nextWatchdogDelay(delay)
			continue
		}

		if first {
			app.addInfoLog("SYSTEM", label+" 守护进程已启动")
			first = false
		} else {
			app.addInfoLog("SYSTEM", label+" 已重启")
			if onRestart != nil {
				go onRestart()
			}
		}

		startedAt := time.Now()
		_ = proc.Wait()

		// 区分主动停止与意外退出
		select {
		case <-ctx.Done():
			app.addInfoLog("SYSTEM", label+" 守护进程已停止")
			return
		default:
		}

		// 健康运行过 → 重置退避，避免一次偶发崩溃后用上一次累积的长延迟
		if time.Since(startedAt) >= healthyRunThreshold {
			delay = app.backoffStart()
		}

		app.addWarnLog("SYSTEM", fmt.Sprintf("%s 意外退出，%v 后重启", label, delay))
		if !sleepOrDone(ctx, delay) {
			return
		}
		delay = nextWatchdogDelay(delay)
	}
}

// ---- netmuxd 进程生命周期（基于 superviseMux）----

// StartNetmuxd 启动 netmuxd 守护进程（单 goroutine 监督，重启后重放 add_device）。
func (app *application) StartNetmuxd() error {
	if app.usesUSBMuxd2WiFi() {
		app.addInfoLog("SYSTEM", "Wi-Fi 诊断后端为 usbmuxd2，跳过 netmuxd 启动")
		return nil
	}
	app.netMuxLifecycleMu.Lock()
	defer app.netMuxLifecycleMu.Unlock()
	return app.startNetmuxdLocked()
}

func (app *application) startNetmuxdLocked() error {
	if app.netMuxState == muxRunning || app.netMuxState == muxStarting {
		return nil
	}
	if app.netMuxState == muxStopping {
		return fmt.Errorf("netmuxd 正在停止")
	}
	app.netMuxState = muxStarting
	ctx, cancel := context.WithCancel(app.rootCtx)
	done := make(chan struct{})
	app.netMuxGeneration++
	generation := app.netMuxGeneration
	app.netmuxdCancel = cancel
	app.netmuxdDone = done
	app.netMuxState = muxRunning

	go func() {
		defer func() {
			close(done)
			app.netMuxLifecycleMu.Lock()
			if app.netMuxGeneration == generation && app.netmuxdDone == done {
				app.netmuxdCancel = nil
				app.netmuxdDone = nil
				app.netMuxState = muxStopped
			}
			app.netMuxLifecycleMu.Unlock()
		}()
		app.superviseMux(ctx, "netmuxd", cmdNetmuxd,
			[]string{"--disable-unix", "--disable-usb", "--host", "127.0.0.1"}, app.replayAddDevice)
	}()

	// 生产环境等待端口就绪（不持锁）；测试注入工厂时跳过
	if app.muxProcFactory == nil {
		if !waitForSocketReady(netmuxdAddr, 10*time.Second) {
			app.addWarnLog("SYSTEM", "netmuxd 端口未在 10s 内就绪，看门狗将持续重试")
		}
	}
	return nil
}

// StopNetmuxd 停止 netmuxd 守护进程（cancel + 等 supervisor 退出，全程不持锁阻塞）。
func (app *application) StopNetmuxd() {
	app.netMuxLifecycleMu.Lock()
	defer app.netMuxLifecycleMu.Unlock()
	app.stopNetmuxdLocked()
}

func (app *application) stopNetmuxdLocked() {
	if app.netMuxState == muxStopped {
		return
	}
	app.netMuxState = muxStopping
	cancel := app.netmuxdCancel
	done := app.netmuxdDone
	generation := app.netMuxGeneration

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			app.addWarnLog("SYSTEM", "等待 netmuxd 退出超时")
			return
		}
	}
	if app.netMuxGeneration == generation {
		app.netmuxdCancel = nil
		app.netmuxdDone = nil
		app.netMuxState = muxStopped
	}
}

// RestartNetmuxd 重启 netmuxd 守护进程并重放 add_device（无固定 sleep）。
func (app *application) RestartNetmuxd() {
	if app.usesUSBMuxd2WiFi() {
		app.addInfoLog("SYSTEM", "Wi-Fi 诊断后端为 usbmuxd2，跳过 netmuxd 重启")
		return
	}
	app.netMuxLifecycleMu.Lock()
	defer app.netMuxLifecycleMu.Unlock()
	app.addInfoLog("SYSTEM", "开始重启 netmuxd 守护进程...")
	app.stopNetmuxdLocked()
	if err := app.startNetmuxdLocked(); err != nil {
		app.addErrorLog("SYSTEM", fmt.Sprintf("重启 netmuxd 失败: %v", err))
		return
	}
	go app.replayAddDevice()
	app.addInfoLog("SYSTEM", "netmuxd 守护进程重启完成")
}

// ---- 就绪探测：轮询等待 socket 可连 ----

// waitForSocketReady 轮询等待 TCP 端口可连（最多等 timeout）。
func waitForSocketReady(addr string, timeout time.Duration) bool {
	return waitForDialReady("tcp", addr, timeout)
}

// waitForUnixSocketReady 轮询等待 unix socket 可连（最多等 timeout）。
func waitForUnixSocketReady(path string, timeout time.Duration) bool {
	return waitForDialReady("unix", path, timeout)
}

func waitForDialReady(network, addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout(network, addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// StartUSBMuxD 启动 usbmuxd2 守护进程（单 goroutine 监督，意外退出自动重启）。
// 不再用 pkill / 固定 time.Sleep / 持锁 sleep —— 清理只删陈旧锁文件，就绪靠 socket 探测。
func (app *application) StartUSBMuxD() error {
	app.usbMuxLifecycleMu.Lock()
	defer app.usbMuxLifecycleMu.Unlock()
	return app.startUSBMuxDLocked()
}

func (app *application) startUSBMuxDLocked() error {
	if app.usbMuxState == muxRunning || app.usbMuxState == muxStarting {
		return nil
	}
	if app.usbMuxState == muxStopping {
		return fmt.Errorf("usbmuxd 正在停止")
	}
	app.usbMuxState = muxStarting

	// 清理陈旧锁/socket 文件（无 pkill、无固定 sleep）；此时旧进程已停止
	app.prepareUSBMuxD()

	ctx, cancel := context.WithCancel(app.rootCtx)
	done := make(chan struct{})
	app.usbMuxGeneration++
	generation := app.usbMuxGeneration
	app.usbmuxdCancel = cancel
	app.usbmuxdDone = done
	app.usbMuxState = muxRunning

	go func() {
		defer func() {
			close(done)
			app.usbMuxLifecycleMu.Lock()
			if app.usbMuxGeneration == generation && app.usbmuxdDone == done {
				app.usbmuxdCancel = nil
				app.usbmuxdDone = nil
				app.usbMuxState = muxStopped
			}
			app.usbMuxLifecycleMu.Unlock()
		}()
		app.superviseMux(ctx, "usbmuxd", cmdUSBMuxd, []string{"-v", "-z"}, nil)
	}()

	// 生产环境等待 unix socket 就绪（不持锁）；测试注入工厂时跳过
	if app.muxProcFactory == nil {
		if !waitForUnixSocketReady("/var/run/usbmuxd", 10*time.Second) {
			app.addWarnLog("SYSTEM", "usbmuxd socket 未在 10s 内就绪，看门狗将持续重试")
		}
	}
	return nil
}

// prepareUSBMuxD 创建必要目录并清理陈旧锁/socket 文件（无 pkill、无 exec、无 sleep）。
// 仅在 StartUSBMuxD 中、确保旧 usbmuxd 已停止后调用。
func (app *application) prepareUSBMuxD() {
	os.MkdirAll(dirRun, 0755)
	os.MkdirAll(dirLockdown, 0755)
	for _, f := range []string{
		"/var/run/usbmuxd.pid",
		"/var/run/usbmuxd",
		"/var/run/usbmuxd.sock",
		"/tmp/usbmuxd.sock",
	} {
		if err := os.Remove(f); err == nil {
			app.addDebugLog("SYSTEM", fmt.Sprintf("已清理陈旧文件: %s", f))
		}
	}
}

// StopUSBMuxD 停止 usbmuxd2 守护进程（cancel + 等 supervisor 退出，全程不持锁阻塞）。
func (app *application) StopUSBMuxD() {
	app.usbMuxLifecycleMu.Lock()
	defer app.usbMuxLifecycleMu.Unlock()
	app.stopUSBMuxDLocked()
}

func (app *application) stopUSBMuxDLocked() {
	if app.usbMuxState == muxStopped {
		return
	}
	app.usbMuxState = muxStopping
	cancel := app.usbmuxdCancel
	done := app.usbmuxdDone
	generation := app.usbMuxGeneration

	if cancel != nil {
		app.addInfoLog("SYSTEM", "正在停止 usbmuxd 守护进程...")
		cancel() // SetGracefulCancel：先 SIGINT，WaitDelay 后强制 kill
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(6 * time.Second): // 略大于优雅退出 grace(3s)
			app.addWarnLog("SYSTEM", "等待 usbmuxd 退出超时")
			return
		}
	}
	if app.usbMuxGeneration == generation {
		app.usbmuxdCancel = nil
		app.usbmuxdDone = nil
		app.usbMuxState = muxStopped
	}
	app.addInfoLog("SYSTEM", "usbmuxd 守护进程已停止")
}

// RestartUSBMuxD 重启 usbmuxd 守护进程（无固定 sleep；停止与就绪均为事件驱动）。
func (app *application) RestartUSBMuxD() error {
	app.usbMuxLifecycleMu.Lock()
	defer app.usbMuxLifecycleMu.Unlock()
	app.addInfoLog("SYSTEM", "开始重启 usbmuxd 守护进程...")
	app.stopUSBMuxDLocked()
	if err := app.startUSBMuxDLocked(); err != nil {
		return fmt.Errorf("重启 usbmuxd 失败: %v", err)
	}
	app.addInfoLog("SYSTEM", "usbmuxd 守护进程重启完成")
	return nil
}
