package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"iosbackup/internal/buildinfo"
	"iosbackup/internal/persistence"
)

// loadConfigs 加载配置
func (app *application) loadConfigs() error {
	// 创建必要的目录
	if err := persistence.EnsurePrivateDirectory(app.paths.ConfigsRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(app.paths.BackupsRoot, 0755); err != nil {
		return err
	}
	configs, err := app.configStore.Load()
	if err != nil {
		return err
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	app.configs = make(map[string]*backupConfig, len(configs))
	for udid, cfg := range configs {
		cfgCopy := cfg
		app.configs[udid] = &cfgCopy
	}
	return nil
}

// saveConfigs 保存配置
func (app *application) saveConfigs() error {
	app.configPersistMu.Lock()
	defer app.configPersistMu.Unlock()
	app.mu.RLock()
	configs := make(map[string]backupConfig, len(app.configs))
	for udid, cfg := range app.configs {
		if cfg != nil {
			configs[udid] = *cfg
		}
	}
	app.mu.RUnlock()
	return app.configStore.Replace(configs)
}

// Run loads the runtime configuration and runs the application until ctx is
// cancelled or the HTTP server exits.
func Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cfg, err := loadRuntimeConfig(os.Getenv)
	if err != nil {
		return newRuntimeConfigExitError(err)
	}
	if err := run(ctx, cfg); err != nil {
		return newApplicationExitError(err)
	}
	return nil
}

type exitError struct {
	prefix string
	err    error
}

func (e *exitError) Error() string { return e.prefix + e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func newRuntimeConfigExitError(err error) error {
	return &exitError{prefix: "event=config.invalid Invalid runtime configuration: ", err: err}
}

func newApplicationExitError(err error) error {
	return &exitError{prefix: "event=application.failed Application failed: ", err: err}
}

func run(ctx context.Context, cfg runtimeConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ctx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	// 初始化日志级别
	initLogLevel()
	paths := applyRuntimeConfig(cfg)

	app := newApplicationWithRuntime(ctx, cfg)
	if err := app.initializeSecurity(); err != nil {
		return err
	}
	app.notificationConfigStore = newNotificationConfigStore(paths.NotificationConfigFile, app.secretStore)

	// 初始化通知管理器
	notificationManager, err := initNotificationManagerFromStore(app.notificationConfigStore, cfg.WebhookAllowCIDRs)
	if err != nil {
		log.Printf("event=notification.init_failed error=%v", err)
		// 创建一个空的通知管理器，但禁用通知功能
		notificationManager = newNotificationManager()
		notificationManager.Disable()
	}
	app.replaceNotificationManager(notificationManager)
	// 先发布事件总线，再启动任何可能广播状态的后台任务。
	app.hub = newEventHub()

	// 加载配置
	if err := app.loadConfigs(); err != nil {
		log.Printf("event=config.load_failed error=%v", err)
	}

	// 启动 usbmuxd（USB 设备）
	if err := app.StartUSBMuxD(); err != nil {
		log.Printf("event=usb.start_failed error=%v", err)
	}

	// 启动 netmuxd（Wi-Fi 设备）
	if err := app.StartNetmuxd(); err != nil {
		log.Printf("event=network.netmuxd_start_failed error=%v", err)
	} else {
		// netmuxd 启动后重放 add_device（异步，不阻塞启动流程）
		go app.replayAddDevice()
	}

	// 初始刷新设备列表
	if err := app.RefreshDevices(); err != nil {
		log.Printf("event=device.initial_refresh_failed error=%v", err)
	}

	// 每条退出路径均先取消 worker，避免 HTTP 意外退出后后台再次重启进程。
	var workers sync.WaitGroup
	startWorker := func(work func(context.Context)) { workers.Add(1); go func() { defer workers.Done(); work(ctx) }() }
	defer func() {
		cancelWorkers()
		app.mu.Lock()
		app.connectionClosed = true
		app.mu.Unlock()
		app.closeBackupIndex()
		workers.Wait()
		app.connectionRecoveryWorkers.Wait()
		app.StopNetmuxd()
		app.StopUSBMuxD()
	}()
	startWorker(app.autoBackupScheduler)

	// 启动 SSE 事件总线 + 中央状态轮询（实时推送设备状态给浏览器）
	startWorker(app.statusPoller)
	startWorker(app.networkRecoveryLoop)
	startWorker(app.connectionRecoveryLoop)

	// 设置路由并启动服务器
	mux := app.setupRoutes()

	addr := cfg.ServerAddress()

	server := newHTTPServer(addr, mux)

	log.Printf("event=server.started iOS Backup version=%s description=%s address=%s", buildinfo.Version, buildinfo.Description, addr)
	log.Printf("event=build.date date=%s", buildinfo.BuildDate)
	serverErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErr <- err
	}()

	select {
	case err := <-serverErr:
		cancelWorkers()
		app.notificationManagerSnapshot().Close()
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		app.notificationManagerSnapshot().Close()
		if shutdownErr != nil {
			return fmt.Errorf("shut down HTTP server: %w", shutdownErr)
		}
		return nil
	}
}

// initializeSecurity 完成持久化与校验后才允许启动外部服务。
func (app *application) initializeSecurity() error {
	cfg, paths := app.runtimeConfig, app.paths
	store, err := initSecretStore(cfg, paths)
	if err != nil {
		return fmt.Errorf("initialize encrypted storage: %w", err)
	}
	auth, generatedPassword, err := newAuthManager(cfg, paths)
	if err != nil {
		return fmt.Errorf("initialize administrator authentication: %w", err)
	}
	csrf, err := newCSRFManager(paths.CSRFSecretFile)
	if err != nil {
		return fmt.Errorf("initialize CSRF protection: %w", err)
	}
	app.authManager, app.csrfManager, app.secretStore = auth, csrf, store
	if !cfg.AuthEnabled {
		log.Printf("event=auth.disabled WARN: Administrator authentication is disabled; enable IOSBK_AUTH_ENABLED=true for NAS deployments")
	} else if generatedPassword != "" {
		log.Printf("event=auth.password_generated Initial administrator password (shown once): password=%s; saved to %s", generatedPassword, paths.AdminPasswordFile)
	} else if cfg.AdminPasswordFile != "" {
		log.Printf("event=auth.enabled Administrator password source=%s", cfg.AdminPasswordFile)
	} else if _, err := os.Lstat(paths.AdminPasswordFile); err == nil {
		log.Printf("event=auth.enabled Administrator password file=%s", paths.AdminPasswordFile)
	} else {
		log.Printf("event=auth.enabled Using existing password digest; no replacement password generated")
	}
	return nil
}
