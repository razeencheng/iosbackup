package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockNotifier 模拟通知器，用于测试
type MockNotifier struct {
	name        string
	enabled     bool
	shouldError bool
	eventsOnce  sync.Once
	events      chan *notificationMessage
	sendCount   atomic.Int64
	lastMessage atomic.Pointer[notificationMessage]
}

func (m *MockNotifier) Send(message *notificationMessage) error {
	m.eventsOnce.Do(func() {
		m.events = make(chan *notificationMessage, 1024)
	})
	messageCopy := *message
	m.sendCount.Add(1)
	m.lastMessage.Store(&messageCopy)
	m.events <- &messageCopy
	if m.shouldError {
		return fmt.Errorf("mock send error")
	}
	return nil
}

func (m *MockNotifier) Count() int {
	return int(m.sendCount.Load())
}

func (m *MockNotifier) LastMessage() *notificationMessage {
	return m.lastMessage.Load()
}

func (m *MockNotifier) Reset() {
	m.eventsOnce.Do(func() {
		m.events = make(chan *notificationMessage, 1024)
	})
	for {
		select {
		case <-m.events:
		default:
			m.sendCount.Store(0)
			m.lastMessage.Store(nil)
			return
		}
	}
}

func (m *MockNotifier) WaitForSends(t *testing.T, count int) *notificationMessage {
	t.Helper()
	m.eventsOnce.Do(func() {
		m.events = make(chan *notificationMessage, 1024)
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	var last *notificationMessage
	for i := 0; i < count; i++ {
		select {
		case last = <-m.events:
		case <-timer.C:
			t.Fatalf("等待 %d 条通知超时，已收到 %d 条", count, i)
		}
	}
	return last
}

func (m *MockNotifier) GetName() string {
	return m.name
}

func (m *MockNotifier) IsEnabled() bool {
	return m.enabled
}

func (m *MockNotifier) Validate() error {
	if m.name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// 测试通知管理器基本功能
func TestNotificationManager(t *testing.T) {
	manager := newNotificationManager()

	// 测试初始状态
	if manager == nil {
		t.Fatal("创建通知管理器失败")
	}

	// 测试添加通知器
	mockNotifier := &MockNotifier{
		name:    "test_notifier",
		enabled: true,
	}

	err := manager.AddNotifier(mockNotifier)
	if err != nil {
		t.Fatalf("添加通知器失败: %v", err)
	}

	// 测试获取通知器信息
	notifiers := manager.GetNotifiers()
	if len(notifiers) != 1 {
		t.Fatalf("期望1个通知器，实际获得%d个", len(notifiers))
	}

	if notifiers[0]["name"] != "test_notifier" {
		t.Errorf("通知器名称不匹配，期望test_notifier，实际%s", notifiers[0]["name"])
	}

	// 测试启用/禁用
	manager.Disable()
	manager.Enable()
}

// 测试通知消息发送
func TestNotificationSend(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "test_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): {"test_notifier"},
	}
	manager.SetNotificationRules(rules)

	// 创建测试消息
	message := &notificationMessage{
		Type:       notificationBackupSuccess,
		Level:      notificationLevelInfo,
		Title:      "测试消息",
		Content:    "这是一条测试消息",
		DeviceName: "测试设备",
		DeviceUDID: "test-udid",
	}

	// 发送消息
	manager.Send(message)
	lastMessage := mockNotifier.WaitForSends(t, 1)

	// 验证发送结果
	if mockNotifier.Count() != 1 {
		t.Errorf("期望发送1次，实际发送%d次", mockNotifier.Count())
	}

	if lastMessage.Title != "测试消息" {
		t.Errorf("消息标题不匹配，期望'测试消息'，实际'%s'", lastMessage.Title)
	}
}

// 测试备份开始通知
func TestSendBackupStart(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "backup_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupStart): {"backup_notifier"},
	}
	manager.SetNotificationRules(rules)

	manager.SendBackupStart("iPhone 15", "12345-abcde")
	lastMessage := mockNotifier.WaitForSends(t, 1)

	if mockNotifier.Count() != 1 {
		t.Errorf("期望发送1次，实际发送%d次", mockNotifier.Count())
	}

	if lastMessage.Type != notificationBackupStart {
		t.Errorf("消息类型不匹配，期望%s，实际%s", notificationBackupStart, lastMessage.Type)
	}

	if lastMessage.DeviceName != "iPhone 15" {
		t.Errorf("设备名称不匹配，期望'iPhone 15'，实际'%s'", lastMessage.DeviceName)
	}
}

// 测试备份成功通知
func TestSendBackupSuccess(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "success_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): {"success_notifier"},
	}
	manager.SetNotificationRules(rules)

	manager.SendBackupSuccess("iPhone 15", "12345-abcde")
	lastMessage := mockNotifier.WaitForSends(t, 1)

	if mockNotifier.Count() != 1 {
		t.Errorf("期望发送1次，实际发送%d次", mockNotifier.Count())
	}

	if lastMessage.Type != notificationBackupSuccess {
		t.Errorf("消息类型不匹配")
	}

	if lastMessage.Level != notificationLevelInfo {
		t.Errorf("消息级别不匹配")
	}

	if !strings.Contains(lastMessage.Title, "✅") {
		t.Errorf("成功消息应包含✅符号")
	}
}

