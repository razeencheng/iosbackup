# 通知系统单元测试指南

## 概述

本文档描述了iOS备份通知系统的单元测试设计和运行方法。测试覆盖了通知管理器、各种通知器实现以及所有通知发送场景。

## 测试文件结构

```
iosbackup/
├── notification_test.go      # 通知管理器核心功能测试
├── notifiers_test.go        # 具体通知器实现测试
├── notifiers_integration_test.go # 显式 opt-in 的真实通知测试
├── scripts/run_tests.sh     # 默认离线测试脚本
└── docs/TESTING_GUIDE.md    # 本测试指南
```

## 测试覆盖范围

### 1. 通知管理器测试 (`notification_test.go`)

#### 核心功能测试
- ✅ **通知管理器创建和初始化**
- ✅ **通知器添加和管理**
- ✅ **通知消息发送和路由**
- ✅ **启用/禁用状态管理**
- ✅ **并发安全性测试**

#### 通知场景测试
- ✅ **备份开始通知** (`SendBackupStart`)
- ✅ **备份成功通知** (`SendBackupSuccess`) 
- ✅ **备份失败通知** (`SendBackupFailed`)
- ✅ **设备上线通知** (`SendDeviceOnline`)
- ✅ **设备离线通知** (`SendDeviceOffline`)
- ✅ **系统错误通知** (`SendSystemError`)

#### 错误处理测试
- ✅ **禁用通知器处理**
- ✅ **通知器发送错误处理**
- ✅ **无效通知器验证**
- ✅ **管理器禁用状态处理**

#### 性能测试
- ✅ **并发发送测试**
- ✅ **基准性能测试**

### 2. 通知器实现测试 (`notifiers_test.go`)

#### Telegram通知器测试
- ✅ **配置验证** (Bot Token, Chat ID)
- ✅ **消息格式验证** (Markdown格式)
- ✅ **HTTP请求验证** (API调用)
- ✅ **错误场景处理**

#### 邮件通知器测试
- ✅ **SMTP配置验证** (主机、端口、认证)
- ✅ **邮件格式验证** (主题、内容)
- ✅ **多种SMTP服务支持** (Gmail、QQ、企业邮箱)
- ✅ **参数验证测试**

#### 企业微信通知器测试
- ✅ **Webhook URL验证** (HTTPS要求)
- ✅ **消息格式验证** (企业微信格式)
- ✅ **HTTP请求验证** (POST请求)
- ✅ **实际发送测试** (Mock服务器)

#### Webhook通知器测试
- ✅ **URL格式验证** (HTTP/HTTPS)
- ✅ **自定义HTTP头验证**
- ✅ **JSON消息格式验证**
- ✅ **完整消息传输测试**

### 3. 配置管理测试

- ✅ **配置序列化/反序列化**
- ✅ **配置验证逻辑**
- ✅ **通知规则管理**

## 测试运行方法

### 1. 快速运行所有测试

```bash
# 使用测试脚本（推荐）
./scripts/run_tests.sh

# 或者手动运行
go test -v ./...
```

默认测试不会编译或运行真实通知测试，也不应读取真实 `/configs`。`scripts/run_tests.sh` 会先检查默认测试清单；如果出现 `TestRealNotice`，脚本立即失败。

### 2. 运行真实通知集成测试

真实通知会向外部服务发送消息，只能在隔离环境中双重显式启用：

```bash
IOSBK_RUN_REAL_NOTIFICATIONS=YES_I_KNOW_THIS_SENDS_MESSAGES \
IOSBK_INTEGRATION_NOTIFICATION_CONFIG=/absolute/path/to/test-notifications.json \
go test -tags=integration -run '^TestRealNotice$' -count=1 .
```

配置路径必须显式提供，测试不会从 `dirConfigs`、`/configs` 或 package 初始化路径猜测配置。只提供 build tag 而没有确认变量时，测试会 SKIP。

### 3. 运行特定测试

```bash
# 运行通知管理器测试
go test -v -run "Test.*Notification" .

# 运行通知器实现测试  
go test -v -run "Test.*Notifier" .

# 运行配置相关测试
go test -v -run "Test.*Config" .
```

