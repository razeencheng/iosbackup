package notification

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultWorkerCount = 4
	defaultQueueSize   = 100
	maxWorkerCount     = 16
	maxQueueSize       = 1000
)

// Options 提供不依赖应用层的时钟、日志与队列配置。
type Options struct {
	Now         func() time.Time
	Logf        func(format string, args ...any)
	WorkerCount int
	QueueSize   int
}

// Manager 以有界队列异步扇出通知。
type Manager struct {
	notifiers             []Notifier
	enabled               bool
	notificationRules     map[string][]string
	notificationTemplates map[string]string
	mu                    sync.RWMutex
	queue                 chan job
	ctx                   context.Context
	cancel                context.CancelFunc
	workers               sync.WaitGroup
	dropped               atomic.Uint64
	closed                atomic.Bool
	closeOnce             sync.Once
	now                   func() time.Time
	logf                  func(format string, args ...any)
}

type job struct {
	notifier Notifier
	message  Message
	result   chan<- Delivery
}

// NewManager 创建一个通知管理器。
func NewManager(options Options) *Manager {
	workerCount := options.WorkerCount
	if workerCount == 0 {
		workerCount = defaultWorkerCount
	}
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > maxWorkerCount {
		workerCount = maxWorkerCount
	}
	queueSize := options.QueueSize
	if queueSize == 0 {
		queueSize = defaultQueueSize
	}
	if queueSize < 1 {
		queueSize = 1
	}
	if queueSize > maxQueueSize {
		queueSize = maxQueueSize
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logf := options.Logf
	if logf == nil {
		logf = log.Printf
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		notifiers:             make([]Notifier, 0),
		enabled:               true,
		notificationRules:     make(map[string][]string),
		notificationTemplates: DefaultTemplates(),
		queue:                 make(chan job, queueSize),
		ctx:                   ctx,
		cancel:                cancel,
		now:                   now,
		logf:                  logf,
	}
	for i := 0; i < workerCount; i++ {
		manager.workers.Add(1)
		go manager.worker()
	}
	return manager
}

func (manager *Manager) worker() {
	defer manager.workers.Done()
	for {
		if manager.ctx.Err() != nil {
			return
		}
		select {
		case <-manager.ctx.Done():
			return
		case work := <-manager.queue:
			delivery := Delivery{Notifier: work.notifier.GetName()}
			if err := work.notifier.Send(&work.message); err != nil {
				delivery.Error = err.Error()
				manager.logf("通知器 %s 发送失败: %v", work.notifier.GetName(), err)
			} else {
				manager.logf("通知器 %s 发送成功: %s", work.notifier.GetName(), work.message.Title)
			}
			if work.result != nil {
				work.result <- delivery
			}
		}
	}
}

// AddNotifier 添加并验证通知器。
func (manager *Manager) AddNotifier(notifier Notifier) error {
	if isNilNotifier(notifier) {
		return fmt.Errorf("通知器不能为空")
	}
	if err := notifier.Validate(); err != nil {
		return fmt.Errorf("通知器 %s 配置验证失败: %v", notifier.GetName(), err)
	}
	manager.mu.Lock()
	manager.notifiers = append(manager.notifiers, notifier)
	manager.mu.Unlock()
	manager.logf("已添加通知器: %s", notifier.GetName())
	return nil
}

func isNilNotifier(notifier Notifier) bool {
	if notifier == nil {
		return true
	}
	value := reflect.ValueOf(notifier)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// SetRules 设置通知规则并隔离调用者后续修改。
func (manager *Manager) SetRules(rules map[string][]string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.notificationRules = CloneRules(rules)
}

func (manager *Manager) SetNotificationRules(rules map[string][]string) { manager.SetRules(rules) }

// Rules 返回通知规则副本。
func (manager *Manager) Rules() map[string][]string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return CloneRules(manager.notificationRules)
}

func (manager *Manager) GetNotificationRules() map[string][]string { return manager.Rules() }

// SetTemplates 校验并设置通知模板。
func (manager *Manager) SetTemplates(templates map[string]string) error {
	if err := ValidateTemplates(templates); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.notificationTemplates = MergeTemplates(templates)
	return nil
}

func (manager *Manager) SetNotificationTemplates(templates map[string]string) error {
	return manager.SetTemplates(templates)
}

// Templates 返回通知模板副本。
func (manager *Manager) Templates() map[string]string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return CloneStringMap(manager.notificationTemplates)
}

