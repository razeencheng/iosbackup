# 快速开始

[English](QUICKSTART.md) · [返回 README](../README.zh-CN.md)

本指南用于在可信 Linux 主机上启动官方 Docker Beta。同一镜像支持 `linux/amd64` 和 `linux/arm64`；不支持、也不承诺 Windows 或 macOS 原生真机运行。

> 容器使用 `--privileged` 和 `--network host`。特权模式等同授予宿主机级权限。不要运行未经审核的镜像，也不要把 Web 界面直接暴露到公网。

## 1. 准备主机

安装 Docker Engine、Docker Compose 插件、Git 和 OpenSSL。选择本地文件系统，并按完整设备备份预留空间。

```bash
git clone https://github.com/razeencheng/iosbackup.git
cd iosbackup
umask 077
mkdir -p data/backups data/configs data/lockdown
openssl rand -base64 24 > data/configs/admin_password
printf 'IOSBK_IMAGE=%s\nIOSBK_SECRET_KEY=%s\n' \
  'ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1' \
  "$(openssl rand -base64 32)" > .env
chmod 600 .env data/configs/admin_password
```

由于创建目录前已设置 `umask 077`，三个数据目录将以仅所有者可访问的权限创建；复制或恢复目录时也应保持该权限。保存过通知秘密或备份密码后，不要轮换 `IOSBK_SECRET_KEY`。请离线保存 `.env` 和管理员密码副本。

## 2. 审核必要权限

仓库提供的 [compose.yaml](../compose.yaml) 配置了以下等价能力：

- `--privileged`
- `--network host`
- `/dev/bus/usb:/dev/bus/usb`
- `/run/udev:/run/udev:ro`
- 持久化 `/backups`、`/configs`、`/var/lib/lockdown` 三个卷

只能在可信主机和可信局域网/VPN 使用。Compose 默认端口是 `9000`。远程访问必须放在带认证的 HTTPS 反向代理和网络 ACL 后面；不要直接发布到公网。

## 3. 启动并检查

```bash
docker compose pull
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:9000/healthz
curl -u "iosbackup:$(cat data/configs/admin_password)" \
  http://127.0.0.1:9000/api/version
```

打开 `http://<Linux主机>:9000/`，输入 `data/configs/admin_password` 中的密码。HTTP 传输不会加密密码，因此只能在可信网络中使用；超出主机访问时应优先使用 HTTPS。

## 4. 通过 USB 完成首次配对

1. 解锁 iPhone 或 iPad，并保持屏幕唤醒。
2. 用 USB 把设备连接到 Linux 主机。
3. 设备出现提示时点击 **Trust（信任）**，并输入锁屏密码。
4. 在 iOS Backup 中刷新设备列表；如有提示，开始配对。
5. 先在非关键设备上完成一次 USB 备份并确认结果，再启用定时任务。

如果没有出现“信任”提示，请重插数据线、再次解锁设备，并检查 USB/udev 挂载。配对记录保存在 `/var/lib/lockdown`；丢失该卷后必须重新配对。

## 5. 配置备份

在设备卡片中设置备份时间窗、间隔、电量阈值、充电条件和 `/backups` 下的路径。USB 流程验证通过前，先不要启用 Wi-Fi、加密或实验操作。

以下容器路径是持久状态，不是可随意删除的缓存：

| 路径 | 内容 |
|---|---|
| `/backups` | 备份数据和可选解包结果 |
| `/configs` | 备份设置、认证/CSRF 文件、加密后的通知/密码秘密 |
| `/var/lib/lockdown` | 设备配对记录 |

每次升级前备份三个卷和 `.env`。继续阅读[操作手册](OPERATIONS.zh-CN.md)，启用 Preview 或 Experimental 能力前先核对[功能状态](FEATURE_STATUS.zh-CN.md)。
