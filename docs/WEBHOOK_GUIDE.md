# Webhook 请求与接收端参考

[English](WEBHOOK_GUIDE.en.md) · [通知手册](manual/notifications.zh-CN.md)

本页描述当前实现的请求格式及限制，不会替你部署接收服务。Webhook 向外发送 JSON 通知，接收服务必须能从容器所在网络访问。备份流程不会等待真实事件投递，也不会因投递失败而中断。

## 请求契约

方法支持 `POST`、`PUT`、`PATCH`，留空为 `POST`。默认 `Content-Type: application/json`。接收端返回任意 `2xx` 表示此次请求成功；这只表示接收端接受了请求，不证明后续业务处理完成。客户端请求总超时为 10 秒，待发送消息不会持久保存，也不保证失败后重试。

```json
{
  "type": "backup_failed",
  "level": "error",
  "title": "备份失败",
  "content": "测试设备备份失败：空间不足",
  "device_name": "Test iPhone",
  "device_udid": "EXAMPLE-DEVICE-ID",
  "timestamp": "2026-10-06T12:34:56+08:00",
  "extra_fields": {"reason": "空间不足"}
}
```

这是使用虚构数据的示例。事件类型见[通知手册的事件参考](manual/notifications.zh-CN.md#事件参考)。级别为 `info`、`warning` 或 `error`；无设备的系统事件可省略设备字段，`extra_fields` 也可缺省。接收端应允许新增字段，不依赖中文标题的固定措辞。正文模板只改变 `content`，不移除其他字段。

## URL、方法与 Header 字段

常规配置和测试按[通知手册](manual/notifications.zh-CN.md)完成。当前 UI 没有自定义 Header 编辑器；需要 Header 的集成应由熟悉认证/CSRF 的管理员通过配置 API 管理。

下面是 API 字段示意，不是可直接发送的完整配置。`POST /api/notifications/config` 保存的是整个通知配置，不能把片段当局部补丁，否则可能移除其他渠道或规则：

```json
{
  "webhook_configs": [{
    "name": "backup_receiver",
    "url": "https://notify.example.com/iosbackup",
    "method": "POST",
    "headers": {"Authorization": "Bearer EXAMPLE_RECEIVER_TOKEN"},
    "enabled": true,
    "replace_secret": true
  }],
  "notification_rules": {
    "backup_success": ["Webhook_backup_receiver"],
    "backup_failed": ["Webhook_backup_receiver"]
  }
}
```

从 GET 的完整配置保留其他字段和渠道；`replace_secret` 和 `clear_secret` 均为 `false` 时，秘密字段留空可保留原值。设置 `replace_secret: true` 时，URL 和完整 Header 必须一起提供。通过 UI 单独改 URL 会清除先前由 API 设置的 Header；带 Header 的渠道应通过 API 一起更新并测试。写请求需要当前认证及页面 `iosbk-csrf` meta 值对应的 `X-IOSBK-CSRF` 请求头。只读检查及响应含义见[通知手册](manual/notifications.zh-CN.md#秘密存储与-api-检查)；不要将真实凭据、token 或请求另存为公共示例。

Header 最多 20 个、名称和值合计不超过 8 KiB。值不能包含 CR/LF；`Host`、`Content-Length`、`Connection`、代理鉴权及其他逐跳 Header 不允许自定义。URL 不接受 `user:password@host` 形式。

## 网络限制与私网接收端

公网通知 URL 要求 HTTPS。私网 HTTPS 目标仍需 `IOSBK_WEBHOOK_ALLOW_CIDRS`；HTTP 仅接受明确的私网 IP 字面量，不接受 HTTP 主机名，即使 DNS 最后解析为私网地址。回环、未指定、组播、链路本地地址禁止，不能通过放大 CIDR 放行。DNS 解析后的连接地址也会检查。Bark 自建服务使用同样的允许网段；它不会放行 Telegram 或企业微信的私网目标。

例如接收服务确实在你管理的 `192.168.50.10` 时，可在 Compose 的应用环境中配置单主机范围：

```yaml
services:
  iosbackup:
    environment:
      IOSBK_WEBHOOK_ALLOW_CIDRS: "192.168.50.10/32"
```

这是示例私网地址，应换成实际服务 IP；优先使用有效证书的 HTTPS。按[配置生效步骤](manual/configuration.zh-CN.md)合并片段并重建容器，直接在 Compose 的 `environment` 中设置此项。HTTP 客户端不使用 `HTTP_PROXY`/`HTTPS_PROXY` 环境代理；防火墙和路由必须允许直接访问。

## 接收端最小要求与验收

1. 建立专用接口，检查方法、内容类型、正文大小和支持的事件类型。
2. 以 HTTPS 保护传输，验证随机授权 token 或接收服务提供的等效凭据；不要只依靠 URL 难猜。
3. 接收后迅速返回 `2xx`，耗时工作进入接收端自己的队列。不要假设事件恰好一次送达，必要时自行去重。
4. 只记录诊断所需字段，隐藏授权头、完整 UDID、用户文件信息及 URL 中的秘密。
5. 按[通知手册的验证步骤](manual/notifications.zh-CN.md#测试送达再验证真实事件)完成测试及真实事件验证，同时核对接收端实际收到的请求。

接收端排障：`401/403` 先查鉴权，超时先查容器是否能直接访问接收端，私网拒绝先查 CIDR。不要反复重建备份实例来调试接收端。规则、开关及真实事件缺失的排查见[通知手册的失败分支](manual/notifications.zh-CN.md#收不到消息时)。