func (manager *Manager) GetNotificationTemplates() map[string]string { return manager.Templates() }

// Send 保持业务发送的异步 best-effort 语义；队列满时立即丢弃并计数。
func (manager *Manager) Send(message *Message) {
	if manager == nil {
		return
	}
	targets, messageCopy := manager.deliveryTargets(message)
	if len(targets) == 0 {
		if message != nil {
			manager.logf("消息类型 %s 没有可用通知器，跳过发送", message.Type)
		}
		return
	}
	for _, notifier := range targets {
		work := job{notifier: notifier, message: CloneMessage(messageCopy)}
		select {
		case manager.queue <- work:
		default:
			manager.dropped.Add(1)
		}
	}
}

// SendAndWait 通过同一有界队列发送，并等待实际发送结果。
func (manager *Manager) SendAndWait(ctx context.Context, message *Message) SendResult {
	result := SendResult{Deliveries: []Delivery{}}
	if manager == nil {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	targets, messageCopy := manager.deliveryTargets(message)
	if len(targets) == 0 {
		return result
	}
	results := make(chan Delivery, len(targets))
	for _, notifier := range targets {
		work := job{notifier: notifier, message: CloneMessage(messageCopy), result: results}
		select {
		case manager.queue <- work:
			result.Attempted++
		case <-ctx.Done():
			result.Failed++
			result.Deliveries = append(result.Deliveries, Delivery{Notifier: notifier.GetName(), Error: ctx.Err().Error()})
			return result
		}
	}
	for received := 0; received < result.Attempted; received++ {
		select {
		case delivery := <-results:
			result.Deliveries = append(result.Deliveries, delivery)
			if delivery.Error == "" {
				result.Succeeded++
			} else {
				result.Failed++
			}
		case <-ctx.Done():
			result.Failed += result.Attempted - received
			return result
		}
	}
	return result
}

func (manager *Manager) deliveryTargets(message *Message) ([]Notifier, Message) {
	if message == nil || manager.closed.Load() {
		return nil, Message{}
	}
	manager.mu.RLock()
	enabled := manager.enabled
	notifiers := append([]Notifier(nil), manager.notifiers...)
	rules := CloneRules(manager.notificationRules)
	templates := CloneStringMap(manager.notificationTemplates)
	manager.mu.RUnlock()
	if !enabled {
		return nil, Message{}
	}
	messageCopy := CloneMessage(*message)
	messageCopy.Timestamp = manager.now()
	messageCopy.Content = renderTemplate(messageCopy, templates)
	allowed, exists := rules[string(message.Type)]
	if !exists || len(allowed) == 0 {
		manager.logf("消息类型 %s 没有配置通知规则或规则为空，跳过发送", message.Type)
		return nil, messageCopy
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	targets := make([]Notifier, 0, len(notifiers))
	for _, notifier := range notifiers {
		if notifier.IsEnabled() && allowedSet[notifier.GetName()] {
			targets = append(targets, notifier)
		}
	}
	return targets, messageCopy
}

func (manager *Manager) Dropped() uint64 {
	if manager == nil {
		return 0
	}
	return manager.dropped.Load()
}

func (manager *Manager) IsEnabled() bool {
	if manager == nil {
		return false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.enabled
}

// Close 可重复调用；它停止接收和处理新任务，并等待正在执行的通知器返回。
func (manager *Manager) Close() {
	if manager == nil {
		return
	}
	manager.closeOnce.Do(func() {
		manager.closed.Store(true)
		manager.cancel()
		manager.workers.Wait()
	})
}

func (manager *Manager) Enable() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	manager.enabled = true
	manager.mu.Unlock()
	manager.logf("通知管理器已启用")
}

func (manager *Manager) Disable() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	manager.enabled = false
	manager.mu.Unlock()
	manager.logf("通知管理器已禁用")
}

func (manager *Manager) GetNotifiers() []map[string]interface{} {
	if manager == nil {
		return nil
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]map[string]interface{}, 0, len(manager.notifiers))
	for _, notifier := range manager.notifiers {
		result = append(result, map[string]interface{}{"name": notifier.GetName(), "enabled": notifier.IsEnabled()})
	}
	return result
}

