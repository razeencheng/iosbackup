package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"iosbackup/internal/buildinfo"
	"iosbackup/internal/i18n"
)

// P4 长操作的有界超时：防止 idevicebackup2 在设备侧等待（如改密/关加密需在设备上输锁屏密码
// 确认）时无限挂起、长期占用备份槽锁死设备。超时即取消、释放槽。
const (
	encryptionOpTimeout = 3 * time.Minute // encryption on/off、changepw
	restoreOpTimeout    = 2 * time.Hour   // restore 是真长操作（整机恢复）
)

func (app *application) requireExperimentalOperations(w http.ResponseWriter) bool {
	if app.enableExperimentalOperations {
		return true
	}
	writeError(w, http.StatusForbidden, i18n.ExperimentalDisabled)
	return false
}

func (app *application) rejectRemovedDeviceOperation(w http.ResponseWriter, udid string) bool {
	if err := app.deviceRemovalBlock(udid); err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return true
	}
	return false
}

// 设置路由
func (app *application) setupRoutes() http.Handler {
	mux := http.NewServeMux()

	// 加载嵌入的HTML模板
	tmpl, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		log.Fatal("event=ui.template_read_failed page=console error=", err)
	}
	app.htmlTemplate = template.Must(template.New("index.html").Funcs(template.FuncMap{
		"contains": strings.Contains,
		"t":        uiText,
	}).Parse(string(tmpl)))
	loginMarkup, err := templateFS.ReadFile("templates/login.html")
	if err != nil {
		log.Fatal("event=ui.template_read_failed page=login error=", err)
	}
	app.loginTemplate = template.Must(template.New("login.html").Funcs(template.FuncMap{"t": uiText}).Parse(string(loginMarkup)))
	onboardingMarkup, err := templateFS.ReadFile("templates/onboarding.html")
	if err != nil {
		log.Fatal("event=ui.template_read_failed page=onboarding error=", err)
	}
	app.onboardingTemplate = template.Must(template.New("onboarding.html").Funcs(template.FuncMap{"t": uiText}).Parse(string(onboardingMarkup)))

	// 静态资源随二进制嵌入，容器与断网环境不依赖工作目录或 CDN。
	staticFS, err := fs.Sub(templateFS, "static")
	if err != nil {
		log.Fatal("event=ui.assets_read_failed error=", err)
	}
	// 主页
	mux.HandleFunc("/", app.handleHome)
	mux.HandleFunc("/onboarding", app.handleOnboarding)
	mux.HandleFunc("/logout", app.handleLogout)

	// 通知设置页面
	mux.HandleFunc("/notifications", app.handleNotifications)

	// API路由
	mux.HandleFunc("/api/version", app.handleVersion)
	mux.HandleFunc("/api/status", app.handleStatus)
	mux.HandleFunc("/api/events", app.handleEvents) // SSE 实时状态推送
	mux.HandleFunc("/api/refresh", app.handleRefresh)
	mux.HandleFunc("/api/restart-usbmuxd", app.handleRestartUSBMuxD)
	mux.HandleFunc("/api/backup/", app.handleBackup)
	mux.HandleFunc("/api/backup-stop/", app.handleBackupStop)
	mux.HandleFunc("/api/backup-resume/", app.handleBackupResume)
	mux.HandleFunc("/api/backup-hold-retry/", app.handleBackupHoldRetry)
	mux.HandleFunc("/api/save-config/", app.handleSaveConfig)
	mux.HandleFunc("/api/test-ip/", app.handleTestDeviceIP)
	mux.HandleFunc("/api/check-dir", app.handleCheckBackupDir)
	mux.HandleFunc("/api/delete-backup/", app.handleDeleteBackup)
	mux.HandleFunc("/api/encryption-status/", app.handleEncryptionStatus)
	mux.HandleFunc("/api/pair/", app.handlePairDevice)
	mux.HandleFunc("/api/removed-devices", app.handleRemovedDevices)
	mux.HandleFunc("/api/remove-device/", app.handleRemoveDevice)
	mux.HandleFunc("/api/restore-device/", app.handleRestoreDevice)

	// 备份查看类（P3，只读 + unback 长操作）
	mux.HandleFunc("/api/backup-info/", app.handleBackupInfo)
	mux.HandleFunc("/api/backup-list/", app.handleBackupList)
	mux.HandleFunc("/api/backup-unback/", app.handleBackupUnback)

	// 加密管理 + 恢复（P4，状态变更/破坏性，要求 CSRF 头）
	mux.HandleFunc("/api/encryption/", app.handleEncryption)
	mux.HandleFunc("/api/backup-changepw/", app.handleChangePassword)
	mux.HandleFunc("/api/restore/", app.handleRestore)

	// 通知相关API
	mux.HandleFunc("/api/notifications/config", app.handleNotificationConfig)
	mux.HandleFunc("/api/notifications/test", app.handleNotificationTest)
	mux.HandleFunc("/api/notifications/status", app.handleNotificationStatus)

	protected := bodyLimitMiddleware(mux)
	protected = app.csrfManager.Middleware(protected)
	protected = app.authManager.Middleware(protected)
	root := http.NewServeMux()
	root.HandleFunc("/static/api-messages.js", handleAPIMessages)
	root.HandleFunc("/static/ui-messages.js", handleUIMessages)
	root.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	root.Handle("/login", bodyLimitMiddleware(http.HandlerFunc(app.handleLogin)))
	root.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.Handle("/", protected)
	return securityHeadersMiddleware(localeMiddleware(root))
}

