# 通知配置指南

[返回中文 README](../README.zh-CN.md)

iOS Backup 可以在备份开始、备份成功、备份失败、设备上线、设备离线和系统错误时发送通知。通知是 best-effort：单个渠道发送失败会写入容器日志，但不会中断备份。

## 1. 使用前检查

通知密钥会使用 `IOSBK_SECRET_KEY` 加密后保存到 `/configs`。保存过任何通知密钥后，必须保持这个环境变量不变；丢失或更换密钥会导致已有秘密不可读取。

只在可信网络中打开 `http://<Linux主机>:9000/notifications`。如果需要跨网络访问，请使用 VPN 或带认证的 HTTPS 反向代理，不要把 Web UI 直接暴露到公网。

## 2. 支持的渠道

| 渠道 | 必填信息 | 备注 |
|---|---|---|
| Telegram | Bot Token、Chat ID | 从 BotFather 创建机器人；先向机器人发送一条消息再读取 Chat ID |
| 邮件 | SMTP 主机、端口、用户名、密码、发件人与收件人 | 建议使用应用专用密码，不要使用主账号密码 |
| 企业微信 | 群机器人 Webhook URL | 机器人权限应保持最小化 |
| Bark | 服务地址、Device Key | 官方服务地址通常是 `https://api.day.app` |
| Webhook | HTTPS URL、方法和可选 Header | 内网目标还需要配置 `IOSBK_WEBHOOK_ALLOW_CIDRS` |

## 3. 在 Web UI 中配置

1. 登录 iOS Backup，打开“通知设置”。
2. 打开“启用通知”。
3. 添加一个通知渠道，填写名称和凭据，并启用该渠道。
4. 在“通知规则”中勾选每种事件需要使用的渠道。
5. 点击“保存”。页面出现“已保存”后再继续。
6. 在“测试”区域选择事件，点击“发送测试通知”，到目标渠道确认收到消息。

通知规则使用由界面生成的完整通知器名称，例如 `Telegram_backup_bot`、`Email_ops`、`Wecom_team`、`Bark_phone` 或 `Webhook_receiver`。通过 Web UI 配置时无需手工拼接名称。

## 4. 自定义消息模板

每种事件都可以单独设置正文模板。留空时使用内建默认模板。支持以下变量：

| 变量 | 内容 |
|---|---|
| `${title}` | 内建标题 |
| `${content}` | 内建正文 |
| `${device_name}` | 设备名称 |
| `${device_udid}` | 设备标识 |
| `${timestamp}` | 北京时间，RFC 3339 格式 |
| `${type}` | 事件类型 |
| `${level}` | 日志级别 |
| `${reason}` | 备份失败原因 |

示例：

```text
[${type}] ${device_name} 已完成备份
时间：${timestamp}
```

未知变量会在保存时被拒绝。模板应用于所有通知渠道；Webhook 的其他结构化字段不会被移除。

## 5. 只读 API 检查

默认端口是 `9000`。以下命令由 curl 在终端中提示输入管理员密码，密码不会出现在命令历史中：

```bash
curl --user iosbackup http://127.0.0.1:9000/api/notifications/status
curl --user iosbackup http://127.0.0.1:9000/api/notifications/config
```

配置保存和测试通知属于状态变更操作，除身份认证外还必须提供当前安装生成的 CSRF token。同一 token 不应写进脚本或文档，因此请通过通知设置页面执行这两项操作。

GET 配置接口只返回秘密是否已经配置，不会返回 Bot Token、SMTP 密码、Webhook URL、授权 Header 或 Device Key。编辑时秘密输入框留空表示保留原值；需要替换时填写新值并保存，需要移除时使用界面中的清除操作。

## 6. 触发时机

- `backup_start`：备份条件检查通过，即将启动备份命令。
- `backup_success`：手动或自动备份成功完成。
- `backup_failed`：已启动的备份命令返回失败。
- `device_online`：设备从离线变为在线。
- `device_offline`：设备经过离线宽限期后仍不可见。
- `system_error`：应用通过统一错误日志入口记录系统错误。

## 7. 排错

查看最近日志：

```bash
docker compose logs --tail=200 iosbackup
```

- 没有任何渠道收到消息：确认总开关、具体渠道和对应事件规则都已启用。
- 测试通知成功但备份没有通知：确认规则选择的是对应事件；`backup_start` 只在备份条件通过后触发。
- Telegram 失败：确认机器人已经收到过消息，且 Chat ID 与 Bot Token 属于同一机器人。
- 邮件失败：检查 SMTP 端口、TLS 要求和应用专用密码，并确认宿主机允许出站 SMTP。
- 企业微信或 Bark 失败：重新生成密钥，并确认服务地址可从容器所在网络访问。
- Webhook 返回 401/403：检查接收端授权 Header；不要记录完整授权值。
- 内网 Webhook 被拒绝：仅在确认目标可信后，将最小必要网段加入 `IOSBK_WEBHOOK_ALLOW_CIDRS`。

自定义 Webhook 的请求格式、安全限制和接收端示例见 [Webhook 推送指南](WEBHOOK_GUIDE.md)。
