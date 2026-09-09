# 快速开始

[English](QUICKSTART.md) · [返回 README](../README.zh-CN.md)

本指南用于在可信 Linux 主机上启动官方 Docker 镜像。同一镜像支持 `linux/amd64` 和 `linux/arm64`；不支持、也不承诺 Windows 或 macOS 原生真机运行。

下面的图片来自 Raspberry Pi ARM64 上运行的 Beta 候选容器，记录登录、设备接入、备份进度和一次 USB 备份的成功终态。成功终态不代表已验证加密内容读取或整机恢复。截图来源与验证范围见[图片清单](images/tutorial/README.md)。

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
  'ghcr.io/razeencheng/iosbackup:v1.5.1' \
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

![管理员登录页面，密码输入框为空](images/tutorial/01-login.png)

点击“验证并进入”。若出现“用户名或密码不正确”，检查密码文件与 `/configs` 挂载是否对应当前实例；不要把密码写入 Issue 或截图。

![输入错误密码后的提示](images/tutorial/02-login-error.png)

## 4. 选择是否准备 Wi-Fi

首次进入后打开使用向导。只使用 USB 时，选择“暂时跳过，只用 USB”。准备以后使用 Wi-Fi 时，按向导在 Finder、Apple Devices 或旧版 iTunes 中打开设备的无线连接选项，再选择“我已完成”。这一步需要在电脑上操作，NAS 无法代为确认；跳过不会影响 USB 备份。

![首次使用向导的可选 Wi-Fi 准备步骤](images/tutorial/03-onboarding-wifi.png)

Wi-Fi 仍属于 Preview。首次验收继续使用 USB，先确认配对、存储和完整备份都正常。

## 5. 通过 USB 完成首次配对

1. 解锁 iPhone 或 iPad，并保持屏幕唤醒。
2. 用 USB 把设备连接到 Linux 主机。
3. 设备出现提示时点击 **Trust（信任）**，并输入锁屏密码。
4. 在 iOS Backup 中刷新设备列表；如有提示，开始配对。
5. 等待向导显示配对完成并进入“完成第一次备份”。

![等待设备通过 USB 接入主机](images/tutorial/04-connect-device.png)

如果没有出现“信任”提示，请重插数据线、再次解锁设备，并检查 USB/udev 挂载。配对记录保存在 `/var/lib/lockdown`；丢失该卷后必须重新配对。

如果页面提示设备已锁定，请在手机上输入锁屏密码，再重试配对。已经信任过的设备可能直接完成此步骤。主机上同时运行两个 USB mux 服务或两个使用 host 网络的备份容器，可能占用同一监听端口；测试新实例时应先停止冲突的旧实例，结束后恢复原服务。

## 6. 完成第一次 USB 备份

先在非关键设备上完成一次 USB 备份并确认结果，再启用定时任务。启动前确认设备显示 **USB**，并按完整设备备份预留空间；没有只备份少量样本文件的测试模式。

在主机运行 `df -h ./data/backups` 查看备份卷可用空间，并在 iPhone 的“设置 → 通用 → iPhone 储存空间”中检查已用容量。实际备份大小不一定等于设备已用空间，但在无法准确估算时，应按已用容量加余量准备存储，并为主机系统保留空间。空间不足时先换到足够大的磁盘或使用数据更少的测试设备；页面显示“可以开始第一次备份”只表示设备和配对已就绪，并不保证磁盘容量充足。

![真机通过 USB 配对后，向导进入首次备份步骤，尚未开始备份](images/tutorial/05-ready-backup.png)

1. 点击“立即备份”，保持数据线连接。
2. iOS 如要求输入锁屏密码或授权备份，请在手机上确认。
3. 等待任务出现明确的成功或失败结果。进度达到 100% 或设备仍然在线，都不能单独证明任务成功。
4. 返回控制台，核对最近一次备份结果、时间及只读备份信息。失败时保留已有数据，按[操作手册](OPERATIONS.zh-CN.md)排查后重试。

备份期间，控制台分别显示整次备份的总体进度和当前文件的传输进度。当前文件完成后会切换到下一个文件，因此文件进度归零不表示整次备份重新开始。

![首次 USB 备份正在接收数据，控制台显示总体与当前文件进度](images/tutorial/08-usb-backup-progress.png)

任务成功后，控制台显示“备份已完成”和完成时间，备份按钮恢复可用。

![USB 备份已正常结束，控制台显示完成时间与成功状态](images/tutorial/09-usb-backup-completed.png)

如果设备原先在 Finder 或 Apple Devices 中启用了备份加密，新实例创建的备份仍会加密。备份成功不表示当前实例已经保存正确的备份密码；缺少密码时，信息或清单读取可能失败并出现 `MBErrorDomain/207`。请保留原有备份加密密码，它不同于锁屏密码、后台登录密码和 `IOSBK_SECRET_KEY`。上述成功实例的磁盘完成标记已核对，但清单解密尚未验证。

完整备份也不等同于已经验证整机恢复；恢复为默认关闭的 Experimental 能力。

## 7. 配置备份

在设备卡片中设置备份时间窗、间隔、电量阈值、充电条件和 `/backups` 下的路径。USB 流程验证通过前，先不要启用 Wi-Fi、加密或实验操作。

以下容器路径是持久状态，不是可随意删除的缓存：

| 路径 | 内容 |
|---|---|
| `/backups` | 备份数据和可选解包结果 |
| `/configs` | 备份设置、认证/CSRF 文件、加密后的通知/密码秘密 |
| `/var/lib/lockdown` | 设备配对记录 |

每次升级前备份三个卷和 `.env`。继续阅读[操作手册](OPERATIONS.zh-CN.md)，启用 Preview 或 Experimental 能力前先核对[功能状态](FEATURE_STATUS.zh-CN.md)。
