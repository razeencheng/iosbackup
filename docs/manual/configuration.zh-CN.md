# 配置、路径与生效方式

[English](configuration.md) · [返回手册](../OPERATIONS.zh-CN.md)

本页说明应用配置和随仓库提供的 `compose.yaml`。

## 修改配置并使其生效

设备设置和通知设置在 Web UI 保存后生效。进程参数统一在 `compose.yaml` 的 `services.iosbackup.environment` 中修改；镜像选择修改同一服务的 `image`。这些参数在启动时读取：先等待活动任务结束，再重建容器；单纯保存文件或执行 `docker compose restart` 不会更新容器环境。群晖用户在 Container Manager 项目的“YAML 配置”中修改并按界面提示重新部署。

普通参数直接填写值，例如 `PORT: "9000"`、`LOG_LEVEL: "INFO"`；只添加需要调整的项目，保留其他配置。默认密码和主密钥由程序生成并保存，无需填写凭据。

例如需要把准备阶段的等待时间改为 45 分钟时，在已有 Compose 文件的 `services.iosbackup.environment` 中添加下面一项，保留其他配置。不要用这个片段覆盖整个文件：

```yaml
services:
  iosbackup:
    environment:
      IOSBK_BACKUP_PREPARATION_TIMEOUT: "45m"
```

```bash
docker compose config --quiet
docker compose up -d --force-recreate iosbackup
docker compose logs --tail=100 iosbackup
curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
```

若使用自定义端口，应替换 `9000`。配置校验不证明挂载、设备或密码可用；重启后重新登录、检查设备，再执行相应任务验证。不要分享 `docker compose config` 的完整输出，它可能展开主密钥。

## 进程环境变量

除密码和主密钥外，其他值本身通常不是凭据，但路径和网络范围仍可能暴露部署信息。布尔量只接受 `true` / `false`（不接受 `1` / `0`）。时长使用 `30s`、`10m`、`1h` 等格式；列出的超时均不能用零禁用。

| 变量 | 程序默认 | Compose 默认 | 用途和范围 |
|---|---|---|---|
| `IOSBK_LISTEN_ADDR` | `127.0.0.1` | `0.0.0.0` | 监听 IP 字面量，不接受主机名。 |
| `PORT` | `8080` | `9000` | 整数 1–65535；直接修改 environment 中的 PORT。 |
| `LOG_LEVEL` | `INFO` | `INFO` | DEBUG / INFO / WARN / ERROR；未知值回落 INFO。直接修改 environment 中的 LOG_LEVEL。 |
| `IOSBK_CONFIGS_DIR` | `/configs` | `/configs` | 绝对目录且不能为 /；需可写、持久化。 |
| `IOSBK_BACKUPS_DIR` | `/backups` | `/backups` | 绝对目录且不能为 /；设备保存位置必须在此目录内。 |
| `IOSBK_AUTH_ENABLED` | `false` | `true` | true / false。推荐部署始终开启认证。 |
| `IOSBK_ADMIN_PASSWORD_FILE` | `未设置` | `未设置` | 可选自定义密码文件的绝对路径，至少 24 字节；默认自动管理密码和摘要，见下文。读取时去掉末尾一组 CR/LF。 |
| `IOSBK_SECRET_KEY` | `未设置` | `未设置` | 可选环境主密钥：标准 Base64 编码的恰 32 字节；与密钥文件覆盖互斥。 |
| `IOSBK_SECRET_KEY_FILE` | `未设置` | `未设置` | 可选主密钥文件的容器内绝对路径；均未设置时自动读取或首次创建 /configs/secret_key。 |
| `IOSBK_INSECURE_ALLOW_REMOTE` | `false` | `false` | true / false；认证关闭时允许非回环监听的显式例外，不是安全访问方式。 |
| `IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS` | `false` | `false` | true / false；控制本地解包和删除备份；整机恢复仍有独立设备开关。 |
| `IOSBK_MAX_HEAVY_JOBS` | `1` | `1` | 整数 1–2；当前限制同时运行的备份任务，不能推断为所有实验操作的总并发限制。 |
| `IOSBK_PRESENCE_INTERVAL` | `10s` | `10s` | 1s–10m；设备发现/在线检查周期，不能证明传输活跃。 |
| `IOSBK_DEVICE_DISCONNECT_GRACE` | `30s` | `30s` | 5s–10m；设备短暂不可见的宽限期，不能恢复已经终止的任务。 |
| `IOSBK_BACKUP_AUTHORIZATION_TIMEOUT` | `5m` | `5m` | 1s–24h；等待手机授权期间连续无活动的期限。 |
| `IOSBK_BACKUP_INACTIVITY_TIMEOUT` | `10m` | `10m` | 1s–24h；发送或接收阶段连续无活动的期限。 |
| `IOSBK_BACKUP_PREPARATION_TIMEOUT` | `30m` | `30m` | 1s–24h；建立会话/等待手机处理阶段连续无活动的期限。 |
| `IOSBK_SCHEDULER_INTERVAL` | `30s` | `30s` | 1s–24h；检查自动备份条件的周期，不是设备备份间隔。 |
| `IOSBK_WIFI_BACKEND` | `netmuxd` | `netmuxd` | netmuxd / usbmuxd2；Wi-Fi 后端。通常保留默认值。 |
| `IOSBK_NETMUXD_LOG_LEVEL` | `warn` | `warn` | error / warn / info / debug / trace；仅 netmuxd 日志。 |
| `IOSBK_WIFI_POWER_ASSERTION` | `true` | `true` | true / false；程序自动请求 Wi-Fi 防休眠，不要求用户持续亮屏；仍需按手机提示完成授权，不保证所有设备与网络均可无人值守。 |
| `IOSBK_MIN_FREE_BYTES` | `0` | `0` | 无符号 64 位整数；当前仅解析保存，未接入空间检查。设置它不会阻止磁盘写满。 |
| `IOSBK_WEBHOOK_ALLOW_CIDRS` | `—` | `—` | 逗号分隔的私网 CIDR；只放行必要范围，回环/链路本地等仍禁止。 |