// 测试备份失败通知
func TestSendBackupFailed(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "error_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupFailed): {"error_notifier"},
	}
	manager.SetNotificationRules(rules)

	manager.SendBackupFailed("iPhone 15", "12345-abcde", "磁盘空间不足")
	lastMessage := mockNotifier.WaitForSends(t, 1)

	// 验证发送
	if mockNotifier.Count() != 1 {
		t.Errorf("期望发送1次，实际发送%d次", mockNotifier.Count())
	}

	if lastMessage.Type != notificationBackupFailed {
		t.Errorf("消息类型不匹配")
	}

	if lastMessage.Level != notificationLevelError {
		t.Errorf("失败消息应为错误级别")
	}

	if !strings.Contains(lastMessage.Content, "磁盘空间不足") {
		t.Errorf("失败原因未包含在消息中")
	}
}

func TestNotificationTemplateRendersMessageVariables(t *testing.T) {
	manager := newNotificationManager()
	defer manager.Close()
	notifier := &MockNotifier{name: "template_notifier", enabled: true}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{
		string(notificationBackupFailed): {"template_notifier"},
	})
	if err := manager.SetNotificationTemplates(map[string]string{
		string(notificationBackupFailed): "${title} | ${type} | ${level} | ${device_name} | ${device_udid} | ${reason} | ${content}",
	}); err != nil {
		t.Fatal(err)
	}

	manager.SendBackupFailed("iPhone 15", "12345-abcde", "磁盘空间不足")
	message := notifier.WaitForSends(t, 1)
	want := "❌ 设备备份失败 | backup_failed | error | iPhone 15 | 12345-abcde | 磁盘空间不足 | 设备 iPhone 15 备份失败\n原因: 磁盘空间不足"
	if message.Content != want {
		t.Fatalf("模板渲染结果不匹配，得到 %q，期望 %q", message.Content, want)
	}
}

func TestNotificationTemplateDefaultsPreserveContent(t *testing.T) {
	manager := newNotificationManager()
	defer manager.Close()
	notifier := &MockNotifier{name: "default_template_notifier", enabled: true}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetNotificationRules(map[string][]string{
		string(notificationBackupSuccess): {"default_template_notifier"},
	})
	manager.SendBackupSuccess("iPhone 15", "12345-abcde")
	message := notifier.WaitForSends(t, 1)
	if message.Content != "设备 iPhone 15 备份成功完成" {
		t.Fatalf("默认模板不应改变现有正文，得到 %q", message.Content)
	}
}