type loginPageData struct {
	Locale    string
	ErrorCode string
	CSRFToken string
	Next      string
	Error     string
	Insecure  bool
	Version   string
	Commit    string
	SourceURL string
}

func (app *application) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !app.authManager.Enabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	next := safeLoginNext(r.URL.Query().Get("next"))
	if r.Method == http.MethodGet {
		if app.authManager.authenticateRequest(r) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		app.renderLogin(w, r, http.StatusOK, next, "")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeError(w, http.StatusForbidden, i18n.CrossSiteRejected)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidForm)
		return
	}
	next = safeLoginNext(r.Form.Get("next"))
	if !constantTokenEqual(r.Form.Get("csrf_token"), app.csrfManager.Token()) {
		writeError(w, http.StatusForbidden, i18n.CSRFInvalid)
		return
	}
	password := r.Form.Get("password")
	if len(password) > 4096 || !app.authManager.AuthenticatePassword(password) {
		app.renderLogin(w, r, http.StatusUnauthorized, next, i18n.LoginInvalidCredentials)
		return
	}
	token, err := app.authManager.createSession(time.Now())
	if err != nil {
		log.Printf("event=auth.session_create_failed error=%v", err)
		writeError(w, http.StatusInternalServerError, i18n.LoginUnavailable)
		return
	}
	setAuthSessionCookie(w, r, token)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (app *application) renderLogin(w http.ResponseWriter, r *http.Request, status int, next, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	data := loginPageData{
		Locale:    i18n.ResolveRequest(r),
		ErrorCode: message,
		CSRFToken: app.csrfManager.Token(),
		Next:      safeLoginNext(next),
		Error:     localizedLoginError(r, message),
		Insecure:  !requestIsSecure(r),
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		SourceURL: buildinfo.SourceURL,
	}
	if err := app.loginTemplate.Execute(w, data); err != nil {
		log.Printf("event=ui.template_render_failed page=login error=%v", err)
	}
}

func (app *application) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(authSessionCookieName); err == nil {
		app.authManager.revokeSession(cookie.Value)
	}
	clearAuthSessionCookie(w, r)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func safeLoginNext(raw string) string {
	if raw == "" {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") || parsed.Path == "/login" {
		return "/"
	}
	return parsed.RequestURI()
}

func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func setAuthSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearAuthSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

// HTTP辅助函数
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, apiMessage(w, code, nil, true))
}

func writeSuccess(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusOK, apiMessage(w, code, nil, false))
}

func extractUDIDFromPath(path string, prefix string) string {
	if strings.HasPrefix(path, prefix) {
		return strings.TrimPrefix(path, prefix)
	}
	return ""
}

type removedDeviceDTO struct {
	UDID            string `json:"udid"`
	Name            string `json:"name"`
	DeviceType      string `json:"device_type"`
	RemovedAt       string `json:"removed_at"`
	LastBackup      string `json:"last_backup"`
	BackupPreserved bool   `json:"backup_preserved"`
}

func removedDeviceDisplayName(config *backupConfig) string {
	if name := strings.TrimSpace(config.Name); name != "" {
		return name
	}
	if len(config.UDID) > 8 {
		return config.UDID[:8] + "…"
	}
	return config.UDID
}

func removedDeviceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return toBeijingTime(value).Format(time.RFC3339)
}

func (app *application) removedDevices() []removedDeviceDTO {
	app.mu.RLock()
	devices := make([]removedDeviceDTO, 0)
	for _, config := range app.configs {
		if config == nil || config.RemovedAt == nil {
			continue
		}
		devices = append(devices, removedDeviceDTO{
			UDID:            config.UDID,
			Name:            removedDeviceDisplayName(config),
			DeviceType:      config.DeviceType,
			RemovedAt:       removedDeviceTime(*config.RemovedAt),
			LastBackup:      removedDeviceTime(config.LastBackup),
			BackupPreserved: true,
		})
	}
	app.mu.RUnlock()
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].RemovedAt == devices[j].RemovedAt {
			return devices[i].UDID < devices[j].UDID
		}
		return devices[i].RemovedAt > devices[j].RemovedAt
	})
	return devices
}

func writeDeviceRemovalError(w http.ResponseWriter, status int, code string) {
	key := map[string]string{"not_found": i18n.DeviceNotFound, "busy": i18n.Busy, "removal_pending": i18n.RemovalPending, "persistence_failed": i18n.DeviceStateSaveFailed, "csrf_required": i18n.CSRFRequired, "invalid_udid": i18n.InvalidUDID}[code]
	if key == "" {
		key = i18n.OperationFailed
	}
	payload := apiMessage(w, key, nil, true)
	payload["code"] = code
	payload["error_code"] = code
	writeJSON(w, status, payload)
}

func writeDeviceRemovalOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDeviceNotFound):
		writeDeviceRemovalError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, errDeviceBusy):
		writeDeviceRemovalError(w, http.StatusConflict, "busy")
	case errors.Is(err, errDeviceRemovalPending):
		writeDeviceRemovalError(w, http.StatusConflict, "removal_pending")
	default:
		payload := operationErrorPayload(w, err, i18n.DeviceStateSaveFailed)
		payload["code"] = "persistence_failed"
		payload["error_code"] = "persistence_failed"
		writeJSON(w, http.StatusInternalServerError, payload)
	}
}

func (app *application) handleRemovedDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	devices := app.removedDevices()
	writeJSON(w, http.StatusOK, map[string]interface{}{"count": len(devices), "devices": devices})
}

func (app *application) handleRemoveDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeDeviceRemovalError(w, http.StatusForbidden, "csrf_required")
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/remove-device/")
	if !udidPattern.MatchString(udid) {
		writeDeviceRemovalError(w, http.StatusBadRequest, "invalid_udid")
		return
	}
	removedAt, err := app.removeDevice(udid)
	if err != nil {
		writeDeviceRemovalOperationError(w, err)
		return
	}
	payload := apiMessage(w, i18n.DeviceRemoved, nil, false)
	payload["removed_at"] = removedDeviceTime(removedAt)
	writeJSON(w, http.StatusOK, payload)
}

func (app *application) handleRestoreDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeDeviceRemovalError(w, http.StatusForbidden, "csrf_required")
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/restore-device/")
	if !udidPattern.MatchString(udid) {
		writeDeviceRemovalError(w, http.StatusBadRequest, "invalid_udid")
		return
	}
	if err := app.restoreRemovedDevice(udid); err != nil {
		writeDeviceRemovalOperationError(w, err)
		return
	}
	go func() {
		if err := app.RefreshDevices(); err != nil {
			app.addWarnLog(udid, fmt.Sprintf("event=device.refresh_after_reinstatement_failed error=%v", err))
		}
	}()
	writeSuccess(w, i18n.DeviceRestored)
}

// HTTP处理函数
func (app *application) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, i18n.NotFound)
		return
	}

	// 不在请求路径上同步探测设备：每台 Wi-Fi 设备需 4 次 ideviceinfo 往返 lockdownd，
	// 同步执行曾让首页 ~3s 才返回。设备状态由后台 statusPoller 持续刷新
	// （presence 每 4s / detail 每 30s），并经 SSE 首帧即时对齐 + 实时推送，
	// 故这里直接用内存缓存渲染（秒开）。需要强制刷新时走「刷新设备状态」(/api/refresh)。
	app.mu.RLock()
	devices := make([]*device, 0, len(app.devices))
	configs := make(map[string]*backupConfig)
	backupStatuses := make(map[string]bool)
	hasBackupInProgress := len(app.backupInProgress) > 0

	for udid, device := range app.devices {
		// 创建设备副本并确保时间是北京时间
		deviceCopy := *device
		deviceCopy.LastBackup = toBeijingTime(device.LastBackup)
		devices = append(devices, &deviceCopy)
		if config, exists := app.configs[udid]; exists {
			configs[udid] = cloneBackupConfig(config)
			app.overlayBackupHoldUnsafe(configs[udid])
		} else {
			// 模板始终需要一份配置（避免 nil 解引用）；用安全默认值兜底
			configs[udid] = &backupConfig{
				UDID:            udid,
				Name:            device.Name,
				StartTime:       "18:00",
				EndTime:         "06:00",
				BackupInterval:  24,
				MinBatteryLevel: 20,
				BackupDirectory: dirBackups,
			}
		}
		// 记录备份状态
		backupStatuses[udid] = app.backupInProgress[udid]
	}
	app.mu.RUnlock()

	// 按在线状态和设备名称排序，避免每次显示乱序
	sort.Slice(devices, func(i, j int) bool {
		// 首先按在线状态排序（在线设备优先）
		if devices[i].IsOnline != devices[j].IsOnline {
			return devices[i].IsOnline // true排在false前面
		}
		// 然后按设备名称排序
		if devices[i].Name == devices[j].Name {
			return devices[i].UDID < devices[j].UDID
		}
		return devices[i].Name < devices[j].Name
	})

	// 网络设备「自动发现的 IP」（经 netmuxd usbmux ListDevices）；netmuxd 不可达时为空
	discoveredIPs := app.discoveredNetworkIPs()

	data := struct {
		Locale                        string
		Devices                       []*device
		Configs                       map[string]*backupConfig
		BackupStatuses                map[string]bool
		HasBackupInProgress           bool
		EncryptionAvailable           bool
		ExperimentalOperationsEnabled bool
		DiscoveredIPs                 map[string]string
		InitialStatus                 statusSnapshot
		Version                       string
		Commit                        string
		SourceURL                     string
		CSRFToken                     string
		AuthEnabled                   bool
	}{
		Locale:                        i18n.ResolveRequest(r),
		InitialStatus:                 app.buildStatusSnapshot(),
		Devices:                       devices,
		Configs:                       configs,
		BackupStatuses:                backupStatuses,
		HasBackupInProgress:           hasBackupInProgress,
		EncryptionAvailable:           app.secretStore != nil && app.secretStore.Available(),
		ExperimentalOperationsEnabled: app.enableExperimentalOperations,
		DiscoveredIPs:                 discoveredIPs,
		Version:                       buildinfo.Version,
		Commit:                        buildinfo.Commit,
		SourceURL:                     buildinfo.SourceURL,
		CSRFToken:                     app.csrfManager.Token(),
		AuthEnabled:                   app.authManager.Enabled(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.htmlTemplate.Execute(w, data); err != nil {
		log.Printf("event=ui.template_render_failed page=console error=%v", err)
		writeError(w, http.StatusInternalServerError, i18n.PageRenderFailed)
		return
	}
}

func (app *application) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	data := struct {
		Locale    string
		CSRFToken string
		Version   string
		Commit    string
		SourceURL string
	}{
		Locale:    i18n.ResolveRequest(r),
		CSRFToken: app.csrfManager.Token(),
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		SourceURL: buildinfo.SourceURL,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := app.onboardingTemplate.Execute(w, data); err != nil {
		log.Printf("event=ui.template_render_failed page=onboarding error=%v", err)
		writeError(w, http.StatusInternalServerError, i18n.PageRenderFailed)
	}
}

func (app *application) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"version":     buildinfo.Version,
		"build_date":  buildinfo.BuildDate,
		"description": buildinfo.Description,
		"commit":      buildinfo.Commit,
		"source_url":  buildinfo.SourceURL,
		"license":     buildinfo.LicenseID,
	})
}

