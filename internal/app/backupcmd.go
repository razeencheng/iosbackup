package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// checkBackupDir 校验一个备份保存目录是否可用：落在映射到主机的挂载点上、能创建/已存在、可写、
// 且未被文件占用。返回 (ok, 中文原因)。不限制在 /backups 内，允许任意已挂载到主机的绝对路径。
func checkBackupDir(path string) (bool, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, "请填写保存位置"
	}
	if !filepath.IsAbs(path) {
		return false, "请使用绝对路径（以 / 开头）"
	}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return false, "该路径已被一个文件占用，请换一个文件夹"
	}
	// 本程序跑在容器里：只有映射到主机（落在 bind mount 上）的目录才会持久化。写进容器自身的
	// overlay 层或 tmpfs（如 /run、/tmp）的备份会在容器重建后丢失，所以先拦掉这种「能写但留不住」的路径。
	if persisted, knowable := pathPersistsToHost(path); knowable && !persisted {
		return false, "这个目录没有映射到主机，备份会写进容器内部、容器重建后丢失；请改用已挂载到主机的目录（例如 /backups 下的路径）"
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, "无法创建该文件夹：" + err.Error()
	}
	tmp := filepath.Join(path, ".iosbk_write_test")
	if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
		return false, "文件夹不可写：" + err.Error()
	}
	_ = os.Remove(tmp)
	return true, ""
}

// ephemeralFS 是「不会持久化到主机」的文件系统类型集合：容器自身的联合文件系统（overlay/aufs）
// 与各类虚拟/内存文件系统。bind mount 进来的目录在容器里看到的是宿主真实 fs（btrfs/ext4/xfs/zfs/
// nfs/fuse… 都不在此集合），因此用「反向白名单」最稳：不在此集合的都按已持久化处理。
var ephemeralFS = map[string]bool{
	"overlay": true, "aufs": true, "tmpfs": true, "devtmpfs": true, "ramfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "mqueue": true,
	"devpts": true, "shm": true, "securityfs": true, "debugfs": true, "tracefs": true,
	"bpf": true, "configfs": true, "fusectl": true, "pstore": true, "hugetlbfs": true,
	"binfmt_misc": true, "nsfs": true,
}

// pathPersistsToHost 判断 path 是否落在「会持久化到主机」的挂载点上（即 docker -v 的 bind mount）。
// 解析 /proc/self/mountinfo，取作为 path 最长前缀的挂载点，看其文件系统类型是否为临时类。
// knowable=false 表示无法判断（非 Linux 本地开发、读不到 mountinfo 等），此时调用方不应据此判失败。
func pathPersistsToHost(path string) (persisted, knowable bool) {
	// 目标目录可能尚未创建，用最近的「已存在」祖先来匹配挂载点；并解析软链接避免错配。
	probe := path
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	if resolved, err := filepath.EvalSymlinks(probe); err == nil {
		probe = resolved
	}

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, false // 读不到（如 macOS 本地开发）→ 无法判断
	}
	fstype, ok := enclosingMountFS(data, probe)
	if !ok {
		return false, false // 没匹配到挂载点 → 无法判断
	}
	return !ephemeralFS[fstype], true
}

// enclosingMountFS 解析 mountinfo，返回作为 probe 最长前缀的挂载点的文件系统类型。ok=false 表示没匹配到。
func enclosingMountFS(mountinfo []byte, probe string) (fstype string, ok bool) {
	bestLen := -1
	for _, line := range strings.Split(string(mountinfo), "\n") {
		// mountinfo: ID PID MAJ:MIN ROOT MOUNTPOINT OPTS [可选字段...] - FSTYPE SOURCE SUPEROPTS
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mnt := unescapeMountField(fields[4])
		fs := ""
		for i := 5; i < len(fields); i++ {
			if fields[i] == "-" && i+1 < len(fields) {
				fs = fields[i+1]
				break
			}
		}
		if fs == "" || !pathHasMountPrefix(probe, mnt) {
			continue
		}
		if len(mnt) > bestLen {
			bestLen = len(mnt)
			fstype = fs
		}
	}
	return fstype, bestLen >= 0
}

// pathHasMountPrefix 判断挂载点 mnt 是否为 path 的祖先（含相等）。"/" 是任何绝对路径的祖先。
func pathHasMountPrefix(path, mnt string) bool {
	if mnt == "/" {
		return true
	}
	return path == mnt || strings.HasPrefix(path, mnt+"/")
}

