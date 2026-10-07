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
	return &exitError{prefix: "运行配置无效: ", err: err}
}

func newApplicationExitError(err error) error {
	return &exitError{prefix: "应用退出: ", err: err}
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
		log.Printf("初始化通知管理器失败: %v", err)
		// 创建一个空的通知管理器，但禁用通知功能
		notificationManager = newNotificationManager()
		notificationManager.Disable()
	}
	app.replaceNotificationManager(notificationManager)
	// 先发布事件总线，再启动任何可能广播状态的后台任务。
	app.hub = newEventHub()

	// 加载配置
	if err := app.loadConfigs(); err != nil {
		log.Printf("加载配置失败: %v", err)
	}

	// 启动 usbmuxd（USB 设备）
	if err := app.StartUSBMuxD(); err != nil {
		log.Printf("启动 usbmuxd 失败: %v", err)
	}

	// 启动 netmuxd（Wi-Fi 设备）
	if err := app.StartNetmuxd(); err != nil {
		log.Printf("启动 netmuxd 失败: %v", err)
	} else {
		// netmuxd 启动后重放 add_device（异步，不阻塞启动流程）
		go app.replayAddDevice()
	}

	// 初始刷新设备列表
	if err := app.RefreshDevices(); err != nil {
		log.Printf("初始刷新设备列表失败: %v", err)
	}

	// 每条退出路径均先取消 worker，避免 HTTP 意外退出后后台再次重启进程。
	var workers sync.WaitGroup
	startWorker := func(work func(context.Context)) { workers.Add(1); go func() { defer workers.Done(); work(ctx) }() }
	defer func() {
		cancelWorkers()
		app.mu.Lock()
		app.connectionClosed = true
		app.mu.Unlock()
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

	log.Printf("iOS 备份管理服务器 %s (%s) 启动在 %s", buildinfo.Version, buildinfo.Description, addr)
	log.Printf("构建日期: %s", buildinfo.BuildDate)
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
			return fmt.Errorf("关闭 HTTP 服务器: %w", shutdownErr)
		}
		return nil
	}
}

// initializeSecurity 完成持久化与校验后才允许启动外部服务。
func (app *application) initializeSecurity() error {
	cfg, paths := app.runtimeConfig, app.paths
	store, err := initSecretStore(cfg, paths)
	if err != nil {
		return fmt.Errorf("初始化加密存储: %w", err)
	}
	auth, generatedPassword, err := newAuthManager(cfg, paths)
	if err != nil {
		return fmt.Errorf("初始化管理员认证: %w", err)
	}
	csrf, err := newCSRFManager(paths.CSRFSecretFile)
	if err != nil {
		return fmt.Errorf("初始化 CSRF 防护: %w", err)
	}
	app.authManager, app.csrfManager, app.secretStore = auth, csrf, store
	if !cfg.AuthEnabled {
		log.Printf("WARN: 管理员认证已关闭；建议在 NAS 部署中启用 IOSBK_AUTH_ENABLED=true")
	} else if generatedPassword != "" {
		log.Printf("首次生成管理员密码（仅显示一次）：password=%s；已保存到 %s", generatedPassword, paths.AdminPasswordFile)
	} else if cfg.AdminPasswordFile != "" {
		log.Printf("管理员密码认证已启用；密码来源文件：%s", cfg.AdminPasswordFile)
	} else if _, err := os.Lstat(paths.AdminPasswordFile); err == nil {
		log.Printf("管理员密码认证已启用；密码文件：%s", paths.AdminPasswordFile)
	} else {
		log.Printf("管理员密码认证已启用；沿用旧认证摘要，未生成替代密码")
	}
	return nil
}