func (app *application) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	app.mu.RLock()
	connectionServices := app.connectionServicesUnsafe()
	canRefresh := app.connectionAdmissionUnsafe(nil) == nil
	backupCount := len(app.backupInProgress)
	backupDevices := make([]string, 0, backupCount)
	backupUDIDs := make([]string, 0, backupCount)
	for udid := range app.backupInProgress {
		backupUDIDs = append(backupUDIDs, udid)
		if device, exists := app.devices[udid]; exists {
			backupDevices = append(backupDevices, device.Name)
		} else {
			backupDevices = append(backupDevices, udid[:8]+"...")
		}
	}
	app.mu.RUnlock()

	// 加密能力（缺密钥时降级；UI 据此灰置加密/恢复相关控件）
	encAvailable := app.secretStore != nil && app.secretStore.Available()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connection_services":       connectionServices,
		"devices":                   app.buildStatusSnapshot().Devices,
		"backup_in_progress_count":  backupCount,
		"backup_devices":            backupDevices,
		"backup_udids":              backupUDIDs,
		"can_refresh":               backupCount == 0 && canRefresh,
		"encryption_available":      encAvailable,
		"encryption_key_configured": encAvailable,
	})
}

func (app *application) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	// 检查是否有设备在备份中
	app.mu.RLock()
	backupCount := len(app.backupInProgress)
	app.mu.RUnlock()

	if backupCount > 0 {
		// 有备份在进行中，使用轻量级刷新
		app.RefreshDevicesStatus()
		writeSuccess(w, i18n.RefreshLightweight)
	} else {
		// 没有备份在进行中，使用完整刷新
		if err := app.RefreshDevices(); err != nil {
			writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
			return
		}
		writeSuccess(w, i18n.RefreshComplete)
	}
}

// handleRestartUSBMuxD 重启「设备连接服务」—— usbmuxd2(USB) + netmuxd(Wi-Fi) 两个守护进程。
// 端点名保持 /api/restart-usbmuxd 兼容旧调用方，但行为已扩为重启两者。
func (app *application) handleRestartUSBMuxD(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	targets := app.connectionTargets()
	if err := app.reserveConnectionRecovery(targets, true); err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return
	}
	go func() { _ = app.executeConnectionRecovery(app.rootCtx, targets, true) }()

	writeSuccess(w, i18n.ConnectionRestartStarted)
}

func (app *application) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	udid := extractUDIDFromPath(r.URL.Path, "/api/backup/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}

	// 同步预检：被限制（离线/正忙/未充电/电量不足）时立即返回原因码，前端据此本地化提示，
	// 避免「点了立即备份却被静默跳过」。PerformBackup 内部仍会再校验作为防御。
	if code, batt, minB := app.backupGateReason(udid); code != "" {
		payload := apiMessage(w, "api."+code, map[string]any{"battery": batt, "min_battery": minB}, true)
		payload["error_code"], payload["battery"], payload["min_battery"] = code, batt, minB
		writeJSON(w, http.StatusConflict, payload)
		return
	}

	job, err := app.beginBackupJob(udid, "", "", false)
	if err != nil {
		payload := operationErrorPayload(w, err, i18n.OperationFailed)
		if translated, ok := err.(*i18n.Error); ok && translated.Code == i18n.BackupBusy {
			payload["error_code"] = "backup_busy"
		}
		writeJSON(w, http.StatusConflict, payload)
		return
	}
	app.broadcastStatus()
	go app.runBackupJob(job)
	payload := apiMessage(w, i18n.BackupStarted, nil, false)
	payload["backup_state"] = backupStateStarting
	payload["backup_job_id"] = job.id
	writeJSON(w, http.StatusAccepted, payload)
}

