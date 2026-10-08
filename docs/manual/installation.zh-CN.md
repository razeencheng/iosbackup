# 在 Linux 安装、登录并保护管理员访问

[English](installation.md) · [返回操作手册](../OPERATIONS.zh-CN.md)

## 目标与前提

按本页步骤安装后，你可以登录控制台，找到三个数据目录及密码、密钥文件，然后继续 USB 配对。本页针对**全新 Linux Docker Compose 部署** ；已有实例请转到[升级](upgrade.zh-CN.md)或[实例恢复](instance-recovery.zh-CN.md)。群晖使用[独立指南](synology.zh-CN.md)。

准备 Docker Engine、Compose 插件，以及容量足够、受保护的本地存储；下文诊断命令还使用主机上的 curl。你的账号需要有 Docker 操作权限。默认安装只需配套 Compose 文件，无需 Git、OpenSSL 或预先生成密钥。

## 1. 保存 Compose 并确认部署位置

下载与所用镜像配套的 [compose.yaml](../../compose.yaml)，保存到新的 `$HOME/iosbackup-deploy` 目录，或目标磁盘上的其他新目录；后续命令始终在这个实际目录执行。已有数据目录不能当作全新实例处理。

```bash
cd "$HOME/iosbackup-deploy"
docker version
docker compose version
ls -ld /dev/bus/usb /run/udev
df -h .
df -i .
docker compose config --quiet
```

确认空间能容纳完整设备备份并留有余量。`config --quiet` 检查 Compose 语法；不要公开完整 `docker compose config` 输出，高级配置可能展开秘密。镜像不存在、没有访问权限或架构不匹配时，向维护者核对来源，不替换成陌生镜像。

| 设置 | 本教程要求 |
|---|---|
| 三个可写挂载 | `./data/backups:/backups`、`./data/configs:/configs`、`./data/lockdown:/var/lib/lockdown` |
| 设备挂载 | `/dev/bus/usb:/dev/bus/usb` 与 `/run/udev:/run/udev:ro`；源路径必须真实存在 |
| 临时目录 | `/tmp`、`/var/run` 使用 tmpfs；需要持久保存的数据写入上述三个数据卷 |
| 权限与网络 | 保留配套配置中的 `privileged: true`、`network_mode: host`，不添加 `ports` |
| 管理员认证 | 保留 `IOSBK_AUTH_ENABLED=true`；默认无需填写密码文件或主密钥变量 |
| 端口 | Compose 默认 `9000`，进程不使用 Compose 时默认 `8080` |

默认监听所有主机接口。确认端口未占用，限制防火墙访问源；跨主机访问优先使用 HTTPS 反向代理或受保护隧道。不要创建公网端口转发或关闭认证。更换端口、目录和凭据见[高级配置](configuration.zh-CN.md)。

## 2. 启动并保存首次密码

在 **Linux 主机的部署目录** 运行：

```bash
docker compose up -d
docker compose ps
docker compose logs iosbackup
```

没有既有凭据且未自行指定密码时，程序生成随机管理员密码，保存为 `data/configs/admin_password`，并在 `auth_credentials.json` 保存摘要。密码只在生成时写入一次启动日志。没有自行指定或已保存的密钥、且不存在 `secrets.enc` 时，程序才生成 `data/configs/secret_key`；主密钥不会写入日志。重启、升级或重新创建容器会复用这些文件。

应用将专用配置目录设为 `0700`，新生成的秘密文件为 `0600`。默认容器用户是 root，因此宿主机读取文件通常需要 sudo。`data/backups` 和 `data/lockdown` 的访问权限仍需按主机/NAS 的文件夹权限检查；不要假设它们自动变成仅所有者可访问。保护整个部署目录及其备份。

![部署目录中的 backups、configs 和 lockdown](images/17-iosbk-dir.png)


## 3. 登录并核对实例

1. **浏览器** 打开已选定的受保护地址，临时可信局域网示例为 `http://<主机地址>:9000/`。HTTP 不加密密码。
2. 输入首次日志中的管理员密码，点击 **“验证并进入”** 。网页登录只填写密码；API Basic Auth 用户名固定为 `iosbackup`。
3. 核对页面版本，打开 **“首次使用向导”** ，先选 **“暂时跳过，只用 USB”** ，继续 [USB 配对与第一次备份](usb-backup.zh-CN.md)。

冷启动可能需要数十秒。需要诊断时，在主机运行以下命令：第一条会在限定时间内重试连接，第二条查询版本：

```bash
curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
curl --fail --user iosbackup http://127.0.0.1:9000/api/version
```

第二条命令交互询问密码，避免将密码写进命令行。记录 `version`、`commit`、`build_date`；`/healthz` 只检查 HTTP 响应，不检查 USB、空间或备份成功。浏览器会话有效期为 12 小时，保存在内存中；重启后需要重新登录。退出使用菜单中的 **“退出登录”** 。


![管理员登录页，密码输入框为空](images/18-iosbk-login.png)

## 找回或更换管理员密码

找不到首次启动日志时，在 **Linux 宿主机的受保护终端、部署目录内** 读取默认文件：

```bash
sudo cat data/configs/admin_password
```

不要把输出放入 Issue、截图或共享终端记录。镜像基于 scratch，不含 shell 或 cat，不能在容器里执行同一命令。自行指定了密码文件的实例，应读取指定的文件。

需要更换管理员密码时，按[高级凭据配置](configuration.zh-CN.md#高级凭据配置)创建**独立的自定义密码文件** 并显式指定 `IOSBK_ADMIN_PASSWORD_FILE`，然后重建容器。已有默认密码和摘要都应保留；直接改默认 `admin_password` 会造成摘要不匹配并拒绝启动。不要删除整个 `/configs`，也不要改主密钥或设备备份密码来修复登录。

## 失败分支

| 现象 | 检查与下一步 |
|---|---|
| 容器持续退出 | 在部署目录查看日志，检查错误指出的密码、主密钥、路径或端口。自行填写的配置有误时，程序不会改用默认值继续启动；先修复具体错误。 |
| 已有 `secrets.enc`，却缺少密钥或解密失败 | 恢复匹配的 `secret_key` 或原外部密钥；程序不会生成替代密钥。见[实例恢复](instance-recovery.zh-CN.md)。 |
| 默认密码与认证摘要不一致 | 恢复匹配的默认文件，或使用高级自定义密码文件；不删除摘要试错。 |
| 页面打不开 | 主机先访问 `/healthz`，再检查客户端网络、防火墙、端口及代理；主机端失败优先查看日志。 |
| “用户名或密码不正确” | 确认打开的是正确实例，并核对它使用的密码文件；不要输入手机锁屏密码或备份加密密码。修改自定义文件后需要重启。 |
| `permission denied` / `read-only file system` | 核对三个绑定挂载的实际路径、可写性和 NAS ACL。 |
| 初始化文件报硬链接不支持 | 配置目录需要支持硬链接的本地文件系统，见[配置参考](configuration.zh-CN.md)；程序不会改用不安全的写入方式。 |
| mux 服务端口冲突 | 检查宿主机 mux 服务或其他 host 网络实例，先确认影响和活动任务，再安排处理。 |

能够登录、确认版本和数据目录，并且主机上的健康检查通过后，安装就完成了。此时还没有测试手机备份。继续[第一次 USB 备份](usb-backup.zh-CN.md)，并安排[完整实例备份](instance-recovery.zh-CN.md)。