### 4. 运行覆盖率测试

```bash
# 生成覆盖率报告
go test -v -coverprofile=coverage.out .
go tool cover -html=coverage.out -o coverage.html

# 查看覆盖率摘要
go tool cover -func=coverage.out
```

### 5. 运行基准测试

```bash
# 运行性能基准测试
go test -v -bench=. -run=^$ .

# 运行特定基准测试
go test -v -bench=BenchmarkNotification .
```

## 测试设计理念

### 1. Mock对象设计

使用 `MockNotifier` 模拟通知器行为：
- 记录发送次数和最后消息
- 支持模拟发送错误
- 可配置启用/禁用状态
- 简单的验证逻辑

### 2. HTTP服务器Mock

使用 `httptest.NewServer` 模拟外部API：
- Telegram Bot API
- 企业微信Webhook
- 自定义Webhook端点

### 3. 异步处理测试

通知系统使用goroutine异步发送：
- 使用有界结果 channel 等待精确发送数量
- 验证异步操作结果
- 测试并发安全性

### 4. 错误场景覆盖

全面测试错误处理：
- 网络连接错误
- 无效配置参数
- 权限认证失败
- 服务器响应错误

## 测试数据和场景

### 1. 测试消息样例

```go
message := &NotificationMessage{
    Type:       NotificationBackupSuccess,
    Level:      NotificationLevelInfo,
    Title:      "✅ 设备备份完成",
    Content:    "设备 iPhone 15 备份成功完成",
    DeviceName: "iPhone 15",
    DeviceUDID: "12345-abcde",
    Timestamp:  nowBeijing(),
}
```

### 2. 配置测试数据

```go
config := &NotificationConfig{
    Enabled: true,
    TelegramConfigs: []TelegramConfig{
        {
            Name:     "test_bot",
            BotToken: "test_token",
            ChatID:   "test_chat_id",
            Enabled:  true,
        },
    },
    NotificationRules: map[string][]string{
        "backup_success": {"test_bot"},
        "backup_failed":  {"test_bot", "email"},
    },
}
```

## 测试最佳实践

### 1. 测试隔离
- 每个测试函数独立运行
- 使用临时配置避免干扰
- Mock外部依赖

### 2. 清晰的测试命名
- 使用描述性测试名称
- 遵循 `Test<功能><场景>` 命名规范
- 子测试使用 `t.Run()` 组织

### 3. 完整的断言
- 验证返回值和状态变化
- 检查错误处理逻辑
- 确认副作用（如日志记录）

### 4. 性能考虑
- 基准测试衡量性能
- 并发测试验证线程安全
- 避免测试中的实际网络请求

## 常见问题和解决方案

### 1. 测试超时
**问题**: 异步通知测试偶尔超时
**解决**: 检查生产代码是否完成发送，并使用有界同步机制；不要增加固定 `time.Sleep`

### 2. Mock服务器端口冲突
**问题**: `httptest.NewServer` 端口被占用
**解决**: 使用 `defer server.Close()` 确保清理

### 3. 配置文件冲突  
**问题**: 测试修改全局配置常量
**解决**: 测试配置序列化而非文件操作

### 4. 并发竞争条件
**问题**: 并发测试出现竞争条件
**解决**: 使用适当的同步机制和等待策略

## 测试维护指南

### 1. 添加新通知器测试
1. 在 `notifiers_test.go` 中添加测试函数
2. 实现配置验证测试
3. 添加消息发送测试
4. 包含错误处理测试

### 2. 添加新通知场景测试
1. 在 `notification_test.go` 中添加场景测试
2. 验证消息格式和内容
3. 测试通知规则路由
4. 确保错误处理覆盖

### 3. 更新基准测试
1. 新功能添加对应基准测试
2. 定期运行性能回归测试
3. 记录性能基线数据

## 持续集成建议

```yaml
# GitHub Actions 示例
- name: Run notification tests
  run: |
    cd iosbackup
    go test -v -coverprofile=coverage.out .
    go tool cover -func=coverage.out
```

通过完善的单元测试，确保通知系统的可靠性和维护性。