// handleSaveConfig 保存设备备份配置（改为 merge 模式）。
//
// merge 语义：
//   - 请求 JSON 中「未出现的字段」→ 保留旧值（旧前端不带 network_address 时不清空已存值）
//   - 请求 JSON 中「显式传入的字段」→ 覆盖（包括显式传空字符串）
//
// 实现：先解析为 map[string]json.RawMessage，再逐字段写入旧配置。
func (app *application) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	udid := extractUDIDFromPath(r.URL.Path, "/api/save-config/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}

	// 解析请求体为原始 JSON map，保留字段出现信息
	var rawFields map[string]json.RawMessage
	if err := decodeRequestJSON(r, &rawFields); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}

	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.RLock()
	if err := app.deviceRemovalBlockedUnsafe(udid); err != nil {
		app.mu.RUnlock()
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return
	}

	// 取出旧配置（或创建新配置）
	old, exists := app.configs[udid]
	candidate := *app.defaultBackupConfig(udid, "")
	if !exists {
		old = &candidate
	} else {
		candidate = *old
	}
	oldIP := old.NetworkAddress // merge 前捕获旧 IP，用于判断 netmuxd 是否需重置
	deviceName := ""
	if device, ok := app.devices[udid]; ok {
		deviceName = device.Name
	}
	app.mu.RUnlock()

	// merge：只更新请求中明确出现的字段
	if err := mergeBackupConfig(&candidate, rawFields); err != nil {
		writeOperationError(w, http.StatusBadRequest, err, i18n.ConfigInvalid)
		return
	}

	// UDID 始终以 URL 路径为准
	candidate.UDID = udid

	// 设备名称以运行时最新为准
	if deviceName != "" {
		candidate.Name = deviceName
	}
	app.applyBackupConfigDefaults(&candidate)
	app.mu.RLock()
	app.overlayBackupHoldUnsafe(&candidate)
	app.mu.RUnlock()
	if err := validateBackupConfig(candidate, []string{app.paths.BackupsRoot}); err != nil {
		writeOperationError(w, http.StatusBadRequest, err, i18n.ConfigInvalid)
		return
	}
	if err := app.configStore.Put(candidate); err != nil {
		writeOperationError(w, http.StatusInternalServerError, err, i18n.ConfigSaveFailed)
		return
	}
	app.mu.Lock()
	app.overlayBackupHoldUnsafe(&candidate)
	app.configs[udid] = &candidate
	app.mu.Unlock()

	// netmuxd 同步：IP 改址/清空 → 重启 netmuxd 清掉旧条目再重放当前 IP（netmuxd 不更新已存
	// UDID 的地址，否则旧 IP 残留）；仅新增/不变 → add_device 重放（幂等）。均异步，不阻塞响应。
	newIP := candidate.NetworkAddress
	switch netmuxdSyncDecision(oldIP, newIP) {
	case netmuxdReset:
		app.addInfoLog(udid, fmt.Sprintf("event=network.address_changed old_ip=%q new_ip=%q Restarting netmuxd", oldIP, newIP))
		app.queueNetworkServiceRestart()
	case netmuxdReplay:
		go app.replayAddDevice()
	}

	writeSuccess(w, i18n.ConfigSaved)
}

// handleCheckBackupDir 校验「保存位置」是否可用（可创建+可写，未被文件占用）。不限制目录范围。
func (app *application) handleCheckBackupDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeRequestJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	ok, err := checkBackupDir(body.Path)
	if ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	payload := operationErrorPayload(w, err, i18n.OperationFailed)
	payload["ok"], payload["reason"] = false, payload["error"]
	writeJSON(w, http.StatusOK, payload)
}

// handleDeleteBackup 删除该设备在本机的整份备份（破坏性）。需 CSRF + 设备名二次确认。
func (app *application) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	if !app.requireExperimentalOperations(w) {
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/delete-backup/")
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	var body struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decodeRequestJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	app.mu.RLock()
	dev, ok := app.devices[udid]
	name := ""
	if ok {
		name = dev.Name
	}
	app.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, i18n.DeviceNotFound)
		return
	}
	if strings.TrimSpace(body.ConfirmName) != name {
		writeError(w, http.StatusConflict, i18n.ConfirmNameMismatch)
		return
	}
	if err := app.DeleteDeviceBackup(udid); err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return
	}
	writeSuccess(w, i18n.BackupDeleted)
}

// handleEncryptionStatus 返回设备当前是否启用备份加密（WillEncrypt），供加密页显示当前状态。
func (app *application) handleEncryptionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/encryption-status/")
	app.mu.RLock()
	currentDevice, ok := app.devices[udid]
	var devCopy device
	online := false
	if ok {
		devCopy = *currentDevice
		online = currentDevice.IsOnline
	}
	app.mu.RUnlock()
	if !ok || !online {
		writeJSON(w, http.StatusOK, map[string]interface{}{"online": false})
		return
	}
	release, taskErr := app.beginConnectionTask(&devCopy)
	if taskErr != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"online": online, "unknown": true})
		return
	}
	defer release()
	out, err := app.runIdeviceCmd(r.Context(), cmdKindShort, &devCopy, cmdIdeviceInfo, "-q", "com.apple.mobile.backup", "-k", "WillEncrypt")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"online": true, "unknown": true})
		return
	}
	will := strings.TrimSpace(strings.ToLower(string(out))) == "true"
	writeJSON(w, http.StatusOK, map[string]interface{}{"online": true, "will_encrypt": will})
}