func TestNotificationTemplateRejectsUnknownVariable(t *testing.T) {
	manager := newNotificationManager()
	defer manager.Close()
	if err := manager.SetNotificationTemplates(map[string]string{
		string(notificationBackupSuccess): "${unknown}",
	}); err == nil {
		t.Fatal("未知模板变量应该被拒绝")
	}
}

// 测试设备上线/离线通知
func TestDeviceStatusNotifications(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "status_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationDeviceOnline):  {"status_notifier"},
		string(notificationDeviceOffline): {"status_notifier"},
	}
	manager.SetNotificationRules(rules)

	// 测试设备上线
	manager.SendDeviceOnline("iPhone 15", "12345-abcde")
	onlineMessage := mockNotifier.WaitForSends(t, 1)

	if mockNotifier.Count() != 1 {
		t.Errorf("设备上线通知发送失败")
	}

	if onlineMessage.Type != notificationDeviceOnline {
		t.Errorf("设备上线消息类型不匹配")
	}

	// 测试设备离线
	manager.SendDeviceOffline("iPhone 15", "12345-abcde")
	offlineMessage := mockNotifier.WaitForSends(t, 1)

	if mockNotifier.Count() != 2 {
		t.Errorf("设备离线通知发送失败")
	}

	if offlineMessage.Type != notificationDeviceOffline {
		t.Errorf("设备离线消息类型不匹配")
	}

	if offlineMessage.Level != notificationLevelWarning {
		t.Errorf("设备离线应为警告级别")
	}
}

// 测试系统错误通知
func TestSendSystemError(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "system_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationSystemError): {"system_notifier"},
	}
	manager.SetNotificationRules(rules)

	manager.SendSystemError("usbmuxd启动失败", "无法连接到USB守护进程")
	lastMessage := mockNotifier.WaitForSends(t, 1)

	if mockNotifier.Count() != 1 {
		t.Errorf("系统错误通知发送失败")
	}

	if lastMessage.Type != notificationSystemError {
		t.Errorf("系统错误消息类型不匹配")
	}

	if lastMessage.Level != notificationLevelError {
		t.Errorf("系统错误应为错误级别")
	}

	if !strings.Contains(lastMessage.Title, "⚠️") {
		t.Errorf("系统错误消息应包含⚠️符号")
	}
}

// 测试禁用的通知器
func TestDisabledNotifier(t *testing.T) {
	manager := newNotificationManager()
	disabledNotifier := &MockNotifier{
		name:    "disabled_notifier",
		enabled: false, // 禁用状态
	}
	enabledNotifier := &MockNotifier{
		name:    "enabled_notifier",
		enabled: true,
	}

	manager.AddNotifier(disabledNotifier)
	manager.AddNotifier(enabledNotifier)

	// 设置通知规则，包含两个通知器
	rules := map[string][]string{
		string(notificationBackupSuccess): {"disabled_notifier", "enabled_notifier"},
	}
	manager.SetNotificationRules(rules)

	message := &notificationMessage{
		Type:    notificationBackupSuccess,
		Level:   notificationLevelInfo,
		Title:   "测试消息",
		Content: "测试内容",
	}

	manager.Send(message)
	enabledNotifier.WaitForSends(t, 1)

	// 禁用的通知器不应该收到消息
	if disabledNotifier.Count() != 0 {
		t.Errorf("禁用的通知器不应该收到消息，实际收到%d次", disabledNotifier.Count())
	}

	// 启用的通知器应该收到消息
	if enabledNotifier.Count() != 1 {
		t.Errorf("启用的通知器应该收到1次消息，实际收到%d次", enabledNotifier.Count())
	}
}

// 测试通知器发送错误处理
func TestNotifierSendError(t *testing.T) {
	manager := newNotificationManager()
	errorNotifier := &MockNotifier{
		name:        "error_notifier",
		enabled:     true,
		shouldError: true, // 模拟发送错误
	}

	manager.AddNotifier(errorNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): {"error_notifier"},
	}
	manager.SetNotificationRules(rules)

	message := &notificationMessage{
		Type:    notificationBackupSuccess,
		Level:   notificationLevelInfo,
		Title:   "测试消息",
		Content: "测试内容",
	}

	// 发送消息（应该处理错误而不崩溃）
	manager.Send(message)
	errorNotifier.WaitForSends(t, 1)

	// 验证尝试发送了
	if errorNotifier.Count() != 1 {
		t.Errorf("错误通知器应该尝试发送1次，实际%d次", errorNotifier.Count())
	}
}