认证启用时按下文规则使用已有密码，或为新实例生成密码；找回方法见[安装与登录](installation.zh-CN.md)。Web 会话有效期为 12 小时且保存在内存中，重启后需要重新登录。

镜像直接在 [compose.yaml](../../compose.yaml) 的 `services.iosbackup.image` 中设置，默认为 `ghcr.io/razeencheng/iosbackup:latest`。需要固定版本时，将该值改为官方版本标签或 digest；改回 `latest` 即可继续跟随更新。首次部署和自动初始化步骤见[安装指南](installation.zh-CN.md)。不要通过修改 `TZ` 来调整设备计划；计划和用户可见时间使用北京时间（UTC+8）。

## 高级凭据配置

默认部署无需设置本节变量。应用将专用配置目录设为 `0700`，生成的密码、摘要和主密钥文件为 `0600`。首次保存密码和密钥时，要求配置所在的**本地文件系统支持硬链接** ；不支持时启动报错，不自动退回可能覆盖现有文件的写入方式。NAS 的共享文件夹 ACL 也应限制为管理员访问。

默认主密钥 `secret_key` 与密文 `secrets.enc` 同在配置目录，方便完整备份；这种加密可以避免 API 输出和单独的密文文件直接暴露秘密，**不能抵御整个配置目录泄露** 。需要分开保管时，使用下述外部密钥文件，保护宿主机和备份同样必要。

### 管理员密码的读取顺序和自定义

1. 非空 `IOSBK_ADMIN_PASSWORD_FILE` 优先，路径必须是容器内的绝对文件路径；文件缺失、不可读或密码少于 24 字节时启动失败。
2. 没有自行指定密码文件时保留已有 `auth_credentials.json`；默认 `admin_password` 若也存在，必须与摘要匹配。
3. 只有默认 `admin_password` 时复用它并创建摘要。两者都不存在时才生成随机密码、受限文件和摘要；只有这一次会把密码写入日志。认证关闭时不生成管理员密码。

默认密码文件与摘要不匹配时，恢复匹配副本或改用单独创建并在配置中指定的密码文件；不要直接覆盖默认文件或删除摘要试错。需要更换管理员密码时，可以在**已经初始化的部署目录** 运行以下高级示例（仅本示例需要主机 OpenSSL）。配置目录通常属于容器 root，因此使用 sudo 写入，不更改整个目录属主：

```bash
sudo sh -c '
  set -eu
  umask 077
  set -C
  openssl rand -base64 32 > data/configs/custom_admin_password
'
```

此命令拒绝覆盖已有文件。若中途失败，先检查该新文件再处理，不修改原默认凭据。将下面一项合并到现有 Compose 的 `environment`，保留其他设置：

```yaml
services:
  iosbackup:
    environment:
      IOSBK_ADMIN_PASSWORD_FILE: "/configs/custom_admin_password"
```

随后执行本页开头的配置校验和容器重建，再用宿主机 `sudo cat data/configs/custom_admin_password` 读取新密码登录。不要公开输出。已选定自定义文件以后，文件内容变化需要重启；变量或挂载变化需要重建容器。将此项设为 `""` 会重新使用保留的默认密码，不会把自定义密码自动写回摘要。

### 自定义主密钥