// handleTestDeviceIP 探测「设备 IP」是否可达（lockdown over Wi-Fi: 62078）。
// 等待 netmuxd 注册并确认设备上线后才返回成功，同时返回状态供页面立即更新。
func (app *application) handleTestDeviceIP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/test-ip/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	var body struct {
		IP string `json:"ip"`
	}
	if err := decodeRequestJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	ip := strings.TrimSpace(body.IP)
	if ip == "" {
		payload := apiMessage(w, i18n.IPRequired, nil, true)
		payload["ok"], payload["reason"] = false, payload["error"]
		writeJSON(w, http.StatusBadRequest, payload)
		return
	}
	addr, err := parseDeviceNetworkAddress(ip)
	if err != nil {
		payload := operationErrorPayload(w, err, i18n.IPInvalid)
		payload["ok"], payload["reason"] = false, payload["error"]
		writeJSON(w, http.StatusBadRequest, payload)
		return
	}
	ip = addr.String()
	if err := app.callAddDevice(udid, ip); err != nil {
		payload := operationErrorPayload(w, err, i18n.IPConnectionFailed)
		payload["ok"], payload["reason"], payload["ip"] = false, payload["error"], ip
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if app.refreshPresence() {
		app.refreshDetails()
	}
	snapshot := app.buildStatusSnapshot()
	connected := false
	for _, device := range snapshot.Devices {
		if device.UDID == udid {
			connected = device.Online
			break
		}
	}
	payload := map[string]any{"ok": connected, "reason": "", "ip": ip, "status": snapshot}
	if !connected {
		for key, value := range apiMessage(w, i18n.IPDeviceOffline, nil, true) {
			payload[key] = value
		}
		payload["reason"] = payload["error"]
	}
	app.broadcastStatus()
	writeJSON(w, http.StatusOK, payload)
}

// mergeBackupConfig 将 rawFields 中出现的字段合并到 dst。
// 未出现的字段保留 dst 的原值。
func mergeBackupConfig(dst *backupConfig, rawFields map[string]json.RawMessage) error {
	known := map[string]any{
		"name": &dst.Name, "start_time": &dst.StartTime, "end_time": &dst.EndTime,
		"backup_interval": &dst.BackupInterval, "min_battery_level": &dst.MinBatteryLevel,
		"only_when_charging": &dst.OnlyWhenCharging, "backup_directory": &dst.BackupDirectory,
		"auto_backup_enabled": &dst.AutoBackupEnabled, "network_address": &dst.NetworkAddress,
		"last_backup_connection": &dst.LastBackupConnection, "restore_enabled": &dst.RestoreEnabled,
	}
	for field, value := range rawFields {
		target, ok := known[field]
		if !ok {
			return i18n.NewError(i18n.ConfigUnknownField, map[string]any{"field": field})
		}
		if err := json.Unmarshal(value, target); err != nil {
			return &i18n.Error{Code: i18n.ConfigFieldInvalid, Params: map[string]any{"field": field}, Detail: err.Error(), Cause: err}
		}
	}
	return nil
}

func (app *application) handlePairDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	udid := extractUDIDFromPath(r.URL.Path, "/api/pair/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}

	// 检查设备是否存在
	app.mu.RLock()
	device, exists := app.devices[udid]
	device = cloneDevice(device)
	app.mu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, i18n.DeviceNotFound)
		return
	}

	release, err := app.beginConnectionTask(device)
	if err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return
	}
	app.setPairingState(udid, pairingStateChecking, "", "")
	go func() { defer release(); app.pairDeviceInTask(device) }()

	payload := apiMessage(w, i18n.PairingStarted, nil, false)
	payload["pairing_state"] = pairingStateChecking
	writeJSON(w, http.StatusAccepted, payload)
}

// handleNotificationConfig 处理通知配置
func (app *application) handleNotificationConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 获取通知配置
		config, err := app.notificationConfigStore.Public()
		if err != nil {
			writeOperationError(w, http.StatusInternalServerError, err, i18n.NotificationConfigLoadFailed)
			return
		}
		writeJSON(w, http.StatusOK, config)

	case http.MethodPost:
		// 保存通知配置
		var config notificationConfig
		if err := decodeRequestJSON(r, &config); err != nil {
			writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
			return
		}

		// 先在内存中合并现有秘密并验证所有已启用通知器；验证失败不得落盘。
		preview, err := app.notificationConfigStore.Preview(&config)
		if err != nil {
			writeOperationError(w, http.StatusBadRequest, err, i18n.NotificationConfigInvalid)
			return
		}
		notificationManager, err := newNotificationManagerFromConfig(preview, app.runtimeConfig.WebhookAllowCIDRs)
		if err != nil {
			writeOperationError(w, http.StatusBadRequest, err, i18n.NotificationConfigInvalid)
			return
		}

		// 保存配置
		if err := app.notificationConfigStore.Save(&config); err != nil {
			notificationManager.Close()
			writeOperationError(w, http.StatusInternalServerError, err, i18n.NotificationConfigSaveFailed)
			return
		}

		// 发布已验证、已落盘的通知管理器。
		oldManager := app.replaceNotificationManager(notificationManager)
		if oldManager != nil {
			oldManager.Close()
		}

		writeSuccess(w, i18n.NotificationConfigSaved)

	default:
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
	}
}