// unescapeMountField 还原 mountinfo 字段里的八进制转义（空格\040、Tab\011、换行\012、反斜杠\134）。
func unescapeMountField(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// DeleteDeviceBackup 删除该设备在本机的整份备份目录（不可恢复）。
// 安全：拒绝非法 UDID；备份/检查进行中拒绝；只删 <备份目录>/<udid>；删后重置 LastBackup。
func (app *application) DeleteDeviceBackup(udid string) error {
	if udid == "" || strings.ContainsAny(udid, `/\`) || strings.Contains(udid, "..") {
		return fmt.Errorf("非法设备标识，已中止")
	}
	path, release, err := app.acquireLocalBackupOp(udid)
	if err != nil {
		return err
	}
	defer release()
	if filepath.Base(path) != udid { // 双保险：路径必须以 udid 结尾，避免误删上级目录
		return fmt.Errorf("备份路径异常，已中止")
	}
	if fi, statErr := os.Stat(path); statErr != nil || !fi.IsDir() {
		return fmt.Errorf("没有找到该设备的本地备份")
	}
	if removeErr := os.RemoveAll(path); removeErr != nil {
		return fmt.Errorf("删除失败: %w", removeErr)
	}
	app.mu.Lock()
	if d, ok := app.devices[udid]; ok {
		d.LastBackup = time.Time{}
	}
	if c, ok := app.configs[udid]; ok {
		c.LastBackup = time.Time{}
	}
	app.mu.Unlock()
	app.saveConfigs()
	app.broadcastStatus()
	return nil
}

// backupGateReason 返回手动备份的即时拦截原因码（""=可备份）。供 handleBackup 同步预检，
// 给用户即时反馈（PerformBackup 内部仍会再校验作为防御）。第二、三个返回值为当前电量/最低电量。
func (app *application) backupGateReason(udid string) (code string, battery, minBattery int) {
	app.mu.RLock()
	defer app.mu.RUnlock()
	if app.connectionAdmissionUnsafe(app.devices[udid]) != nil {
		return "connection_unavailable", 0, 0
	}
	if err := app.deviceRemovalBlockedUnsafe(udid); errors.Is(err, errDeviceRemoved) {
		return "removed", 0, 0
	} else if errors.Is(err, errDeviceRemovalPending) {
		return "removal_pending", 0, 0
	}
	device, ok := app.devices[udid]
	if !ok {
		return "missing", 0, 0
	}
	if !device.IsOnline {
		return "offline", 0, 0
	}
	if app.backupInProgress[udid] {
		return "busy", 0, 0
	}
	config, ok := app.configs[udid]
	if !ok {
		return "", device.BatteryLevel, 0
	}
	if config.OnlyWhenCharging && !device.IsCharging {
		return "not_charging", device.BatteryLevel, config.MinBatteryLevel
	}
	if device.BatteryLevel < config.MinBatteryLevel {
		return "low_battery", device.BatteryLevel, config.MinBatteryLevel
	}
	return "", device.BatteryLevel, config.MinBatteryLevel
}

// deviceBackupPath 返回该设备备份在磁盘上的目录 <备份目录>/<udid>。
func (app *application) deviceBackupPath(udid string) string {
	app.mu.RLock()
	dir := dirBackups
	if cfg, ok := app.configs[udid]; ok && cfg.BackupDirectory != "" {
		dir = cfg.BackupDirectory
	}
	app.mu.RUnlock()
	return filepath.Join(dir, udid)
}

// hasLocalBackup 判断该设备在磁盘上是否已有备份：检查备份目录里是否有 idevicebackup2 生成的
// 标记文件（Manifest/Status/Info.plist）。没有备份时 idevicebackup2 list 会以 exit status 51 失败，
// 提前用本检查就能给出「还没有备份」的友好提示，而不是把原始错误抛给用户。
func (app *application) hasLocalBackup(udid string) bool {
	base := app.deviceBackupPath(udid)
	for _, name := range []string{"Manifest.plist", "Status.plist", "Info.plist"} {
		if fi, err := os.Stat(filepath.Join(base, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// dirSizeBytes 递归累加目录下所有普通文件的大小（路径不存在/出错时返回已累加值，不报错）。
func dirSizeBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的条目
		}
		if d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// humanSize 把字节数格式化为人类可读（B/KB/MB/GB/...，保留 1 位小数）。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// idevicebackup2 的 info/list 会把「下载 manifest」的进度条 + ANSI 转义混进 stdout（真机观测）。
// 下面两个用于在解析/返回前清洗噪声。
var (
	ansiRE        = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	progressBarRE = regexp.MustCompile(`\[[#=.>\s]*\]`)
)

// cleanBackupOutput 去掉 ANSI 转义、处理 \r 覆盖、丢弃进度/状态行，返回真实数据文本。
func cleanBackupOutput(out []byte) string {
	s := ansiRE.ReplaceAllString(string(out), "")
	var lines []string
	for _, raw := range strings.Split(s, "\n") {
		// 进度条用 \r 原地覆盖：只取最后一个 \r 之后的内容
		if i := strings.LastIndex(raw, "\r"); i >= 0 {
			raw = raw[i+1:]
		}
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" || isProgressLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// isProgressLine 判断是否为传输进度/状态行（应丢弃）。真实 CSV/info 数据行不命中这些模式。
func isProgressLine(line string) bool {
	t := strings.TrimSpace(line)
	if progressBarRE.MatchString(t) { // [####] / [....] / [==>  ] / []
		return true
	}
	for _, p := range []string{"Bytes / ", "KB / ", "MB / ", "GB / ", "TB / "} {
		if strings.Contains(t, p) {
			return true
		}
	}
	// 文件传输状态行 "Sending <path>" / "Receiving <path>"（无逗号，区别于 CSV 记录）
	if (strings.HasPrefix(t, "Sending") || strings.HasPrefix(t, "Receiving")) && !strings.Contains(t, ",") {
		return true
	}
	// 纯状态标签
	switch t {
	case "Backup", "Status", "Sent", "Received":
		return true
	}
	return false
}

// BackupListEntry 备份文件列表的一行（CSV→JSON，字段数可变）。
type backupListEntry struct {
	Fields []string `json:"fields"`
}

// backupDeviceContext 在锁内取出执行 idevicebackup2 子命令所需的设备副本与备份目录。
// 会拒绝「正在备份中」「不存在」「离线」的设备（尊重 backupInProgress）。
func (app *application) backupDeviceContext(udid string) (*device, string, error) {
	app.mu.RLock()
	defer app.mu.RUnlock()

	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		return nil, "", err
	}
	if app.backupInProgress[udid] {
		return nil, "", fmt.Errorf("设备 %s 正在备份中，请稍后再试", udid)
	}
	device, ok := app.devices[udid]
	if !ok {
		return nil, "", fmt.Errorf("设备 %s 不存在", udid)
	}
	if !device.IsOnline {
		return nil, "", fmt.Errorf("设备 %s 当前离线", udid)
	}
	dir := dirBackups
	if cfg, ok := app.configs[udid]; ok && cfg.BackupDirectory != "" {
		dir = cfg.BackupDirectory
	}
	// 返回设备副本，避免锁外被并发改写（runIdeviceCmd 只读 UDID/Connection）
	devCopy := *device
	return &devCopy, dir, nil
}

// backupPasswordEnv 若该设备存了备份密码，返回 BACKUP_PASSWORD env（读加密备份的
// info/list 需要它解密 manifest，否则报 ErrorCode 207）。无可用密码返回 nil。密码经 env
// 注入、绝不进 argv/日志。
func (app *application) backupPasswordEnv(udid string) []string {
	if app.secretStore == nil || !app.secretStore.Available() {
		return nil
	}
	pw, err := app.secretStore.GetBackupPassword(udid)
	if err != nil || pw == "" {
		return nil
	}
	return []string{"BACKUP_PASSWORD=" + pw}
}

// isEncryptedBackupError 判断 idevicebackup2 输出是否为"加密备份缺/错密码"。
func isEncryptedBackupError(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "Invalid password") ||
		strings.Contains(s, "MBErrorDomain/207") ||
		strings.Contains(s, "encrypted backup")
}

// BackupInfo 读取设备备份的信息（只读）。idevicebackup2 -u <udid> info <dir>
func (app *application) BackupInfo(ctx context.Context, udid string) ([]byte, error) {
	device, dir, err := app.backupDeviceContext(udid)
	if err != nil {
		return nil, err
	}
	release, err := app.beginConnectionTask(device)
	if err != nil {
		return nil, err
	}
	defer release()
	// 加密备份的 info 需要 BACKUP_PASSWORD 才能解密 manifest
	const maxBackupInfoBytes = 1 << 20
	out := newHeadBuffer(maxBackupInfoBytes)
	errTail := newTailBuffer(maxCommandErrorBytes)
	err = app.runIdeviceCmdStreamEnv(ctx, cmdKindMedium, device, app.backupPasswordEnv(udid), cmdIdevicebackup2, out, errTail, "info", dir)
	if err != nil {
		return out.Bytes(), fmt.Errorf("获取备份信息失败: %w（%s）", err, strings.TrimSpace(errTail.String()))
	}
	// 清洗掉下载 manifest 的进度条/ANSI 噪声，只留真实信息
	cleaned := cleanBackupOutput(out.Bytes())
	if out.Truncated() {
		cleaned += "\n[output truncated]"
	}
	return []byte(cleaned), nil
}

// BackupList 列出设备备份内的文件（只读，CSV→JSON）。idevicebackup2 -u <udid> list <dir>
func (app *application) BackupList(ctx context.Context, udid string) ([]backupListEntry, error) {
	entries, _, _, err := app.BackupListLimited(ctx, udid, 5000)
	return entries, err
}

func (app *application) BackupListLimited(ctx context.Context, udid string, limit int) ([]backupListEntry, int, bool, error) {
	if limit < 0 {
		limit = 0
	}
	entries := make([]backupListEntry, 0, min(limit, 256))
	total, err := app.streamBackupList(ctx, udid, func(fields []string) error {
		if len(entries) < limit {
			entries = append(entries, backupListEntry{Fields: append([]string(nil), fields...)})
		}
		return nil
	})
	return entries, total, total > limit, err
}

func (app *application) streamBackupList(ctx context.Context, udid string, onRow func([]string) error) (int, error) {
	device, dir, err := app.backupDeviceContext(udid)
	if err != nil {
		return 0, err
	}
	release, err := app.beginConnectionTask(device)
	if err != nil {
		return 0, err
	}
	defer release()
	// 加密备份的 list 需要 BACKUP_PASSWORD 才能解密 manifest
	pwEnv := app.backupPasswordEnv(udid)
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdoutR, stdoutW := io.Pipe()
	stderr := newTailBuffer(maxCommandErrorBytes)
	result := make(chan error, 1)
	go func() {
		runErr := app.runIdeviceCmdStreamEnv(cmdCtx, cmdKindMedium, device, pwEnv, cmdIdevicebackup2, stdoutW, stderr, "list", dir)
		_ = stdoutW.Close()
		result <- runErr
	}()

	total := 0
	parseErr := forEachBackupListRow(stdoutR, func(fields []string) error {
		total++
		return onRow(fields)
	})
	if parseErr != nil {
		cancel()
		_ = stdoutR.CloseWithError(parseErr)
	}
	cmdErr := <-result
	if parseErr != nil {
		return total, parseErr
	}
	if cmdErr != nil {
		errOutput := stderr.Bytes()
		if isEncryptedBackupError(errOutput) {
			if len(pwEnv) == 0 {
				return total, fmt.Errorf("备份已加密，但本服务没有保存该设备的备份密码（多半是在设备/Finder 上开启的加密），无法列出文件清单；占用空间仍可查看")
			}
			return total, fmt.Errorf("备份已加密，但保存的备份密码不正确，无法列出文件清单；请在「加密」页用正确密码重新开启")
		}
		return total, fmt.Errorf("列出备份失败: %w（%s）", cmdErr, strings.TrimSpace(stderr.String()))
	}
	return total, nil
}

func forEachBackupListRow(reader io.Reader, onRow func([]string) error) error {
	filteredR, filteredW := io.Pipe()
	filterResult := make(chan error, 1)
	go func() {
		err := consumeBoundedLines(reader, func(line string, _ bool) error {
			line = strings.TrimSpace(line)
			if strings.ContainsRune(line, '\x1b') {
				line = ansiRE.ReplaceAllString(line, "")
			}
			if line == "" || strings.HasSuffix(line, backupLogTruncatedMarker) ||
				(!strings.Contains(line, ",") && isProgressLine(line)) {
				return nil
			}
			if _, writeErr := io.WriteString(filteredW, line); writeErr != nil {
				return writeErr
			}
			_, writeErr := io.WriteString(filteredW, "\n")
			return writeErr
		})
		_ = filteredW.CloseWithError(err)
		filterResult <- err
	}()

	r := csv.NewReader(filteredR)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.ReuseRecord = true
	for {
		fields, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = filteredR.CloseWithError(err)
			<-filterResult
			return err
		}
		if len(fields) > 0 {
			if err := onRow(fields); err != nil {
				_ = filteredR.CloseWithError(err)
				<-filterResult
				return err
			}
		}
	}
	return <-filterResult
}

// parseBackupList 把 idevicebackup2 list 的 CSV 输出解析为行集合（字段数可变，容错）。
// 非 CSV 输出回退为按行拆分，保证不丢信息。
func parseBackupList(out []byte) []backupListEntry {
	entries := []backupListEntry{}
	_ = forEachBackupListRow(bytes.NewReader(out), func(fields []string) error {
		entries = append(entries, backupListEntry{Fields: append([]string(nil), fields...)})
		return nil
	})
	return entries
}