func (manager *Manager) SendBackupStart(deviceName, deviceUDID string) {
	manager.Send(&Message{Type: BackupStart, Level: LevelInfo, Title: "✅ 设备备份开始", Content: fmt.Sprintf("设备 %s 开始备份", deviceName), DeviceName: deviceName, DeviceUDID: deviceUDID})
}

func (manager *Manager) SendBackupSuccess(deviceName, deviceUDID string) {
	manager.Send(&Message{Type: BackupSuccess, Level: LevelInfo, Title: "✅ 设备备份完成", Content: fmt.Sprintf("设备 %s 备份成功完成", deviceName), DeviceName: deviceName, DeviceUDID: deviceUDID})
}

func (manager *Manager) SendBackupFailed(deviceName, deviceUDID, reason string) {
	manager.Send(&Message{Type: BackupFailed, Level: LevelError, Title: "❌ 设备备份失败", Content: fmt.Sprintf("设备 %s 备份失败\n原因: %s", deviceName, reason), DeviceName: deviceName, DeviceUDID: deviceUDID, ExtraFields: map[string]string{"reason": reason}})
}

func (manager *Manager) SendDeviceOnline(deviceName, deviceUDID string) {
	manager.Send(&Message{Type: DeviceOnline, Level: LevelInfo, Title: "✅ 设备上线", Content: fmt.Sprintf("设备 %s 已连接", deviceName), DeviceName: deviceName, DeviceUDID: deviceUDID})
}

func (manager *Manager) SendDeviceOffline(deviceName, deviceUDID string) {
	manager.Send(&Message{Type: DeviceOffline, Level: LevelWarning, Title: "❌ 设备离线", Content: fmt.Sprintf("设备 %s 已断开连接", deviceName), DeviceName: deviceName, DeviceUDID: deviceUDID})
}

func (manager *Manager) SendSystemError(title, content string) {
	manager.Send(&Message{Type: SystemError, Level: LevelError, Title: fmt.Sprintf("⚠️ 系统错误: %s", title), Content: content})
}

var templateVariablePattern = regexp.MustCompile(`\$\{([a-z_][a-z0-9_]*)\}`)

func DefaultTemplates() map[string]string {
	return map[string]string{
		string(BackupStart):   "设备 ${device_name} 开始备份",
		string(BackupSuccess): "设备 ${device_name} 备份成功完成",
		string(BackupFailed):  "设备 ${device_name} 备份失败\n原因: ${reason}",
		string(DeviceOnline):  "设备 ${device_name} 已连接",
		string(DeviceOffline): "设备 ${device_name} 已断开连接",
		string(SystemError):   "${content}",
	}
}

func MergeTemplates(templates map[string]string) map[string]string {
	merged := DefaultTemplates()
	for key, value := range templates {
		if value != "" {
			merged[key] = value
		}
	}
	return merged
}

func ValidateTemplates(templates map[string]string) error {
	knownTypes := map[string]bool{
		string(BackupStart): true, string(BackupSuccess): true, string(BackupFailed): true,
		string(DeviceOnline): true, string(DeviceOffline): true, string(SystemError): true,
	}
	knownVariables := map[string]bool{
		"type": true, "level": true, "title": true, "content": true,
		"device_name": true, "device_udid": true, "timestamp": true, "reason": true,
	}
	for eventType, template := range templates {
		if !knownTypes[eventType] {
			return fmt.Errorf("未知通知模板类型: %s", eventType)
		}
		if len(template) > 8192 {
			return fmt.Errorf("通知模板 %s 过长", eventType)
		}
		for _, match := range templateVariablePattern.FindAllStringSubmatch(template, -1) {
			if !knownVariables[match[1]] {
				return fmt.Errorf("通知模板 %s 使用了未知变量 ${%s}", eventType, match[1])
			}
		}
	}
	return nil
}