// handleNotificationTest 处理通知测试
func (app *application) handleNotificationTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	var testData struct {
		NotifierName string `json:"notifier_name"`
		MessageType  string `json:"message_type"`
	}

	if err := decodeRequestJSON(r, &testData); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}

	manager := app.notificationManagerSnapshot()
	if manager == nil {
		writeError(w, http.StatusServiceUnavailable, i18n.NotificationUnavailable)
		return
	}

	message := manager.NewTestMessage(notificationType(testData.MessageType))
	if message == nil {
		writeError(w, http.StatusBadRequest, i18n.NotificationTypeUnsupported)
		return
	}

	// 测试发送必须等待真实结果，不能把“已入队/零通知器”回报成已送达。
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result := manager.SendAndWait(ctx, message)
	switch {
	case result.Attempted == 0:
		writeJSON(w, http.StatusConflict, result)
	case result.Succeeded == 0:
		writeJSON(w, http.StatusBadGateway, result)
	case result.Failed > 0:
		writeJSON(w, http.StatusMultiStatus, result)
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleNotificationStatus 处理通知状态查询
func (app *application) handleNotificationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	manager := app.notificationManagerSnapshot()
	if manager == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled":   false,
			"notifiers": []map[string]interface{}{},
		})
		return
	}

	status := map[string]interface{}{
		"enabled":   manager.IsEnabled(),
		"notifiers": manager.GetNotifiers(),
		"dropped":   manager.Dropped(),
	}

	writeJSON(w, http.StatusOK, status)
}

// handleBackupInfo 读取设备备份信息（只读）。GET /api/backup-info/{udid}
func (app *application) handleBackupInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-info/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	path, err := app.backupQueryPath(udid)
	if err != nil {
		writeOperationError(w, http.StatusBadRequest, err, i18n.BackupInfoFailed)
		return
	}
	// 占用空间来自磁盘（遍历备份目录），与设备无关——即便下面取设备 info 失败也照样返回
	sizeHuman := humanSize(dirSizeBytes(path))

	out, err := app.BackupInfo(r.Context(), udid)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"udid": udid, "info": "", "size_human": sizeHuman, "info_status": "unavailable",
			"info_error": operationErrorPayload(w, err, i18n.BackupInfoFailed),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"udid": udid, "info": string(out), "size_human": sizeHuman, "info_status": "available",
	})
}

// handleBackupList 列出备份内文件（只读，CSV→JSON）。GET /api/backup-list/{udid}
func (app *application) handleBackupList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-list/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	path, err := app.backupQueryPath(udid)
	if err != nil {
		writeOperationError(w, http.StatusBadRequest, err, i18n.BackupListFailed)
		return
	}
	if _, _, err := app.backupDeviceContext(udid); err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.BackupListFailed)
		return
	}
	empty, err := confirmedBackupAbsent(path)
	if err != nil {
		writeOperationError(w, http.StatusConflict, &backupQueryError{diagnostic: backupQueryDiagnostic{Operation: "list", Stage: "admission", Reason: "storage_unavailable", Summary: "Local backup storage is unavailable."}, cause: err}, i18n.BackupListFailed)
		return
	}
	if empty && r.URL.Query().Get("format") != "csv" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"udid": udid, "entries": []backupListEntry{}, "count": 0, "truncated": false, "empty": true})
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		app.serveBackupCSV(w, r, udid, empty)
		return
	}

	const maxJSONEntries = 5000
	entries, total, truncated, err := app.BackupListLimited(r.Context(), udid, maxJSONEntries)
	if err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.BackupListFailed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"udid":      udid,
		"entries":   entries,
		"count":     total,
		"truncated": truncated,
		"empty":     false,
	})
}

// handleBackupUnback 解包备份（长操作，后台执行）。POST /api/backup-unback/{udid}
func (app *application) handleBackupUnback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	if !app.requireExperimentalOperations(w) {
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-unback/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	// 纯本地操作：设备离线也可执行；只要求本地存在完整备份。
	if !app.hasLocalBackup(udid) {
		writeError(w, http.StatusConflict, i18n.BackupMissing)
		return
	}
	if err := requirePlainSQLiteManifest(filepath.Join(app.deviceBackupPath(udid), "Manifest.db")); err != nil {
		writeOperationError(w, http.StatusConflict, err, i18n.OperationFailed)
		return
	}
	go func() {
		if err := app.Unback(context.Background(), udid); err != nil {
			message := fmt.Sprintf("event=unpack.request_failed error=%v", err)
			app.addErrorLog(udid, message)
			app.notifySystemEvent("unpack_failed", map[string]string{"device_udid": udid, "diagnostics": message})
		}
	}()
	writeSuccess(w, i18n.UnpackStarted)
}

// csrfHeader 状态变更端点要求的自定义头（拒绝简单跨站表单 POST）。前端所有 P4 POST 须带它。
const csrfHeader = "X-IOSBK-CSRF"

// csrfOK 简单 CSRF 防护：要求自定义头存在。跨站简单表单 POST 无法设置自定义头。
func csrfOK(r *http.Request) bool {
	return r.Header.Get(csrfHeader) != ""
}

