# 快速开始

[English](QUICKSTART.md) · [返回 README](../README.zh-CN.md)

本页带你完成**全新 Linux Docker Compose 部署、登录和第一次 USB 备份** 。群晖使用[单独指南](manual/synology.zh-CN.md)，已有实例使用[升级与回滚](manual/upgrade.zh-CN.md)。高级设置和排错见[安装指南](manual/installation.zh-CN.md)。

## 1. 下载 Compose 文件

准备安装 Docker Engine 和 Compose 插件的 Linux 主机、iPhone/iPad（需知道锁屏密码）和 USB 数据线，按完整设备备份预留空间。镜像支持 `linux/amd64`、`linux/arm64`，不承诺 Windows 或 macOS 原生真机运行。

将与所用镜像配套的 [compose.yaml](../compose.yaml) 保存到一个新的部署目录，例如 `$HOME/iosbackup-deploy`

> Compose 使用 `--privileged`、`--network host`、`/dev/bus/usb:/dev/bus/usb` 和 `/run/udev:/run/udev:ro`，并持久化 `/backups`、`/configs`、`/var/lib/lockdown`。特权模式授予宿主机级权限；建议只使用可信镜像，限制访问源，不要把 Web 界面直接暴露到公网。

## 2. 启动并登录

在 **Linux 主机的部署目录** 执行：

```bash
cd "$HOME/iosbackup-deploy"
docker compose up -d
docker compose logs iosbackup
```

首次启动自动生成管理员密码和内部加密主密钥，保存在持久化的 `data/configs` 中，重启或升级继续复用。日志只在生成时显示一次管理员密码；主密钥永不写入日志。不要公开首次日志。

浏览器打开受保护的访问地址，默认为 `http://<Linux主机>:9000/`。输入日志中的管理员密码，点击“验证并进入”，打开首次使用向导并选择“暂时跳过，只用 USB”。

![管理员登录页面，密码输入框为空](images/tutorial/01-login.png)

找不到首次日志时，可在**宿主机的受保护终端** 用 `sudo cat data/configs/admin_password` 读取默认密码。自定义密码或启动失败的处理见[安装指南](manual/installation.zh-CN.md)。冷启动可能需要数十秒；`/healthz` 通过只表示 HTTP 能响应，不证明配对或备份成功。

## 3. 完成配对和第一次 USB 备份

1. 用 USB 把手机连接到运行服务的 Linux 主机。出现“信任此电脑”提示时，解锁后点击“信任”并按提示确认。
2. 在页面刷新设备，按提示检查配对（在手机上点击“信任”后，配对通常已完成），等向导显示“完成第一次备份”；确认连接方式为 **USB** 。
3. 建议对照手机已用容量检查主机上 `data/backups` 可用空间并保留余量。向导中“可以开始第一次备份”只表示设备和配对就绪，不检查磁盘够用。建议先完成一次 USB 备份。

![首次使用向导的 USB 备份步骤，图中为第一次备份完成后的状态](images/tutorial/05-ready-backup.png)

4. 点击“立即备份”，保持数据线连接；备份唤醒手机并出现密码输入界面时，按提示输入锁屏密码；
5. 等待进度即可。

![USB 备份过程中显示总体和当前文件进度](images/tutorial/08-usb-backup-progress.png)

6. 核对“备份已完成”、完成时间和[只读备份信息](manual/inspection.zh-CN.md)。失败时保留数据，按 [USB 备份指南](manual/usb-backup.zh-CN.md)排查后重试。

![备份完成页示例：群晖 NAS 控制台，截图时设备通过 Wi-Fi 在线](images/tutorial/09-usb-backup-completed.png)

已有加密设置的设备仍会生成加密备份；请保留原备份加密密码，成功备份不代表实例已保存该密码。加密清单读取见[加密指南](manual/encryption.zh-CN.md)。备份成功也不等于已验证整机恢复。

## 下一步

第一次 USB 备份成功后，可以继续[启用并验证 Wi-Fi 备份](manual/wifi.zh-CN.md)。按指南完成电脑端的无线连接设置，再回到控制台，确认设备显示 **Wi-Fi 已连接** 并完成一次 Wi-Fi 备份。

之后可按需要继续：

- [设置定时备份](manual/scheduling.zh-CN.md)和[通知](manual/notifications.zh-CN.md)，安排日常备份并接收结果。
- [查看备份信息和清单](manual/inspection.zh-CN.md)，或了解[备份加密与密码](manual/encryption.zh-CN.md)。
- 遇到问题时，查看[常见排错](manual/troubleshooting.zh-CN.md)和[状态、进度与错误说明](manual/states.zh-CN.md)。
- 升级或迁移前，按[实例备份与恢复](manual/instance-recovery.zh-CN.md)保护三个持久化卷，包括 `data/configs` 中的密码和 `secret_key`，以及实际使用的额外配置与外部密钥，再阅读[升级与回滚](manual/upgrade.zh-CN.md)。

更多任务见[操作手册](OPERATIONS.zh-CN.md)，各项能力的支持范围见[功能状态](FEATURE_STATUS.zh-CN.md)。
