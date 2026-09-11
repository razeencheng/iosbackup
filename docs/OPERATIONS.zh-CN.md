# 操作手册

[English](OPERATIONS.md) · [返回 README](../README.zh-CN.md)

本手册适用于官方 Linux 容器，不承诺 Windows/macOS 原生真机支持，也不保证恢复成功。请保留独立验证过的备份。

## 安全边界

服务需要 `--privileged`、`--network host`、`/dev/bus/usb:/dev/bus/usb` 和 `/run/udev:/run/udev:ro`。特权模式等同授予宿主机级权限。只能部署在可信主机和可信局域网/VPN，保持认证开启，不要暴露到公网。必须跨可信网络访问时，请使用带认证的 HTTPS 反向代理。

必须持久化并保护三个数据位置：

- `/backups`：设备备份与可选解包结果
- `/configs`：设置、认证、CSRF 状态和加密秘密
- `/var/lib/lockdown`：配对记录

把 `.env`、`data/configs/admin_password`、`IOSBK_SECRET_KEY`、配对记录、设备标识和备份内容都当作秘密。不要把它们放进 Issue 或日志片段。

## 日常检查

```bash
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl -u "iosbackup:$(cat data/configs/admin_password)" \
  http://127.0.0.1:9000/api/version
docker compose logs --tail=200 iosbackup
```

`/healthz` 只证明 HTTP 进程有响应，不能证明设备已配对、备份可读或 Wi-Fi 传输健康。还应检查界面状态和一份已完成的测试备份。

应用和设备日志写到容器标准输出，请使用 `docker compose logs`；项目没有公开日志下载 API。分享片段前，必须删除设备标识、网络地址、通知目标和文件路径。

## 首次 USB 配对与信任

1. 解锁设备，并保持屏幕唤醒。
2. 使用可靠的 USB 数据线直接连接主机。
3. 点击 **信任（Trust）**，并输入设备锁屏密码。
4. 刷新界面；如有提示，开始配对并再次接受信任。
5. 第一次 USB 备份完成前保持设备连接。

反复配对失败时，请重新插拔 USB、解锁设备后重试，并确认 `/dev/bus/usb:/dev/bus/usb`、`/run/udev:/run/udev:ro` 已配置。不要把删除 `/var/lib/lockdown` 当成常规排错手段；这会丢弃配对状态，所有设备都要重新配对。

## 定时备份

调度器会按设备配置的备份时间窗、最小间隔、电量阈值和充电条件筛选任务，用户可见的调度时间使用北京时间。每设备锁会阻止备份/检查重叠，备份运行时也会阻止刷新。如果条件不满足，后续调度检查会再次考虑该设备。

先完成一次手动 USB 备份，再为一台设备开启定时任务；至少观察一次完整运行并核对备份信息后，再增加设备。

## 备份进度说明

主进度条表示整次备份的总体进度。设备工具能提供时，另一个当前文件进度会在切换文件时重新计算。界面刻意不显示文件名，也不估算剩余时间。Wi-Fi 设备短暂消失时，状态可能显示“等待设备重新连接”并保留最后一次可信进度。任务一旦结束就不会自动续传；恢复连接后需重新开始备份。

### 无活动超时

备份界面区分等待手机授权、向手机发送数据、接收数据和等待手机处理，并显示最后备份活动时间（北京时间）。增量备份需要先把已有清单发给手机；大型加密清单可能需要几分钟处理，不能单凭总体 0% 或当前文件 100% 判断卡死。总体达到 100% 也必须等工具确认本次备份成功。

配套镜像的 `idevicebackup2` 通过无载荷的协议收发计数报告活动。只有真实收发字节或成功收到协议消息才刷新活动时间，设备在线、心跳、防休眠续租和重复进度显示均不计入。使用未带补丁的自定义工具时，后备信号是已识别的文件切换/字节/进度变化，精度受工具输出限制。

| 启动环境变量 | 默认值 | 适用阶段 |
| --- | --- | --- |
| `IOSBK_BACKUP_AUTHORIZATION_TIMEOUT` | `5m` | 等待手机输入密码授权 |
| `IOSBK_BACKUP_INACTIVITY_TIMEOUT` | `10m` | 正在发送或接收但无新活动 |
| `IOSBK_BACKUP_PREPARATION_TIMEOUT` | `30m` | 建立会话、等待手机处理或下一步响应 |

均接受 `1s`–`24h` 的 Go duration，不能设为零禁用；更改 Compose 环境变量并重建容器后持久生效。它们限制连续无活动时间，不限制有持续传输的备份总时长。较慢设备可提高准备期限。

超时会终止旧任务进程组，显示 `backup_stalled`，保留已有备份集和最后成功时间，释放占用以便重新备份。请检查手机提示和网络后手动重试；不会立即循环自动重试，后续自动备份仍按原调度条件运行。如果新任务仍反复停在相同位置，可用 USB 做对照。此保护处理无限等待，不证明 iOS 最初不再响应的内部原因。

## Wi-Fi 前提与回退

Wi-Fi 备份属于预览（Preview）功能。启用前：

