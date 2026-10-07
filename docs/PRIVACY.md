# Privacy

## Local-first boundary

iOS Backup runs on your own host and is managed by one administrator. The Web UI, embedded static assets, device discovery, configuration, and backup processing run on the deployment host. The project includes no telemetry, advertising, analytics SDK, remote font, or remote frontend asset.

The project has no telemetry and does not automatically upload data to the maintainer. Only administrator-configured notifications—Telegram, SMTP email, WeCom, Bark, or a generic webhook—are designed to connect to third-party Internet services. A notification test sends a real message, and those providers receive the configured content and metadata. When Wi-Fi features are enabled, netmuxd also discovers devices on the local network (including through mDNS) and connects to reachable iOS devices; that local device traffic is not telemetry or a maintainer upload.

Device identifiers/names, battery state, pairing records, backup contents, and application logs stay on the deployment host unless the administrator exports or sends them. The maintainer does not receive this data automatically.

Notification configuration GET responses expose only configured status, not stored secret values. Notification secrets and stored backup passwords are encrypted in `/configs/secrets.enc` with the instance master key, automatically stored in `/configs/secret_key` by default or explicitly supplied through `IOSBK_SECRET_KEY` / `IOSBK_SECRET_KEY_FILE`; losing/changing that key makes them unreadable. Default key and ciphertext share a directory, so encryption does not protect against disclosure of the entire configuration directory. It does not replace host, volume, or backup access controls.

The administrator is responsible for protecting `/backups`, `/configs`, `/var/lib/lockdown`, Compose configuration files in use, `admin_password`, `secret_key`, and external credentials; applying suitable retention/deletion rules; and complying with local law. Removing the container does not remove persistent data. The newly generated administrator password appears once in first-start logs; protect those logs. The master key is never logged.

## 中文摘要

iOS Backup 由一位管理员管理，在自己的主机上运行，Web UI、静态资源、设备发现、配置和备份处理都在部署主机运行；没有遥测、广告、分析 SDK、远程字体或远程前端资源。

项目没有遥测，也不会自动向维护者上传数据。只有管理员自行配置的 Telegram、SMTP、企业微信、Bark 或 Webhook 通知功能会按设计连接互联网第三方服务；测试通知也会真实发送。启用 Wi-Fi 功能时，netmuxd 仍会在局域网内发现设备（包括 mDNS）并连接可达的 iOS 设备；这类本地设备通信不是遥测，也不会上传给维护者。设备标识/名称、电量、配对记录、备份和日志默认留在主机上。

保存的备份密码和通知秘密使用实例主密钥加密在 `/configs/secrets.enc`；默认密钥自动保存在 `/configs/secret_key`，也可由高级环境值或文件提供。密钥与密文默认同目录，不能防御整个配置目录泄露。管理员负责保护 `/backups`、整个 `/configs`、`/var/lib/lockdown`、所有使用的 Compose 配置文件和外部凭据，并制定符合所在地规则的保留/删除策略。首次生成的管理员密码会在初始化日志中显示一次，请保护该日志；主密钥永不写入日志。删除容器不会删除持久化数据。