// 测试配置创建和序列化
func TestNotificationConfig(t *testing.T) {
	// 测试创建配置
	config := &notificationConfig{
		Enabled: true,
		TelegramConfigs: []telegramConfig{
			{
				Name:     "test_bot",
				BotToken: "test_token",
				ChatID:   "test_chat_id",
				Enabled:  true,
			},
		},
		EmailConfigs: []emailConfig{
			{
				Name:     "test_email",
				SMTPHost: "smtp.gmail.com",
				SMTPPort: 587,
				Username: "test@gmail.com",
				Password: "password",
				From:     "test@gmail.com",
				To:       "target@gmail.com",
				Enabled:  false,
			},
		},
		NotificationRules: map[string][]string{
			string(notificationBackupSuccess): {"test_bot"},
			string(notificationBackupFailed):  {"test_bot", "test_email"},
		},
	}

	// 测试配置序列化
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("配置序列化失败: %v", err)
	}

	// 测试配置反序列化
	var loadedConfig notificationConfig
	err = json.Unmarshal(data, &loadedConfig)
	if err != nil {
		t.Fatalf("配置反序列化失败: %v", err)
	}

	// 验证配置内容
	if !loadedConfig.Enabled {
		t.Errorf("配置启用状态不匹配")
	}

	if len(loadedConfig.TelegramConfigs) != 1 {
		t.Errorf("期望1个Telegram配置，实际%d个", len(loadedConfig.TelegramConfigs))
	}

	if loadedConfig.TelegramConfigs[0].Name != "test_bot" {
		t.Errorf("Telegram配置名称不匹配")
	}

	if len(loadedConfig.EmailConfigs) != 1 {
		t.Errorf("期望1个Email配置，实际%d个", len(loadedConfig.EmailConfigs))
	}

	// 验证通知规则
	successRules := loadedConfig.NotificationRules[string(notificationBackupSuccess)]
	if len(successRules) != 1 || successRules[0] != "test_bot" {
		t.Errorf("备份成功通知规则不匹配")
	}
}

// 测试通知器验证
func TestNotifierValidation(t *testing.T) {
	manager := newNotificationManager()

	// 测试无效的通知器
	invalidNotifier := &MockNotifier{
		name:    "", // 空名称应该验证失败
		enabled: true,
	}

	err := manager.AddNotifier(invalidNotifier)
	if err == nil {
		t.Errorf("添加无效通知器应该失败")
	}

	// 测试有效的通知器
	validNotifier := &MockNotifier{
		name:    "valid_notifier",
		enabled: true,
	}

	err = manager.AddNotifier(validNotifier)
	if err != nil {
		t.Errorf("添加有效通知器不应该失败: %v", err)
	}
}

// 测试管理器启用/禁用状态
func TestManagerEnableDisable(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "test_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): {"test_notifier"},
	}
	manager.SetNotificationRules(rules)

	// 禁用管理器
	manager.Disable()

	message := &notificationMessage{
		Type:    notificationBackupSuccess,
		Level:   notificationLevelInfo,
		Title:   "测试消息",
		Content: "测试内容",
	}

	manager.Send(message)

	// 管理器禁用时不应该发送消息
	if mockNotifier.Count() != 0 {
		t.Errorf("管理器禁用时不应该发送消息，实际发送%d次", mockNotifier.Count())
	}

	// 重新启用管理器
	manager.Enable()
	manager.Send(message)
	mockNotifier.WaitForSends(t, 1)

	// 管理器启用后应该发送消息
	if mockNotifier.Count() != 1 {
		t.Errorf("管理器启用后应该发送消息，实际发送%d次", mockNotifier.Count())
	}
}

