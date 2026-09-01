# iOS备份通知系统

本项目集成了灵活的通知系统，支持多种通知渠道，可以在备份完成、失败或设备状态变化时发送通知。

## 支持的通知渠道

### 1. Telegram Bot
通过Telegram Bot发送通知消息。

**配置参数：**
- `bot_token`: Bot Token（从 @BotFather 获取）
- `chat_id`: 聊天ID（个人聊天或群组ID）

**获取Bot Token：**
1. 与 @BotFather 对话
2. 发送 `/newbot` 创建新Bot
3. 设置Bot名称
4. 获取Bot Token

**获取Chat ID：**
1. 将Bot添加到群组或直接与Bot对话
2. 发送一条消息给Bot
3. 访问 `https://api.telegram.org/bot<BOT_TOKEN>/getUpdates`
4. 在返回的JSON中找到 `chat.id`

### 2. 邮件通知
通过SMTP发送邮件通知。

**支持的邮件服务：**
- Gmail (smtp.gmail.com:587)
- QQ邮箱 (smtp.qq.com:587) 
- 163邮箱 (smtp.163.com:587)
- 企业邮箱等

**配置参数：**
- `smtp_host`: SMTP服务器地址
- `smtp_port`: SMTP端口（通常为587或465）
- `username`: 登录用户名
- `password`: 登录密码（Gmail建议使用App Password）
- `from`: 发件人邮箱
- `to`: 收件人邮箱

### 3. 企业微信群机器人
通过企业微信群机器人发送通知。

**配置参数：**
- `webhook_url`: 机器人Webhook URL

**获取Webhook URL：**
1. 在企业微信群中添加机器人
2. 复制生成的Webhook URL

### 4. 自定义Webhook
发送JSON格式的通知到自定义的HTTP端点。

**配置参数：**
- `url`: Webhook URL
- `method`: HTTP方法（默认POST）
- `headers`: 自定义HTTP头

### 5. Bark
通过 Bark HTTP API 推送到 iPhone。

**配置参数：**
- `server_url`: Bark 服务地址，官方服务通常为 `https://api.day.app`，自建 Bark 使用自建服务地址
- `device_key`: Bark 设备 Key（保存到加密秘密存储，不会通过 GET 配置接口返回）

Bark 通知器完整名称格式为 `Bark_<name>`。例如配置名为 `phone`，规则中写 `Bark_phone`。

## 配置文件

通知配置保存在 `/configs/notification_configs.json` 文件中。可以参考 `notification_config_example.json` 示例文件。

### 配置结构说明

```json
{
  "enabled": true,                    // 是否启用通知系统
  "telegram_configs": [...],          // Telegram配置列表
  "email_configs": [...],             // 邮件配置列表  
  "wecom_configs": [...],             // 企业微信配置列表
  "bark_configs": [...],              // Bark配置列表
  "webhook_configs": [...],           // Webhook配置列表
  "notification_rules": {             // 通知规则
    "backup_start": ["Telegram_bot1"], // 备份开始时使用的通知器
    "backup_success": ["Telegram_bot1"], // 备份成功时使用的通知器
    "backup_failed": ["Telegram_bot1", "Email_email1"], // 备份失败时使用的通知器
    "device_online": [],              // 设备上线通知
    "device_offline": [],             // 设备离线通知  
    "system_error": ["Telegram_bot1"] // 系统错误通知
  }
}
```

### 自定义通知模板

可以按事件类型修改通知正文模板。未配置模板时使用内建默认模板，不影响旧配置。

支持的变量：

- `${title}`：通知标题
- `${content}`：内建通知正文
- `${device_name}`：设备名称
- `${device_udid}`：设备 UDID
- `${timestamp}`：北京时间，RFC3339 格式
- `${type}`：事件类型
- `${level}`：通知级别
- `${reason}`：备份失败原因

示例：

```json
{
  "notification_templates": {
    "backup_success": "[${type}] ${device_name} 已完成备份\n时间：${timestamp}",
    "backup_failed": "${title}\n设备：${device_name}\n原因：${reason}",
    "system_error": "${title}\n${content}"
  }
}
```

模板应用于所有通知渠道。Webhook 的 JSON 结构仍会保留 `type`、`device_name`、`device_udid` 等字段，模板内容写入 `content`。

