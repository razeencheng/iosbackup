# 在群晖 DSM 上部署并验证 USB 备份

[English](synology.md) · [返回操作手册](../OPERATIONS.zh-CN.md)

## 目标与适用条件

本页以 **Container Manager 的“项目”界面** 完成部署：准备共享文件夹 → 导入 Compose → 启动项目 → 查看密码并登录 → 完成第一次 USB 备份。主流程通过 DSM 和浏览器操作；需要进一步诊断时，再使用文末的可选 SSH 步骤。

准备 DSM 管理员账号、一台知道锁屏密码的 iPhone/iPad、USB 数据线，以及容量充足的本地存储卷。

- NAS 使用 DSM 7.2 或以上版本，且该型号的套件中心提供兼容的 **Container Manager** 。“项目”功能支持 Compose；不同套件版本的 DSM 要求见[群晖发行说明](https://www.synology.com/en-us/releaseNote/ContainerManager?os=DSM&version=7_x_series)。
- 镜像支持 `linux/amd64` 和 `linux/arm64`，NAS 的 CPU 架构须匹配。能安装 Container Manager 不代表所有型号都能运行此镜像或访问 USB 设备。
- 已有实例使用[升级与回滚](upgrade.zh-CN.md)或[实例备份与恢复](instance-recovery.zh-CN.md)，不要把已有数据目录当成全新部署目录。

界面入口依据群晖官方文档编写，按钮名称可能随 DSM 版本和语言变化。具体型号的 USB 支持以本页最后的实际备份结果为准。

## 1. 安装 Container Manager 并确认存储空间

1. 登录 DSM，在 **“套件中心”** 找到并安装 Container Manager；已经安装时，直接打开。
2. 确认左侧有 **“项目”** 入口。若只有旧版 Docker 套件或没有该入口，先核对型号和套件兼容性。
3. 在 **“存储管理器”** 查看目标存储卷的剩余容量。对照手机“设置 → 通用 → iPhone 储存空间”的已用容量，为备份和 NAS 日常运行保留余量。
4. 在 Container Manager 的项目和容器列表中确认没有正在运行的旧 iOS Backup 实例；有正在执行的备份时，先等待任务结束。

![DSM 中的 Container Manager](images/01-syno-cm.png)

## 2. 在 File Station 准备部署目录

1. 打开 **File Station** ，选择用于存放备份的共享文件夹，例如 `docker`。如果还没有合适的共享文件夹，在 **“控制面板 → 共享文件夹”** 创建，并选择目标存储卷。
2. 在共享文件夹中新建 `iosbackup` 文件夹。本文示例的完整路径是 `/volume1/docker/iosbackup`；你的卷可能是 `volume2`，共享文件夹名称也可能不同，后续始终选择实际目录。
3. 在共享文件夹权限和该目录的属性中确认访问范围，只授权需要管理此实例的账号。配置目录使用支持硬链接的本地文件系统，细节见[配置参考](configuration.zh-CN.md)。
4. 在电脑上下载配套的 [compose.yaml](../../compose.yaml)，将文件重命名为 `docker-compose.yml`，保留全部内容，供下一步上传。

默认安装会自动创建数据目录和凭据，只需导入配套 Compose，无需手动生成密码或主密钥。项目创建后，三个数据目录对应如下：

| 示例 NAS 目录 | 容器目录 | 保存内容 |
|---|---|---|
| `/volume1/docker/iosbackup/data/backups` | `/backups` | 设备备份 |
| `/volume1/docker/iosbackup/data/configs` | `/configs` | 管理员密码、主密钥和应用配置 |
| `/volume1/docker/iosbackup/data/lockdown` | `/var/lib/lockdown` | 设备配对记录 |

Compose 中的 `./data/...` 相对于项目工作目录。选择项目路径时就决定了数据保存位置，不要在备份开始后随意移动目录。

## 3. 在“项目”中导入 Compose 并启动

1. 打开 **Container Manager → 项目 → 创建** 。
2. 填写 **项目名称** ，例如 `iosbackup`；**路径** 选择刚创建的部署目录，例如 `/volume1/docker/iosbackup`。
3. 在 **来源** 选择上传 Compose 文件，上传电脑上的 `docker-compose.yml`。也可以使用编辑器创建文件并粘贴原 Compose 的全部内容。
4. 核对配置保留以下内容：

   | 配置 | 应保留的值或作用 |
   |---|---|
   | 镜像 | 默认 `ghcr.io/razeencheng/iosbackup:latest` |
   | `network_mode` | `host`，使用 NAS 网络 |
   | `privileged` | `true`，供设备访问使用 |
   | 数据挂载 | `./data/backups`、`./data/configs`、`./data/lockdown` 对应上一节的三个容器目录 |
   | USB 挂载 | `/dev/bus/usb:/dev/bus/usb` |
   | udev 挂载 | `/run/udev:/run/udev:ro` |
   | 监听端口 | 默认 `9000` |

5. 网页门户步骤保留 Web Station 选项关闭，后续直接使用 NAS 地址和应用端口访问。
6. 确认摘要后点击 **“完成”** ，选择完成后启动项目。等待镜像下载和容器创建结束；第一次下载可能较久。
7. 确认项目中的 `iosbackup` 容器正在运行。如果创建完成但未启动，在项目的 **“动作”** 中先 **“构建”** ，完成后 **“启动”** ；这里的构建使用 Compose 中指定的镜像，无需自行编译源码。

创建向导和项目操作见[群晖官方项目帮助](https://kb.synology.cn/zh-cn/DSM/help/ContainerManager/docker_project)。通过“项目”启动、停止和更新应用，避免另用单容器创建向导重复部署。

host 网络不需要额外端口映射。访问端口默认是 `9000`，与 DSM 管理端口不同。若启用 DSM 防火墙，在 **“控制面板 → 安全性 → 防火墙”** 允许可信管理来源访问该 TCP 端口，保留现有管理规则；方法见[群晖防火墙帮助](https://kb.synology.com/index.php/en-us/DSM/help/DSM/AdminCenter/connection_security_firewall?version=7)。不要把 Web 界面直接暴露到公网。

![Container Manager 项目配置](images/02-syno-config.png)

## 4. 查看容器日志并登录

1. 在 **Container Manager → 容器** 选择 `iosbackup`，打开 **“详细信息 → 日志”** 。这里查看应用的输出；左侧总览中的 Container Manager 活动日志不是首次密码所在的位置。容器日志入口见[群晖官方容器帮助](https://kb.synology.com/en-global/DSM/help/ContainerManager/docker_container)。
2. 找到首次启动时生成的管理员密码，妥善保存。密码只在生成时显示一次，主密钥不会出现在日志中；截图或分享日志时遮盖密码。
3. 用浏览器打开 `http://<NAS地址>:9000/`，输入管理员密码，点击 **“验证并进入”** 。已有 HTTPS 访问地址时使用该地址；HTTP 不加密密码，直接访问限于可信局域网。
4. 登录后核对页面显示的应用版本。首次使用向导中选择 **“暂时跳过，只用 USB”** ，先完成下一节的 USB 备份。
5. 返回项目的 **“YAML 配置”** 核对三个数据挂载，并在 File Station 核对它们位于目标部署目录；需要查看容器实际挂载源时，使用文末可选的 SSH 检查。

首次启动会在 `data/configs` 保存管理员密码、认证摘要和 `secret_key`。配置目录为 `0700`，新秘密文件为 `0600`；File Station 因权限不能查看时，不要给所有人读写权限。找不到首次生成密码的日志时，可按文末的可选 SSH 步骤读取默认密码；自定义密码的处理见[安装指南](installation.zh-CN.md)。

![Container Manager 中的 iOS Backup 容器信息](images/03-syno-iosbackup-info.png)

## 5. 在 NAS 上完成第一次 USB 备份

1. 用数据线把手机连接到 **NAS 的实际 USB 端口** 。手机出现密码输入界面时输入锁屏密码，出现“信任此电脑”提示时解锁后点击“信任”；无需持续保持解锁或亮屏。
2. 在 iOS Backup 控制台按向导完成配对，确认目标设备显示 **USB 已连接** 。手机插在打开浏览器的电脑上，不能替代插在 NAS 上。
3. 核对电量、充电状态、备份位置和剩余空间，点击 **“立即备份”** 。备份唤醒手机后，按出现的密码或信任提示完成确认，保持数据线连接。
4. 等待明确的 **“备份已完成”** ，核对完成时间和最近备份记录。需要详细步骤或进度说明时，阅读 [USB 配对与首次备份](usb-backup.zh-CN.md)。
5. 确认成功后，可继续[启用 Wi-Fi 备份](wifi.zh-CN.md)，再设置[定时任务](scheduling.zh-CN.md)和[通知](notifications.zh-CN.md)。按[实例备份与恢复](instance-recovery.zh-CN.md)保护三个数据目录。

完成标准是应用版本和数据位置正确、管理员能够登录，以及一台设备完成一次真实 USB 备份。记录 NAS 型号、DSM/Container Manager 版本、设备系统版本和结果，供升级后对照；备份成功不等于已验证整机恢复。

## 日常管理仍使用 Container Manager

无设备任务时，在 **“项目 → 动作”** 中使用 **“停止”** 、**“启动”** 或 **“重新启动”** 。配置需要修改时，从该项目的 **“YAML 配置”** 入口编辑并按界面提示部署；升级前先阅读[升级与回滚](upgrade.zh-CN.md)。

**“清除”或“删除”涉及移除项目资源，不能当作普通停止按钮。** 不要用这些操作排查登录或 USB 问题；容器导出的设置也不能代替三个持久化目录的备份。

## 可选：SSH 检查和读取默认密码

只在图形界面信息不足、需要检查主机 USB 路径、实际挂载、磁盘状态，或找回默认密码时使用。本节不用于重复创建或启动项目。

1. 在 DSM **“控制面板 → 终端机和 SNMP → 终端机”** 启用 SSH，仅允许可信管理网络访问，记录实际端口。
2. 在电脑终端连接 NAS，替换示例账号、地址和端口：

   ```bash
   ssh -p 22 your-admin@your-nas
   ```

3. 使用 administrators 组账号，进入管理员 shell，并切换到实际项目目录：

   ```bash
   sudo -i
   cd /volume1/docker/iosbackup
   ```

4. 根据问题选择检查项，不必全部执行：

   ```bash
   uname -m
   ls -ld /dev/bus/usb /run/udev
   df -h .
   df -i .
   docker ps --filter name=iosbackup
   docker logs --tail=200 iosbackup
   docker inspect iosbackup --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}'
   ```

   `x86_64` 对应 `amd64`，`aarch64` 对应 `arm64`。若 `df` 不支持 `-i`，跳过该项；路径缺失时先核对型号与部署条件，不创建空 `/run/udev` 代替真实设备信息。分享检查结果前脱敏。

5. 找不到首次密码日志时，在这个受保护的 **NAS 管理员 shell** 读取默认密码：

   ```bash
   cat data/configs/admin_password
   ```

   这是宿主机文件，不是在 Container Manager 的“打开终端”中执行；运行镜像不提供普通 shell 或 cat。不要公开命令输出，不要读取或展示 `secret_key`。

6. 需要区分本机服务与访问网络问题，且 NAS 已有 curl 时，可以检查：

   ```bash
   curl -fsS http://127.0.0.1:9000/healthz
   curl --fail --user iosbackup http://127.0.0.1:9000/api/version
   ```

   若修改了端口，替换 `9000`。curl 会交互提示输入管理员密码；`/healthz` 成功只说明 HTTP 能响应，不证明 USB 配对或备份成功。

操作结束后退出管理员 shell 和 SSH 会话。若 SSH 仅为本次排错开启，可在 DSM 关闭，先确认没有依赖该连接的传输任务。

## 遇到问题时

| 现象 | 先在图形界面检查 | 何时使用可选 SSH |
|---|---|---|
| 项目创建或镜像下载失败 | 查看项目部署输出，核对镜像来源、网络和 NAS 架构 | 需要进一步确认架构或 Docker 状态时 |
| 容器立即退出 | 打开该容器日志，检查路径、权限和端口冲突 | 日志指向挂载源不存在或权限问题时 |
| 容器运行，但浏览器打不开 | 核对 NAS 地址、默认端口和 DSM 防火墙；host 网络没有端口映射 | 用本机 `/healthz` 区分服务与访问网络问题时 |
| USB 无设备或配对失败 | 确认线插在 NAS、支持数据传输，并处理手机密码/信任提示；查看应用与容器日志 | 需要确认 `/dev/bus/usb`、`/run/udev` 和实际挂载时 |
| 无法写入备份目录 | 检查 File Station 目录、存储容量和 DSM 权限 | 需要核对实际路径、磁盘或 inode 状态时 |
| 忘记管理员密码且首次日志已丢失 | 先查看当前实例是否使用自定义密码 | 读取默认 `data/configs/admin_password` 时 |

保留已有配置、配对记录和备份。仍无法解决时，按[常见排错](troubleshooting.zh-CN.md)整理脱敏环境信息与错误日志。