// 基准测试：测试通知发送性能
func BenchmarkNotificationSend(b *testing.B) {
	manager := newNotificationManager()

	// 添加多个模拟通知器
	notifierNames := make([]string, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("bench_notifier_%d", i)
		mockNotifier := &MockNotifier{
			name:    name,
			enabled: true,
		}
		manager.AddNotifier(mockNotifier)
		notifierNames[i] = name
	}

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): notifierNames,
	}
	manager.SetNotificationRules(rules)

	message := &notificationMessage{
		Type:       notificationBackupSuccess,
		Level:      notificationLevelInfo,
		Title:      "基准测试消息",
		Content:    "这是一条基准测试消息",
		DeviceName: "测试设备",
		DeviceUDID: "bench-test-udid",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		manager.Send(message)
	}
}

// 测试并发发送
func TestConcurrentSend(t *testing.T) {
	manager := newNotificationManager()
	mockNotifier := &MockNotifier{
		name:    "concurrent_notifier",
		enabled: true,
	}
	manager.AddNotifier(mockNotifier)

	// 设置通知规则
	rules := map[string][]string{
		string(notificationBackupSuccess): {"concurrent_notifier"},
	}
	manager.SetNotificationRules(rules)

	// 并发发送多条消息
	const goroutines = 10
	const messagesPerGoroutine = 5

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				message := &notificationMessage{
					Type:    notificationBackupSuccess,
					Level:   notificationLevelInfo,
					Title:   fmt.Sprintf("并发消息-%d-%d", id, j),
					Content: "并发测试内容",
				}
				manager.Send(message)
			}
		}(i)
	}

	expectedCount := goroutines * messagesPerGoroutine
	mockNotifier.WaitForSends(t, expectedCount)
	if mockNotifier.Count() != expectedCount {
		t.Errorf("期望发送%d条消息，实际发送%d条", expectedCount, mockNotifier.Count())
	}
}