模板变量必须使用 `${变量名}` 格式；未知变量会在保存配置时被拒绝。模板为空时恢复该事件的内建默认模板。

## API接口

### 获取通知配置
```bash
curl -X GET http://localhost:8080/api/notifications/config
```

### 保存通知配置
```bash
curl -X POST http://localhost:8080/api/notifications/config \
  -H "Content-Type: application/json" \
  -d @notification_configs.json
```

### 查看通知状态
```bash
curl -X GET http://localhost:8080/api/notifications/status
```

### 测试通知
```bash
curl -X POST http://localhost:8080/api/notifications/test \
  -H "Content-Type: application/json" \
  -d '{"message_type": "backup_success"}'
```

支持的测试消息类型：
- `backup_start`: 备份开始
- `backup_success`: 备份成功
- `backup_failed`: 备份失败
- `device_online`: 设备上线
- `device_offline`: 设备离线
- `system_error`: 系统错误

## 通知触发时机

### 自动触发
- **备份开始**: 备份条件检查通过、即将执行备份命令时
- **备份成功**: 手动或自动备份完成时
- **备份失败**: 备份命令、备份目录或备份准备阶段出错时
- **设备上线**: 设备从离线变为在线时
- **设备离线**: 设备从在线变为离线时；Wi-Fi 设备经过离线宽限期后触发
- **系统错误**: 系统通过统一错误日志入口记录错误时

### 手动测试
通过API接口可以手动发送测试通知。

## 使用示例

### 1. 配置Telegram通知

1. 创建Bot并获取Token
2. 获取Chat ID  
3. 修改配置文件：

```json
{
  "enabled": true,
  "telegram_configs": [
    {
      "name": "backup_bot",
      "bot_token": "123456789:ABCDEF...",
      "chat_id": "-123456789",
      "enabled": true
    }
  ],
  "notification_rules": {
    "backup_start": ["Telegram_backup_bot"],
    "backup_success": ["Telegram_backup_bot"],
    "backup_failed": ["Telegram_backup_bot"]
  }
}
```

### 2. 配置邮件通知

```json
{
  "enabled": true,
  "email_configs": [
    {
      "name": "gmail_notify",
      "smtp_host": "smtp.gmail.com",
      "smtp_port": 587,
      "username": "your_email@gmail.com", 
      "password": "your_app_password",
      "from": "your_email@gmail.com",
      "to": "admin@example.com",
      "enabled": true
    }
  ],
  "notification_rules": {
    "backup_failed": ["gmail_notify"],
    "system_error": ["gmail_notify"]
  }
}
```

### 3. 配置企业微信通知

```json
{
  "enabled": true,
  "wecom_configs": [
    {
      "name": "work_group",
      "webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=your_key",
      "enabled": true
    }
  ],
  "notification_rules": {
    "backup_success": ["Wecom_work_group"],
    "backup_failed": ["Wecom_work_group"]
  }
}
```

## 故障排除

### 常见问题

1. **Telegram通知失败**
   - 检查Bot Token是否正确
   - 检查Chat ID是否正确
   - 确保Bot已添加到群组（如果是群组通知）

2. **邮件通知失败**
   - 检查SMTP配置是否正确
   - Gmail需要使用App Password而非账户密码
   - 检查防火墙是否阻止SMTP连接

3. **企业微信通知失败**
   - 检查Webhook URL是否有效
   - 确认机器人权限设置正确

### 日志查看

通知发送状态会记录在系统日志中：
```bash
# 查看Docker容器日志
docker logs iosbackup_container

# 或直接运行时查看控制台输出
```

## 安全建议

1. **保护敏感信息**
   - 不要在代码中硬编码Token或密码
   - 使用环境变量或安全的配置文件
   - 定期轮换Token和密码

2. **网络安全**
   - 使用HTTPS Webhook端点
   - 在防火墙中限制不必要的出站连接

3. **权限控制**
   - 限制Bot权限
   - 使用专用的邮箱账户进行通知
   - 定期审查通知配置

## 扩展开发

如需添加新的通知渠道，可以：

1. 实现 `Notifier` 接口
2. 添加对应的配置结构
3. 在 `InitNotificationManager` 中添加初始化逻辑
4. 更新配置文件格式

参考现有的实现代码：
- `notification.go`: 核心接口和管理器
- `notifiers.go`: 具体通知器实现
- `routes.go`: API接口处理 