// handleEncryption 开/关备份加密（改设备状态，后台执行）。POST /api/encryption/{udid}
func (app *application) handleEncryption(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/encryption/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	var req struct {
		Enable   bool   `json:"enable"`
		Password string `json:"password"`
	}
	if err := decodeRequestJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, i18n.EncryptionPasswordRequired)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), encryptionOpTimeout)
		defer cancel()
		if err := app.SetBackupEncryption(ctx, udid, req.Enable, req.Password); err != nil {
			message := fmt.Sprintf("event=encryption.request_failed error=%v", err)
			app.addErrorLog(udid, message)
			app.notifySystemEvent("encryption_failed", map[string]string{"device_udid": udid, "diagnostics": message})
		}
	}()
	writeSuccess(w, i18n.EncryptionSubmitted)
}

// handleChangePassword 改备份密码（改设备状态，后台执行）。POST /api/backup-changepw/{udid}
func (app *application) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/backup-changepw/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := decodeRequestJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	if req.Old == "" || req.New == "" {
		writeError(w, http.StatusBadRequest, i18n.PasswordsRequired)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), encryptionOpTimeout)
		defer cancel()
		if err := app.ChangeBackupPassword(ctx, udid, req.Old, req.New); err != nil {
			message := fmt.Sprintf("event=encryption.password_change_failed error=%v", err)
			app.addErrorLog(udid, message)
			app.notifySystemEvent("password_change_failed", map[string]string{"device_udid": udid, "diagnostics": message})
		}
	}()
	writeSuccess(w, i18n.PasswordChangeSubmitted)
}

// restoreReq 恢复请求体（多重确认 + 选项）。
type restoreReq struct {
	Confirmations struct {
		Understand bool   `json:"understand"`  // 理解破坏性
		DeviceName string `json:"device_name"` // 用户手输，trim 后须与设备名精确匹配
	} `json:"confirmations"`
	Password string         `json:"password"` // 加密备份需要（经 env，不入 argv）
	Options  restoreOptions `json:"options"`
}

// handleRestore 恢复备份到设备（**破坏性**，默认禁用 + 多重确认 + CSRF + 审计）。POST /api/restore/{udid}
func (app *application) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, i18n.CSRFRequired)
		return
	}
	udid := extractUDIDFromPath(r.URL.Path, "/api/restore/")
	if udid == "" {
		writeError(w, http.StatusBadRequest, i18n.InvalidUDID)
		return
	}
	if app.rejectRemovedDeviceOperation(w, udid) {
		return
	}

	app.mu.RLock()
	cfg, cfgOk := app.configs[udid]
	dev, devOk := app.devices[udid]
	restoreEnabled := cfgOk && cfg.RestoreEnabled
	devName := ""
	devOnline := false
	if devOk {
		devName = dev.Name
		devOnline = dev.IsOnline
	}
	app.mu.RUnlock()

	// 默认禁用：未开启恢复直接 403（审计）
	if !restoreEnabled {
		app.addWarnLog(udid, "event=restore.rejected reason=restore_disabled")
		writeError(w, http.StatusForbidden, i18n.RestoreDisabled)
		return
	}

	var req restoreReq
	if err := decodeRequestJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, i18n.InvalidJSON)
		return
	}
	if !req.Confirmations.Understand {
		writeError(w, http.StatusBadRequest, i18n.RestoreConfirmationRequired)
		return
	}
	if !devOk || !devOnline {
		writeError(w, http.StatusConflict, i18n.DeviceOffline)
		return
	}
	// 设备名 trim 后精确匹配
	if strings.TrimSpace(req.Confirmations.DeviceName) != devName {
		app.addWarnLog(udid, "event=restore.rejected reason=device_name_mismatch")
		writeError(w, http.StatusBadRequest, i18n.ConfirmNameMismatch)
		return
	}

	app.addWarnLog(udid, "event=restore.authorized Destructive restore confirmed by acknowledgement, device name and CSRF")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), restoreOpTimeout)
		defer cancel()
		if err := app.Restore(ctx, udid, req.Password, req.Options); err != nil {
			message := fmt.Sprintf("event=restore.failed error=%v", err)
			app.addErrorLog(udid, message)
			app.notifySystemEvent("restore_failed", map[string]string{"device_udid": udid, "diagnostics": message})
		}
	}()
	writeSuccess(w, i18n.RestoreStarted)
}

// handleNotifications 处理通知设置页面
func (app *application) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, i18n.MethodNotAllowed)
		return
	}

	// 读取通知设置页面模板
	tmpl, err := templateFS.ReadFile("templates/notifications.html")
	if err != nil {
		log.Printf("event=ui.template_read_failed page=notifications error=%v", err)
		writeError(w, http.StatusInternalServerError, i18n.PageRenderFailed)
		return
	}

	parsed, err := template.New("notifications.html").Funcs(template.FuncMap{"t": uiText}).Parse(string(tmpl))
	if err != nil {
		log.Printf("event=ui.template_parse_failed page=notifications error=%v", err)
		writeError(w, http.StatusInternalServerError, i18n.PageRenderFailed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Locale      string
		CSRFToken   string
		Version     string
		Commit      string
		SourceURL   string
		AuthEnabled bool
	}{i18n.ResolveRequest(r), app.csrfManager.Token(), buildinfo.Version, buildinfo.Commit, buildinfo.SourceURL, app.authManager.Enabled()}
	if err := parsed.Execute(w, data); err != nil {
		log.Printf("event=ui.template_render_failed page=notifications error=%v", err)
	}
}