// TestNotificationRules 测试通知规则功能
func TestNotificationRules(t *testing.T) {
	// 创建通知管理器
	manager := newNotificationManager()

	// 创建测试通知器
	telegramNotifier := &MockNotifier{name: "Telegram_test", enabled: true}
	emailNotifier := &MockNotifier{name: "Email_test", enabled: true}
	wecomNotifier := &MockNotifier{name: "Wecom_test", enabled: true}

	// 添加通知器
	if err := manager.AddNotifier(telegramNotifier); err != nil {
		t.Fatalf("添加 telegram 通知器失败: %v", err)
	}
	if err := manager.AddNotifier(emailNotifier); err != nil {
		t.Fatalf("添加 email 通知器失败: %v", err)
	}
	if err := manager.AddNotifier(wecomNotifier); err != nil {
		t.Fatalf("添加 wecom 通知器失败: %v", err)
	}

	// 设置通知规则
	rules := map[string][]string{
		"backup_start":   {"Telegram_test"},
		"backup_success": {"Telegram_test", "Email_test"},
		"backup_failed":  {"Email_test"},
		"device_online":  {"Wecom_test"},
		"device_offline": {}, // 设备离线不发送通知
		"system_error":   {"Email_test", "Wecom_test"},
	}
	manager.SetNotificationRules(rules)

	// 测试备份开始通知（只应该发送给 Telegram）
	telegramNotifier.Reset()
	emailNotifier.Reset()
	wecomNotifier.Reset()

	manager.SendBackupStart("测试设备", "test-udid")
	telegramNotifier.WaitForSends(t, 1)

	if telegramNotifier.Count() != 1 {
		t.Errorf("备份开始通知：期望 Telegram 收到 1 条消息，实际 %d 条", telegramNotifier.Count())
	}
	if emailNotifier.Count() != 0 {
		t.Errorf("备份开始通知：期望 Email 收到 0 条消息，实际 %d 条", emailNotifier.Count())
	}
	if wecomNotifier.Count() != 0 {
		t.Errorf("备份开始通知：期望 Wecom 收到 0 条消息，实际 %d 条", wecomNotifier.Count())
	}

	// 测试备份成功通知（应该发送给 Telegram 和 Email）
	telegramNotifier.Reset()
	emailNotifier.Reset()
	wecomNotifier.Reset()

	manager.SendBackupSuccess("测试设备", "test-udid")
	telegramNotifier.WaitForSends(t, 1)
	emailNotifier.WaitForSends(t, 1)

	if telegramNotifier.Count() != 1 {
		t.Errorf("备份成功通知：期望 Telegram 收到 1 条消息，实际 %d 条", telegramNotifier.Count())
	}
	if emailNotifier.Count() != 1 {
		t.Errorf("备份成功通知：期望 Email 收到 1 条消息，实际 %d 条", emailNotifier.Count())
	}
	if wecomNotifier.Count() != 0 {
		t.Errorf("备份成功通知：期望 Wecom 收到 0 条消息，实际 %d 条", wecomNotifier.Count())
	}

	// 测试备份失败通知（只应该发送给 Email）
	telegramNotifier.Reset()
	emailNotifier.Reset()
	wecomNotifier.Reset()

	manager.SendBackupFailed("测试设备", "test-udid", "测试错误")
	emailNotifier.WaitForSends(t, 1)

	if telegramNotifier.Count() != 0 {
		t.Errorf("备份失败通知：期望 Telegram 收到 0 条消息，实际 %d 条", telegramNotifier.Count())
	}
	if emailNotifier.Count() != 1 {
		t.Errorf("备份失败通知：期望 Email 收到 1 条消息，实际 %d 条", emailNotifier.Count())
	}
	if wecomNotifier.Count() != 0 {
		t.Errorf("备份失败通知：期望 Wecom 收到 0 条消息，实际 %d 条", wecomNotifier.Count())
	}

	// 测试设备离线通知（应该不发送给任何通知器）
	telegramNotifier.Reset()
	emailNotifier.Reset()
	wecomNotifier.Reset()

	manager.SendDeviceOffline("测试设备", "test-udid")

	if telegramNotifier.Count() != 0 {
		t.Errorf("设备离线通知：期望 Telegram 收到 0 条消息，实际 %d 条", telegramNotifier.Count())
	}
	if emailNotifier.Count() != 0 {
		t.Errorf("设备离线通知：期望 Email 收到 0 条消息，实际 %d 条", emailNotifier.Count())
	}
	if wecomNotifier.Count() != 0 {
		t.Errorf("设备离线通知：期望 Wecom 收到 0 条消息，实际 %d 条", wecomNotifier.Count())
	}

	// 测试系统错误通知（应该发送给 Email 和 Wecom）
	telegramNotifier.Reset()
	emailNotifier.Reset()
	wecomNotifier.Reset()

	manager.SendSystemError("测试错误", "这是一个测试系统错误")
	emailNotifier.WaitForSends(t, 1)
	wecomNotifier.WaitForSends(t, 1)

	if telegramNotifier.Count() != 0 {
		t.Errorf("系统错误通知：期望 Telegram 收到 0 条消息，实际 %d 条", telegramNotifier.Count())
	}
	if emailNotifier.Count() != 1 {
		t.Errorf("系统错误通知：期望 Email 收到 1 条消息，实际 %d 条", emailNotifier.Count())
	}
	if wecomNotifier.Count() != 1 {
		t.Errorf("系统错误通知：期望 Wecom 收到 1 条消息，实际 %d 条", wecomNotifier.Count())
	}

	t.Log("通知规则测试通过：所有通知都按照规则正确发送")
}