1. 先完成 USB 配对并保留 `/var/lib/lockdown`。
2. 先在 Mac 的 Finder 或 Windows 的 Apple Devices/iTunes 中开启无线可见；这只是准备 iOS 设备，不代表 iOS Backup 支持在这些系统原生运行。
3. 确保设备与主机网络互通；自动发现需要使用主机网络模式。
4. 除非诊断已知兼容问题，否则保留默认 netmuxd 后端。
5. 除非特定设备需要临时诊断回退，否则保持 `IOSBK_WIFI_POWER_ASSERTION=true`。
6. 定时前先验证一次手动 Wi-Fi 备份。

活动数据流期间，设备发现短暂消失时会使用有界的重新连接宽限期；已经失败结束的任务不会自动续传。出现 `Could not receive from mobilebackup2 (-4)`、反复重连、休眠或锁屏失败时，请解锁并重新连接设备，再改用 USB 重试。USB 是支持的回退路径；设备卡片可见不代表活动 Wi-Fi 数据流一定健康。

## 备份卷

升级或执行实验操作前：

1. 确认没有备份、解包、删除或恢复正在执行。
2. 停止服务，避免快照期间配置和配对文件变化。
3. 把 `.env` 与三个数据目录复制到受保护存储。

请按完整设备备份预留空间，并另外考虑卷快照或实验性解包产生的临时副本。

在仓库目录创建归档的示例：

```bash
docker compose stop
umask 077
tar -czf iosbackup-state-backup.tgz \
  .env data/backups data/configs data/lockdown
docker compose start
```

归档包含凭据、配对记录和个人数据，必须加密保存，并测试能否列出与恢复。容器镜像不是这些卷的备份。

## 升级

1. 阅读 [CHANGELOG.md](../CHANGELOG.md) 和[功能状态](FEATURE_STATUS.zh-CN.md)。
2. 创建并核验上面的卷备份。
3. 把 `.env` 中的 `IOSBK_IMAGE` 改成明确审核过的标签，例如 `ghcr.io/razeencheng/iosbackup:v1.5.2`。
4. 拉取镜像并重建服务。
5. 检查健康状态、构建身份、日志、配对和只读查看；然后在非关键设备上完成一次 USB 备份并确认结果。

```bash
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:9000/healthz
docker compose logs --tail=200 iosbackup
```

不要使用未经审核的浮动镜像标签。

## 回滚

把 `IOSBK_IMAGE` 改回之前确实可工作的精确标签或摘要，再执行 `docker compose up -d`，并通过 `/api/version` 核对回滚结果。在旧应用验证完成前保留当前卷快照。

不要自动用旧 `/backups`、`/configs` 或 `/var/lib/lockdown` 覆盖新数据。只有发布说明明确指出格式不兼容，或确认当前数据已经损坏时，才恢复卷快照；同时保留失败状态用于诊断。

## 实验操作

整机恢复按设备以 `restore_enabled=false` 保持关闭。本地解包和本地备份删除由 `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS=false` 全局关闭；Compose 会显式传递这个默认值。阅读[功能状态](FEATURE_STATUS.zh-CN.md)并完成卷备份后，才可把它严格设置为 `true`。

即使启用，解包/删除仍需已认证的 UI/API、CSRF、校验和操作专用确认；恢复还有独立的每设备开关和多重确认。它们都不是常规恢复承诺。

## 排错

### 找不到设备或配对失败

- 解锁设备，重新插拔可靠的 USB 数据线，并接受“信任”。
- 检查容器配置中是否有 USB 和 udev 挂载。
- 查看 `docker compose logs --tail=200 iosbackup` 的配对/usbmux 日志。
- 保留 `/var/lib/lockdown`；只有作为有备份的最后手段才替换它。

### 设备锁定或密码错误

配对、修改加密设置或开始备份前，请先解锁设备。锁屏状态变化也可能影响预览版 Wi-Fi 功能。命令已经失败时，请重新连接设备并改用 USB 重试。

### `mobilebackup2 (-4)`、Wi-Fi 中断或重新连接循环

备份运行时不要连续重启所有组件。等待活动任务进入终态，解锁并重新连接设备，查看之前的心跳与传输日志，再通过 USB 重试。如果 USB 成功，该设备应继续关闭 Wi-Fi，并提交已脱敏的预览功能问题。

### 磁盘空间

检查 `/backups` 后端文件系统的可用空间和 inode 数量。解包会创建第二份文件，可能需要大量额外空间。空间不足必须先修复再重试；不要为了清理而删除唯一已知可用备份。

### 权限或只读文件系统

确认宿主机目录存在，分别挂载到 `/backups`、`/configs`、`/var/lib/lockdown`，且容器可写。容器根文件系统应继续只读；应修复绑定挂载的路径或权限，而不是放宽无关宿主机权限。

### 容器运行但界面不可用

检查 `/healthz`、端口 `9000`、宿主机防火墙和 `docker compose logs`。使用主机网络时没有额外端口映射。若配置反向代理，请先检查本地服务端点，再分别检查代理认证和 TLS。

支持边界和安全报告内容见 [SUPPORT.md](../SUPPORT.md) 与 [SECURITY.md](../SECURITY.md)。