func renderTemplate(message Message, templates map[string]string) string {
	template := templates[string(message.Type)]
	if template == "" {
		return message.Content
	}
	values := map[string]string{
		"type": string(message.Type), "level": string(message.Level), "title": message.Title,
		"content": message.Content, "device_name": message.DeviceName, "device_udid": message.DeviceUDID,
		"timestamp": message.Timestamp.Format(time.RFC3339),
	}
	for key, value := range message.ExtraFields {
		values[key] = value
	}
	return templateVariablePattern.ReplaceAllStringFunc(template, func(variable string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(variable, "${"), "}")
		return values[name]
	})
}

// ValidateRules 校验规则只引用已启用并已注册的通知器。
func ValidateRules(rules map[string][]string, notifiers []map[string]interface{}) error {
	knownTypes := map[string]bool{
		string(BackupStart): true, string(BackupSuccess): true, string(BackupFailed): true,
		string(DeviceOnline): true, string(DeviceOffline): true, string(SystemError): true,
	}
	knownNotifiers := make(map[string]bool, len(notifiers))
	for _, notifier := range notifiers {
		if name, ok := notifier["name"].(string); ok {
			knownNotifiers[name] = true
		}
	}
	for messageType, names := range rules {
		if !knownTypes[messageType] {
			return fmt.Errorf("未知通知消息类型: %s", messageType)
		}
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if !knownNotifiers[name] {
				return fmt.Errorf("通知规则 %s 引用了不存在或未启用的通知器 %s", messageType, name)
			}
			if seen[name] {
				return fmt.Errorf("通知规则 %s 重复引用通知器 %s", messageType, name)
			}
			seen[name] = true
		}
	}
	return nil
}

// NewManagerFromConfig 根据已由应用层解密的配置构造通知管理器。
func NewManagerFromConfig(config *Config, allowPrivate []netip.Prefix, options Options) (*Manager, error) {
	if config == nil {
		return nil, fmt.Errorf("通知配置不能为空")
	}
	manager := NewManager(options)
	if !config.Enabled {
		manager.Disable()
		return manager, nil
	}
	for _, item := range config.TelegramConfigs {
		if item.Enabled {
			if err := manager.AddNotifier(NewTelegramNotifier(item)); err != nil {
				manager.Close()
				return nil, err
			}
		}
	}
	for _, item := range config.EmailConfigs {
		if item.Enabled {
			if err := manager.AddNotifier(NewEmailNotifier(item)); err != nil {
				manager.Close()
				return nil, err
			}
		}
	}
	for _, item := range config.WecomConfigs {
		if item.Enabled {
			if err := manager.AddNotifier(NewWecomNotifier(item)); err != nil {
				manager.Close()
				return nil, err
			}
		}
	}
	for _, item := range config.BarkConfigs {
		if item.Enabled {
			if err := manager.AddNotifier(NewBarkNotifierWithAllowlist(item, allowPrivate)); err != nil {
				manager.Close()
				return nil, err
			}
		}
	}
	for _, item := range config.WebhookConfigs {
		if item.Enabled {
			if err := manager.AddNotifier(NewWebhookNotifierWithAllowlist(item, allowPrivate)); err != nil {
				manager.Close()
				return nil, err
			}
		}
	}
	if err := ValidateRules(config.NotificationRules, manager.GetNotifiers()); err != nil {
		manager.Close()
		return nil, err
	}
	if err := manager.SetTemplates(config.NotificationTemplates); err != nil {
		manager.Close()
		return nil, err
	}
	manager.SetRules(config.NotificationRules)
	return manager, nil
}