- 非空 `IOSBK_SECRET_KEY` 与非空 `IOSBK_SECRET_KEY_FILE` **只能选择一个** ，同时配置会启动失败；空值视为未配置。密钥必须是标准 Base64 编码的恰 32 个随机字节。
- 自行填写的环境密钥或密钥文件配置无效时，程序报错，不自动回退。文件路径是容器内绝对路径；外部文件需要挂载，不能只填写宿主机路径。自行指定的密钥不会自动复制到默认文件。
- 两者都未设置时读取 `/configs/secret_key`。该文件也不存在且没有 `secrets.enc` 时才生成；已有密文缺少原密钥，或任意已保存秘密无法解密时，启动失败并保留文件。
- 自行指定密钥时，程序不会改用默认 `secret_key`；更换密钥的保存位置时必须使用**同一把原密钥** ，不能通过换成另一把随机密钥来更新密钥。

需要将主密钥与配置目录分开保存时，先准备包含**原密钥** 的受保护宿主机文件，再将以下条目合并到现有服务，不替换完整 Compose。把示例宿主路径换成实际文件；新实例也可由高级管理员事先提供符合格式的随机密钥。

```yaml
services:
  iosbackup:
    environment:
      IOSBK_SECRET_KEY: ""
      IOSBK_SECRET_KEY_FILE: "/run/secrets/iosbackup-secret-key"
    volumes:
      - /absolute/protected/path/secret_key:/run/secrets/iosbackup-secret-key:ro
```

保持文件仅管理员可读。等活动任务结束，备份原配置，核对 Compose 与 override 文件没有冲突，然后重建容器；确认原秘密可读后才继续使用。外部密钥要单独备份，不能只复制 `/configs`。生成密码、密钥、认证/CSRF 文件和首次日志都不应进入 Git、镜像构建上下文或公开问题报告。

## 每台设备的设置

在设备的“备份设置”中调整，完整任务示例见[自动备份](scheduling.zh-CN.md)。

| 字段 | 新设备默认值 | 含义 |
|---|---|---|
| `start_time / end_time` | `18:00 / 06:00` | 北京时间 HH:mm；可跨午夜。 |
| `backup_interval` | `24` | 最小间隔，单位小时；1–720。UI 提供 6/12/24/48 小时。 |
| `min_battery_level` | `20` | 0–100；手动备份也会检查电量/充电条件。 |
| `only_when_charging` | `true` | 仅在充电时开始备份。 |
| `auto_backup_enabled` | `false` | 保存后由后续调度检查决定是否启动，不是立即执行按钮。 |
| `backup_directory` | `/backups` | 默认是备份根目录；填写容器路径，不能使用宿主机路径或逃出根目录。 |
| `network_address` | `空 / empty` | 可选设备 IP 字面量，不接受域名和回环等特殊地址。 |
| `restore_enabled` | `false` | 独立的每设备整机恢复许可；常规备份保持关闭。 |

`udid`、`name`、`device_type` 为设备身份/展示信息；`last_backup`、`last_backup_connection` 为最近成功记录；`removed_at` 记录移除状态。它们不应作为调度开关手动修改。重新添加已移除设备后，原有自动计划可能继续执行。

## 持久化路径

下列宿主机路径相对于 Compose 部署目录；自定义挂载以实际配置为准。迁移时参见[实例备份与恢复](instance-recovery.zh-CN.md)。

| 宿主机 | 容器内 | 用途 |
|---|---|---|
| `data/backups/` | `/backups` | 各设备 <UDID>/ 备份集；不是自动保存多代快照。 |
| `data/configs/backup_configs.json` | `/configs/backup_configs.json` | 设备设置和最近成功时间；版本化 JSON，勿在运行时手改。 |
| `data/configs/notification_configs.json` | `/configs/notification_configs.json` | 通知渠道、规则及模板；秘密另存。 |
| `data/configs/secrets.enc` | `/configs/secrets.enc` | 加密保存备份密码及通知秘密，需要原主密钥。 |
| `data/configs/admin_password` | `/configs/admin_password` | 首次自动生成的管理员明文密码，0600；显式自定义来源可能位于其他路径。 |
| `data/configs/auth_credentials.json` | `/configs/auth_credentials.json` | 默认认证摘要，不能从摘要还原密码。 |
| `data/configs/secret_key` | `/configs/secret_key` | 默认自动生成并复用的主密钥，0600；与 secrets.enc 一起备份。显式外部来源另行备份。 |
| `data/configs/csrf_secret.json` | `/configs/csrf_secret.json` | 状态修改请求的 CSRF 秘密。 |
| `data/lockdown/` | `/var/lib/lockdown` | 设备信任/配对记录，含敏感材料；不要用清空来常规排错。 |
| `compose.yaml` | `无 / none` | 镜像、进程参数和挂载配置；与数据目录一起备份；额外使用的 Compose 配置文件也需保存。 |

临时目录 `/tmp` 和 `/var/run` 由 Compose 挂载为内存文件系统，不是备份位置。应用日志写到标准输出；当前无公开的日志下载 API。

需要定位无活动超时和错误码时，继续阅读[状态与错误参考](states.zh-CN.md)；需要修改密码时，见[安装与管理员访问](installation.zh-CN.md)及[备份加密](encryption.zh-CN.md)。