// TestLoadNotificationConfigDefaults 测试默认配置加载
func TestLoadNotificationConfigDefaults(t *testing.T) {
	// 临时修改配置文件路径，确保读取不存在的文件
	originalPath := notificationConfigFile
	notificationConfigFile = "/tmp/nonexistent_notification_config.json"
	defer func() {
		notificationConfigFile = originalPath
	}()

	// 加载配置（应该返回默认配置）
	config, err := loadNotificationConfig()
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}

	// 验证默认配置的完整性
	if config == nil {
		t.Fatal("配置不应该为nil")
	}

	// 验证基本字段
	if config.Enabled {
		t.Error("默认配置应该禁用通知")
	}

	// 验证数组字段都已初始化且不为nil
	if config.TelegramConfigs == nil {
		t.Error("TelegramConfigs不应该为nil")
	}
	if config.EmailConfigs == nil {
		t.Error("EmailConfigs不应该为nil")
	}
	if config.WecomConfigs == nil {
		t.Error("WecomConfigs不应该为nil")
	}
	if config.WebhookConfigs == nil {
		t.Error("WebhookConfigs不应该为nil")
	}
	if config.NotificationRules == nil {
		t.Error("NotificationRules不应该为nil")
	}

	// 验证数组为空数组而不是nil
	if len(config.TelegramConfigs) != 0 {
		t.Error("默认TelegramConfigs应该为空数组")
	}
	if len(config.EmailConfigs) != 0 {
		t.Error("默认EmailConfigs应该为空数组")
	}
	if len(config.WecomConfigs) != 0 {
		t.Error("默认WecomConfigs应该为空数组")
	}
	if len(config.WebhookConfigs) != 0 {
		t.Error("默认WebhookConfigs应该为空数组")
	}

	// 验证通知规则包含所有必需的类型
	expectedRules := []string{
		string(notificationBackupStart),
		string(notificationBackupSuccess),
		string(notificationBackupFailed),
		string(notificationDeviceOnline),
		string(notificationDeviceOffline),
		string(notificationSystemError),
	}

	for _, ruleType := range expectedRules {
		if _, exists := config.NotificationRules[ruleType]; !exists {
			t.Errorf("缺少通知规则类型: %s", ruleType)
		}
	}

	t.Log("默认配置测试通过：所有字段都正确初始化")
}

// TestDefaultConfigJSONSerialization 测试默认配置的JSON序列化
func TestDefaultConfigJSONSerialization(t *testing.T) {
	// 获取默认配置
	originalPath := notificationConfigFile
	notificationConfigFile = "/tmp/nonexistent_notification_config.json"
	defer func() {
		notificationConfigFile = originalPath
	}()

	config, err := loadNotificationConfig()
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}

	// 序列化为JSON
	jsonData, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("JSON序列化失败: %v", err)
	}

	// 反序列化验证
	var parsedConfig map[string]interface{}
	if err := json.Unmarshal(jsonData, &parsedConfig); err != nil {
		t.Fatalf("JSON反序列化失败: %v", err)
	}

	// 验证关键字段存在且不为null
	checkField := func(fieldName string) {
		value, exists := parsedConfig[fieldName]
		if !exists {
			t.Errorf("JSON中缺少字段: %s", fieldName)
			return
		}
		if value == nil {
			t.Errorf("字段 %s 不应该为null", fieldName)
		}
	}

	checkField("enabled")
	checkField("telegram_configs")
	checkField("email_configs")
	checkField("wecom_configs")
	checkField("webhook_configs")
	checkField("notification_rules")

	// 验证数组字段是空数组而不是null
	checkArray := func(fieldName string) {
		value, exists := parsedConfig[fieldName]
		if !exists {
			t.Errorf("JSON中缺少数组字段: %s", fieldName)
			return
		}

		arr, ok := value.([]interface{})
		if !ok {
			t.Errorf("字段 %s 应该是数组类型", fieldName)
			return
		}

		if len(arr) != 0 {
			t.Errorf("默认配置中字段 %s 应该是空数组", fieldName)
		}
	}

	checkArray("telegram_configs")
	checkArray("email_configs")
	checkArray("wecom_configs")
	checkArray("webhook_configs")

	// 打印JSON以便调试
	t.Logf("序列化后的JSON: %s", string(jsonData))
	t.Log("JSON序列化测试通过：所有字段都正确序列化")
}
