# Webhook 推送指导

[返回通知配置指南](NOTIFICATION_README.md)

## 1. 工作方式

iOS Backup 会在通知事件发生时，向已启用的 Webhook 发送 HTTP 请求。请求体是完整的 JSON 通知消息，发送方式为异步 best-effort：业务流程不会等待 Webhook 完成，也不会因为 Webhook 失败而中断备份。

支持的事件：

- `backup_start`：备份命令准备执行
- `backup_success`：备份完成且命令返回成功
- `backup_failed`：备份命令已经启动后返回失败
- `device_online`：设备从离线变为在线
- `device_offline`：设备从在线变为离线
- `system_error`：应用通过统一错误日志入口记录错误

## 2. Webhook 请求格式

默认请求方法是 `POST`，请求头包含：

```http
Content-Type: application/json
```

请求体示例：

```json
{
  "type": "backup_success",
  "level": "info",
  "title": "✅ 设备备份完成",
  "content": "设备 iPhone 15 备份成功完成",
  "device_name": "iPhone 15",
  "device_udid": "EXAMPLE-DEVICE-ID",
  "timestamp": "2026-08-16T12:34:56+08:00"
}
```

失败事件会额外包含 `extra_fields.reason`：

```json
{
  "type": "backup_failed",
  "level": "error",
  "title": "❌ 设备备份失败",
  "content": "设备 iPhone 15 备份失败\n原因: 磁盘空间不足",
  "device_name": "iPhone 15",
  "device_udid": "EXAMPLE-DEVICE-ID",
  "timestamp": "2026-08-16T12:34:56+08:00",
  "extra_fields": {
    "reason": "磁盘空间不足"
  }
}
```

接收端应返回任意 `2xx` 状态码表示成功。非 `2xx` 会记录为通知发送失败。

### 自定义正文

Webhook 的 `content` 字段支持按事件配置的模板。可用变量包括 `${title}`、`${content}`、`${device_name}`、`${device_udid}`、`${timestamp}`、`${type}`、`${level}` 和 `${reason}`。例如：

```json
{
  "notification_templates": {
    "backup_success": "[${type}] ${device_name} 备份完成，时间 ${timestamp}",
    "backup_failed": "${title}\n设备：${device_name}\n原因：${reason}"
  }
}
```

模板应用于所有通知渠道；对 Webhook 来说，渲染后的内容写入 `content`，其他结构化字段不会被删除。

## 3. 配置方式

推荐通过通知设置页面配置。也可以使用配置 API。下面的例子假设应用运行在 `<Linux主机>`，使用默认端口 `9000`，并且管理员认证已启用：

```bash
curl -i -u 'iosbackup:YOUR_ADMIN_PASSWORD' \
  -X GET 'http://<Linux主机>:9000/api/notifications/config'
```

POST 保存配置需要 CSRF 请求头。先从登录后的浏览器会话取得应用页面中的 `iosbk-csrf` meta 值，或者直接在通知设置页面保存配置。不要把管理员密码、CSRF token、Webhook 密钥提交到 Git。

配置字段：

```json
{
  "enabled": true,
  "webhook_configs": [
    {
      "name": "backup_receiver",
      "url": "https://notify.example.com/iosbackup",
      "method": "POST",
      "headers": {
        "Authorization": "Bearer YOUR_RECEIVER_TOKEN"
      },
      "enabled": true,
      "replace_secret": true
    }
  ],
  "notification_rules": {
    "backup_start": ["Webhook_backup_receiver"],
    "backup_success": ["Webhook_backup_receiver"],
    "backup_failed": ["Webhook_backup_receiver"],
    "device_online": [],
    "device_offline": [],
    "system_error": ["Webhook_backup_receiver"]
  }
}
```

注意：通知规则使用通知器完整名称，而不是配置里的短名称。Webhook 的完整名称格式是：

```text
Webhook_<name>
```

例如配置名是 `backup_receiver`，规则中必须写 `Webhook_backup_receiver`。

Webhook URL 和自定义 Header 会进入加密秘密存储。运行时应配置 `IOSBK_SECRET_KEY`，否则不能保存启用的秘密配置。

## 4. 接收端示例

### Node.js / Express

```js
import express from "express";

const app = express();
app.use(express.json({ limit: "64kb" }));

app.post("/iosbackup", (req, res) => {
  const event = req.body;
  console.log(`[${event.type}] ${event.title}: ${event.content}`);
  res.sendStatus(204);
});

app.listen(3000, "127.0.0.1");
```

### Go

```go
http.HandleFunc("/iosbackup", func(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    defer r.Body.Close()
    var event struct {
        Type    string `json:"type"`
        Title   string `json:"title"`
        Content string `json:"content"`
    }
    if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&event); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }
    log.Printf("[%s] %s: %s", event.Type, event.Title, event.Content)
    w.WriteHeader(http.StatusNoContent)
})
```

生产环境建议：

- 使用 HTTPS。
- 使用随机 Bearer token，并在接收端校验 `Authorization`。
- 限制请求体大小，拒绝不支持的事件类型。
- 记录请求 ID、事件类型和接收结果，但不要记录完整的授权 Header。
- 接收端先快速返回 `2xx`，耗时任务放入自己的队列。

## 5. 远程验证

在 Linux 宿主机上先检查应用看到的通知器和规则：

```bash
curl -sS -u 'iosbackup:YOUR_ADMIN_PASSWORD' \
  'http://127.0.0.1:9000/api/notifications/status'

curl -sS -u 'iosbackup:YOUR_ADMIN_PASSWORD' \
  'http://127.0.0.1:9000/api/notifications/config'
```

然后在通知设置页面逐项测试六种事件。测试接口支持：

```text
backup_start
backup_success
backup_failed
device_online
device_offline
system_error
```

如果测试成功但真实事件没有到达，查看应用日志中的以下信息：

```text
消息类型 ... 没有配置通知规则或规则为空，跳过发送
通知器 ... 发送成功
通知器 ... 发送失败
```

## 6. 常见问题

### 测试成功，真实备份没有通知

检查对应事件的规则是否选择了完整通知器名称，尤其是 `backup_start`。真实备份开始通知只会在备份条件通过并即将执行备份命令时触发；备份目录或命令启动阶段的错误目前只记录日志，不一定进入 `backup_failed` 通知路径。

### Webhook 返回 401 或 403

检查接收端要求的 Header 名称和值。Header 值不能包含换行，`Host`、`Content-Length` 等受保护 Header 不能自定义。

### 内网 Webhook 不发送

出于 SSRF 防护，公网 URL 必须使用 HTTPS，内网目标必须配置在 `IOSBK_WEBHOOK_ALLOW_CIDRS` 中。仅在确认目标可信时加入对应网段。

### 看不到秘密

GET 配置接口只返回 `configured: true`，不会返回 URL、Header 或 token。编辑时秘密字段留空表示保留旧值；需要替换时填写新值并保存，需要删除时使用“清除密钥”。
