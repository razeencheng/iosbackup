# Privacy

## Local-first boundary

iOS Backup is a local-first, single-administrator tool. The Web UI, embedded static assets, device discovery, configuration, and backup processing run on the deployment host. The project includes no telemetry, advertising, analytics SDK, remote font, or remote frontend asset.

The project has no telemetry and does not automatically upload data to the maintainer. Notification channels explicitly configured by the administrator—Telegram, SMTP email, WeCom, Bark, or a generic webhook—are the only designed Internet-facing third-party integrations. A notification test sends a real message, and those providers receive the configured content and metadata. When Wi-Fi features are enabled, netmuxd also discovers devices on the local network (including through mDNS) and connects to reachable iOS devices; that local device traffic is not telemetry or a maintainer upload.

Device identifiers/names, battery state, pairing records, backup contents, and application logs stay on the deployment host unless the administrator exports or sends them. The maintainer does not receive this data automatically.

Notification configuration GET responses expose only configured status, not stored secret values. Notification secrets and stored backup passwords are encrypted in `/configs/secrets.enc` with `IOSBK_SECRET_KEY`; losing/changing that key makes them unreadable. Encryption at this layer does not replace host, volume, or backup access controls.

The administrator is responsible for protecting `/backups`, `/configs`, `/var/lib/lockdown`, `.env`, the admin password, and the master key; applying suitable retention/deletion rules; and complying with local law. Removing the container does not remove persistent data.

## 中文摘要

iOS Backup 是本地优先的单管理员工具，Web UI、静态资源、设备发现、配置和备份处理都在部署主机运行；没有遥测、广告、分析 SDK、远程字体或远程前端资源。

项目没有遥测，也不会自动向维护者上传数据。管理员显式配置的 Telegram、SMTP、企业微信、Bark 或 Webhook 通知，是唯一设计用于连接互联网第三方服务的集成；测试通知也会真实发送。启用 Wi-Fi 功能时，netmuxd 仍会在局域网内发现设备（包括 mDNS）并连接可达的 iOS 设备；这类本地设备通信不是遥测，也不会上传给维护者。设备标识/名称、电量、配对记录、备份和日志默认留在主机上。

管理员负责保护 `/backups`、`/configs`、`/var/lib/lockdown`、`.env`、管理员密码和 `IOSBK_SECRET_KEY`，并制定符合所在地规则的保留/删除策略。删除容器不会删除持久化数据。
