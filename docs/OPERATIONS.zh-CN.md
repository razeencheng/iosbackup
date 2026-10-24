# 操作手册

[English](OPERATIONS.md) · [返回 README](../README.zh-CN.md)

这套手册适合在家中自己部署和管理 iOS Backup 的用户，步骤已对照当前代码、界面和已有测试记录核对。普通 Linux 主机使用 Docker Compose，群晖另有安装指南；中英文包含相同的操作内容。第一次使用，建议从带截图的[快速开始](QUICKSTART.zh-CN.md)入手。

首次启用计划前，请**在非关键设备上完成一次 USB 备份** ，并**按完整设备备份预留空间** 。目前还没有通过本手册完成真机恢复的成功记录。备份任务是否完成、文件清单能否读取、能否恢复到设备，需要分别检查。

## 选择你要完成的任务

| 任务 | 完成后应能确认 |
|---|---|
| [开始前确认环境与支持边界](manual/overview.zh-CN.md) | 判断 Linux/NAS、权限和功能成熟度是否适合自己 |
| [安装、登录与更换管理员密码](manual/installation.zh-CN.md) | 创建全新实例并检查版本和持久化目录 |
| [在群晖 DSM 部署](manual/synology.zh-CN.md) | 核对 Container Manager、Compose、USB 与目录权限 |
| [连接 USB 并完成首次备份](manual/usb-backup.zh-CN.md) | 按手机提示输入密码或确认信任、配对、开始备份并确认备份结果 |
| [设置自动备份](manual/scheduling.zh-CN.md) | 设置时间窗、间隔、电量和充电条件并观察一次执行 |
| [启用并验证 Wi-Fi 备份](manual/wifi.zh-CN.md) | 用电脑准备手机、与 NAS 配对、测试无线备份，失败时改用 USB |
| [管理备份加密与密码](manual/encryption.zh-CN.md) | 区分四类密码，处理已有加密备份和接管限制 |
| [查看备份信息和导出清单](manual/inspection.zh-CN.md) | 理解 5000 项限制、CSV 与完整数据的区别 |
| [设置并验证通知](manual/notifications.zh-CN.md) | 保存规则、检查测试送达、观察真实事件 |
| [管理设备和存储](manual/device-storage.zh-CN.md) | 移除设备、重新添加、检查空间和保留副本 |
| [恢复手机数据：总览与准备](manual/restore.zh-CN.md) | 先保护现有备份，查看 Finder / Apple Devices 的前提与待验证范围 |
| [备份、迁移和恢复部署实例](manual/instance-recovery.zh-CN.md) | 同时保护主密钥、配置、配对记录和备份集 |
| [升级与回滚](manual/upgrade.zh-CN.md) | 固定镜像版本，验证升级结果并保留回退路径 |
| [评估实验操作](manual/experimental.zh-CN.md) | 了解解包、删除、整机恢复的启用条件、风险和已验证范围 |
| [按症状排错](manual/troubleshooting.zh-CN.md) | 根据页面和日志先做简单检查，再决定重试或求助 |
| [查配置、路径和生效方式](manual/configuration.zh-CN.md) | 区分程序/Compose 默认值与设备设置 |
| [看懂状态、进度和错误](manual/states.zh-CN.md) | 看懂任务阶段、无活动超时，判断备份是否完成 |
| [排查高级 Wi-Fi / NAT 问题](manual/wifi-nat.zh-CN.md) | 仅在 USB 对照后调查跨网段和空闲连接 |

## 日常查看

在实际 Compose 部署目录运行，若端口已改变，请替换 `9000`：

```bash
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl --user iosbackup http://127.0.0.1:9000/api/version
docker compose logs --tail=200 iosbackup
```

curl 会提示输入管理员密码。`/healthz` 只确认 HTTP 能响应，不能证明设备就绪、磁盘可写或备份可恢复。日志含可能的设备与网络信息，分享前脱敏；当前没有公开日志下载 API。

## 备份查询诊断

备份信息或清单查询失败时，展开“诊断详情”，点击“复制诊断”。浏览器无法访问剪贴板时，可以选中回退文本手动复制。信息不可用时仍可能显示“占用空间”；清单失败时文件数显示未知，不会显示成零个文件。反馈时提供应用版本、iOS 版本、USB/Wi-Fi 连接方式和复制的诊断，分享前检查内容，不需要提交密码或原始文件清单。

查询失败在默认日志等级下记录 WARN，事件为 `event=backup_query.failed`。CSV 准备失败为 `event=backup_export.failed`，响应写入中断为 `event=backup_export.interrupted`。正常请求取消只在 DEBUG 下记录 `event=backup_query.cancelled`。诊断分别保存真实进程退出码与工具明确报告的设备错误码；只有退出码 49 或 255，不能据此认定密码错误或备份损坏。

CSV 完整生成后才开始下载。同时只允许一个导出，单文件最多 **32 MiB**，沿用**五分钟命令超时**，响应写入期限为**两分钟**。JSON 页面仍最多保留 5000 项。导出准备失败会留在页面显示错误，不会下载半份文件；之后的传输中断仍可能导致下载失败，需要重试。

临时 CSV 使用 `TMPDIR`（通常为 `/tmp`），权限为 `0600`，创建后立即删除目录项，成功或失败关闭文件时释放空间。默认 Compose 的 `/tmp` 为 **64 MiB tmpfs**，与其他临时任务共享，需要为导出留足空间。超过大小上限或空间不足返回 HTTP 507，已有导出占用名额返回 503 和 `Retry-After`。反向代理需给上游留出查询准备及传输时间；代理自身超时可能更早结束请求。本次诊断增强不增加离线读取或加密清单解密能力。

## 始终保留的访问与数据边界

容器使用特权模式与宿主机网络，需要 USB/udev 挂载。只在可信主机和受限局域网/VPN 部署，保持认证开启，不要暴露到公网。跨网络访问使用合适的 HTTPS 与访问控制。

长期保存 `/backups`、整个 `/configs`（包括默认 `secret_key`、密码与摘要）、`/var/lib/lockdown`，以及所有使用的 Compose 配置文件和外部凭据。管理员密码、主密钥、备份密码、配对记录与用户数据都不能放入公开 Issue。容器镜像不包含这些数据。Core、Experimental 的边界见[功能状态](FEATURE_STATUS.zh-CN.md)。

## 文档和验证材料

- [贡献者开发指南](DEVELOPMENT.zh-CN.md)与[离线测试指南](TESTING_GUIDE.md)：复现软件检查；不等于设备兼容性或恢复保证。
- [支持范围](../SUPPORT.md)、[隐私](PRIVACY.md)、[安全报告](../SECURITY.md)：普通问题先准备脱敏环境和日志；漏洞按私密渠道报告。

标注“源码核对”的步骤已对照代码检查；“已有真机记录”仅说明记录中的设备和版本做过相应操作。其他设备也需要单独测试。
