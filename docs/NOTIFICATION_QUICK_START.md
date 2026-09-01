# 通知系统快速开始

## 概述

本iOS备份系统集成了完整的通知功能，支持多种通知渠道，可以在备份完成、失败时自动发送通知。

## 主要特性

- 🤖 **Telegram Bot通知** - 即时消息推送
- 📧 **邮件通知** - 支持各种邮箱服务
- 💬 **企业微信通知** - 群机器人推送
- 🔗 **自定义Webhook** - 集成任何HTTP服务
- ⚙️ **灵活配置** - 支持多通知器和规则配置

## 快速配置Telegram通知

### 1. 创建Telegram Bot
1. 在Telegram中与 @BotFather 对话
2. 发送 `/newbot` 创建新Bot
3. 设置Bot名称，获取Bot Token

### 2. 获取Chat ID
1. 将Bot添加到群组或直接对话
2. 发送任意消息
3. 访问 `https://api.telegram.org/bot<BOT_TOKEN>/getUpdates`
4. 从返回JSON中获取 `chat.id`

### 3. 配置文件
创建 `/configs/notification_configs.json`：
```json
{
  "enabled": true,
  "telegram_configs": [
    {
      "name": "backup_bot",
      "bot_token": "你的Bot Token",
      "chat_id": "你的Chat ID",
      "enabled": true
    }
  ],
  "notification_rules": {
    "backup_success": ["backup_bot"],
    "backup_failed": ["backup_bot"]
  }
}
```

## 测试通知

```bash
# 测试通知功能
curl -X POST http://localhost:8080/api/notifications/test \
  -H "Content-Type: application/json" \
  -d '{"message_type": "backup_success"}'

# 查看通知器状态
curl -X GET http://localhost:8080/api/notifications/status
```

## 通知触发场景

- ✅ **备份成功** - 手动或自动备份完成
- ❌ **备份失败** - 备份过程出现错误
- 📱 **设备状态** - 设备上线/离线（可选）
- ⚠️ **系统错误** - 系统异常情况

## 更多配置选项

参考 `NOTIFICATION_README.md` 了解：
- 邮件通知配置
- 企业微信通知设置
- 自定义Webhook集成
- 高级配置选项
- 故障排除指南 