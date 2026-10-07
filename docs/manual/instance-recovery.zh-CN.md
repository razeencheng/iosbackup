# 备份、恢复和迁移整个部署实例

[English](instance-recovery.md) · [返回操作手册](../OPERATIONS.zh-CN.md)

## 目标与适用条件

保存应用的设备备份、配置、配对记录和密钥，在独立目录中恢复并核对，然后决定是否接替原实例。本页恢复的是 NAS 上的部署实例；向 iPhone 写回数据属于另一个[实验操作](experimental.zh-CN.md)。

以下命令面向使用仓库 `compose.yaml`、`./data` 布局的 Linux Docker Compose 部署，需要管理员权限、Python 3 和支持 ACL/xattr 的 GNU tar。自定义挂载、外部密码文件或 NAS 共享文件夹应先写出实际路径清单，不能直接套用默认目录。群晖专用备份工具可以保留共享文件夹 ACL，但仍需保存下列全部数据和配置。

以下步骤已对照当前配置格式和程序行为核对，但尚未按这些步骤完整测试跨主机恢复。请先用独立副本演练，保留原实例。

## 一、保存一个一致的实例副本

1. 在 Web UI 逐台关闭“自动备份”并保存，记下原计划以便以后恢复。等待已开始的备份、检查、加密、解包或恢复结束；关闭自动备份不会取消正在运行的任务。查看界面和日志，不能只凭 `/healthz` 判断空闲。
2. 在部署目录记录实际镜像、版本、挂载与 Compose 项目名。下面的命令只输出镜像引用；不要把完整 `docker inspect` 或 `docker compose config` 结果上传到 Issue，它们可能包含主密钥。

   ```bash
   docker compose ps
   docker inspect --format '{{.Config.Image}} {{.Image}}' iosbackup
   curl --user iosbackup http://127.0.0.1:9000/api/version
   ```

   `curl` 会提示输入管理员密码。若容器改过名称，替换 `iosbackup`；端口也以实际配置为准。把准确镜像引用/摘要、版本和所有 Compose 配置文件保存在受保护记录中。
3. 准备另一块有足够空间的受保护存储。下面的 `/mnt/protected-backup` 必须替换为**已挂载、已加密且可信** 的备份目标，不能在被归档的 `data` 目录内部创建归档。`tar.gz` 只是压缩包，不提供加密。

   ```bash
   set -eu
   umask 077
   archive_dir=$(mktemp -d /mnt/protected-backup/iosbackup-state.XXXXXX)
   docker compose stop
   docker compose ps -a
   ```

   确认服务已停止；如果还有相关容器或任务运行，先查清原因。迁移期间不要让另一个实例同时使用这些目录。
4. 在同一个终端、同一个部署目录归档。示例包含 Compose 和三个完整数据目录，并按实际存在情况加入常用 Compose override 文件；无需创建缺失的文件。自定义 `-f` 文件、外部密码/密钥文件或额外挂载必须另行加入清单和受保护副本。

   ```bash
   set -- compose.yaml data/backups data/configs data/lockdown
   for iosbk_extra in compose.override.yaml compose.override.yml docker-compose.override.yaml docker-compose.override.yml; do
     if [ -f "$iosbk_extra" ]; then set -- "$@" "$iosbk_extra"; fi
   done
   sudo tar --acls --xattrs --numeric-owner -czpf "$archive_dir/state.tar.gz" "$@"
   sudo chmod 600 "$archive_dir/state.tar.gz"
   sudo tar -tzf "$archive_dir/state.tar.gz" > "$archive_dir/contents.txt"
   sudo sha256sum "$archive_dir/state.tar.gz" > "$archive_dir/state.sha256"
   ```

   任一命令失败时，保留错误信息并停止后续迁移。检查清单包含整个 `data/configs`：默认管理员密码和认证摘要、`secret_key`、CSRF 状态、备份配置、`secrets.enc`（使用过秘密时），以及配对记录和实际备份文件。使用自定义密码或外部主密钥时，将实际使用的凭据文件一并保存。清单中的路径同样属于私人信息。
5. 在受保护存储上另存镜像/版本记录，并确认归档可以读取。若本次只是备份，可用 `docker compose start` 重启原服务；恢复已记录的计划前先核对状态。若即将迁移，原服务保持停止。不要删除原目录或唯一已知可用副本。

**预期结果：** 拥有停服时的一致副本、准确的镜像版本或摘要和可用的原始主密钥。归档列表与校验和通过只证明文件可以读，不证明实例已恢复或 iPhone 可恢复。

## 二、恢复到新的空目录，先隔离检查

1. 在目标主机准备新的空目录，不能覆盖正在使用的部署目录。先核对归档校验和，检查它只包含你刚保存的预期相对路径，不接受来源不明的归档。以下路径均为示例。

   ```bash
   (
     set -eu
     umask 077
     sudo mkdir /srv/iosbackup-recovery
     sudo chown "$(id -u):$(id -g)" /srv/iosbackup-recovery
     chmod 700 /srv/iosbackup-recovery
     cd /srv/iosbackup-recovery
     sudo tar --acls --xattrs --numeric-owner -xzpf /mnt/protected-backup/SELECTED/state.tar.gz
   )
   ```

   只有上面的命令块成功结束才继续；目录已存在或解包失败时停止，不进入后续步骤。成功后在当前终端进入新目录，后续命令均在此运行：

   ```bash
   cd /srv/iosbackup-recovery
   ```

   解包保留原数值属主、模式和受支持的 ACL/xattr。核对目标主机 UID/GID 与文件系统 ACL；不要用递归 `chmod 777` 解决差异。保留配置目录的 `0700`、密码/主密钥/摘要等文件的 `0600` 权限。另行核对备份和配对目录只允许管理员访问，不假设应用会自动收紧它们的权限。
2. **先不要启动恢复的正式 Compose。** 核对默认 `secret_key` 和密码/摘要已恢复，或原外部凭据来源及其值保持不变，卷路径确实指向新副本。继续使用原登录密码。迁移不是重新初始化：缺少密钥时找回原始副本，不要让程序为恢复的实例生成新的密码或主密钥。
3. 关闭恢复副本内所有自动备份和恢复开关，避免旧归档含启用计划。以下脚本只操作当前新目录中的配置：保存原文件副本，只接受当前 `schema_version=1` 格式，保留其他字段，并拒绝陌生格式。原实例必须停止，当前恢复实例尚未启动。

   ```bash
   sudo python3 - <<'PY'
   import json, os, shutil, stat, tempfile
   from pathlib import Path
   p = Path('data/configs/backup_configs.json')
   if not p.is_file() or p.is_symlink():
       raise SystemExit('未找到普通备份配置文件；先核对归档和路径，不启动服务')
   data = json.loads(p.read_text())
   if not isinstance(data, dict):
       raise SystemExit('配置格式未知')
   if data.get('schema_version') != 1 or not isinstance(data.get('configs'), dict):
       raise SystemExit('配置版本未知；停止并查阅对应版本说明')
   devices = data['configs']
   if any(not isinstance(v, dict) for v in devices.values()):
       raise SystemExit('设备配置格式未知')
   saved = p.with_name(p.name + '.before-recovery')
   if saved.exists():
       raise SystemExit('原配置副本已存在；先核对，不覆盖')
   shutil.copy2(p, saved)
   for cfg in devices.values():
       cfg['auto_backup_enabled'] = False
       cfg['restore_enabled'] = False
   original = p.stat()
   fd, temporary = tempfile.mkstemp(dir=p.parent, prefix='.recovery-')
   try:
       with os.fdopen(fd, 'w') as f:
           os.fchmod(f.fileno(), stat.S_IMODE(original.st_mode))
           os.fchown(f.fileno(), original.st_uid, original.st_gid)
           json.dump(data, f, ensure_ascii=False, indent=2)
           f.write('\n')
           f.flush()
           os.fsync(f.fileno())
       os.replace(temporary, p)
   finally:
       if os.path.exists(temporary):
           os.unlink(temporary)
   print('已在恢复副本中关闭自动备份与恢复开关；原文件副本已保留')
   PY
   ```

   脚本保留文件属主与模式；如部署依赖文件级特殊 ACL/xattr，应使用相应管理工具恢复这些属性。无配置文件可能意味着实例从未配置设备，也可能是归档不完整；先确认原因，不创建猜测的配置。
4. 建立独立的 `compose.recovery.yaml`，**单独使用它，不与原 Compose 合并** 。该验证实例使用内部桥接网络，无 USB/udev、无特权模式，只将网页映射到目标主机回环地址，不能承担真实设备备份。

   ```yaml
   services:
     iosbackup:
       image: "REPLACE_WITH_RECORDED_IMAGE"
       restart: "no"
       tmpfs:
         - /tmp:size=64m,mode=1777
         - /var/run:size=16m,mode=0755
       ports:
         - "127.0.0.1:19000:9000"
       environment:
         PORT: "9000"
         LOG_LEVEL: "INFO"
         IOSBK_LISTEN_ADDR: "0.0.0.0"
         IOSBK_AUTH_ENABLED: "true"
         IOSBK_CONFIGS_DIR: "/configs"
         IOSBK_BACKUPS_DIR: "/backups"
         IOSBK_ENABLE_EXPERIMENTAL_OPERATIONS: "false"
       volumes:
         - ./data/backups:/backups
         - ./data/configs:/configs
         - ./data/lockdown:/var/lib/lockdown
       networks: [recovery]
   networks:
     recovery:
       internal: true
   ```

   只连接这个内部网络，不挂载宿主机套接字、设备或其他网络。确认主机上没有把这个网络额外路由到手机所在网段的规则。若原配置使用 `/backups` 之外的允许路径，先在验证配置中映射相同容器路径到独立副本，不能挂载原生产目录。
   此配置直接使用已恢复到 `/configs` 的默认密码和主密钥。原实例使用自定义密码或主密钥时，按[高级凭据配置](configuration.zh-CN.md#高级凭据配置)补上原配置，保持密码和密钥值不变。外部文件要只读挂载其**恢复副本** ，并沿用原容器路径；未恢复这些文件前不要启动。不能同时提供密钥环境值和密钥文件。
5. 将 `compose.recovery.yaml` 中的 `image: "REPLACE_WITH_RECORDED_IMAGE"` 替换为记录的原镜像完整 tag 或 digest；不要使用 `latest`，也不需要另外创建配置文件。确认镜像能取得、原凭据来源已恢复，再启动。

   ```bash
   docker compose -p iosbackup-recovery -f compose.recovery.yaml pull
   docker compose -p iosbackup-recovery -f compose.recovery.yaml up -d
   docker compose -p iosbackup-recovery -f compose.recovery.yaml logs --tail=100
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:19000/healthz
   curl --user iosbackup http://127.0.0.1:19000/api/version
   ```

   在目标主机浏览器打开 `http://127.0.0.1:19000/`；远程主机可使用 SSH 本地转发再打开同一地址：`ssh -L 19000:127.0.0.1:19000 <管理员>@<目标主机>`。不要开放验证端口到公网。
6. 核对原密码可登录、没有意外生成新凭据的日志、版本匹配、设备配置和已移除设备状态保留、自动计划关闭、日志无配置/秘密解密错误。设备离线及发现失败在此隔离环境中属于预期；不能据此判定配对损坏。秘密输入框为空是保护行为，不证明秘密丢失。这里不测试真实通知，也不证明加密清单可以解密。

**预期结果：** 恢复副本能在隔离状态读取应用配置并登录；原始归档和生产目录均保留。记录检查项目及未验证项。

## 三、切换为正式实例并验证设备

1. 确认旧主机上的原服务已停止，且不会被其他 Compose 项目、系统任务或重启策略再次启动。切换前保留旧目录；不要在两个实例中开启同一设备的计划。
2. 停止并移除隔离容器及其内部网络；下面命令不删除绑定挂载的数据目录。

   ```bash
   docker compose -p iosbackup-recovery -f compose.recovery.yaml down
   ```

3. 审核恢复的正式 `compose.yaml`：镜像、端口、项目名、USB/udev、三个独立副本挂载及原密钥正确，实验开关关闭。先只接入一台非关键设备，按[安装指南](installation.zh-CN.md)启动正式服务。
4. 核对登录和版本，再核对 USB 在线与配对、原备份信息和[文件清单](inspection.zh-CN.md)。不要因配对失败就清空 lockdown；先查挂载、信任和设备状态。遇到 `MBErrorDomain/207`，按[加密指南](encryption.zh-CN.md)检查原密码/密钥，不以重新生成密钥修复。
5. 在保持自动计划关闭时，完成一次[手动 USB 备份](usb-backup.zh-CN.md)，确认页面显示备份已完成，核对最近成功时间，并记录哪些备份信息和清单能读取。随后一次只为一台设备恢复[自动计划](scheduling.zh-CN.md)，观察真实触发，再测试通知。通知测试会实际发送消息。

失败时先关闭新实例，保留日志和新目录，再决定用原实例继续运行；切回原实例前确认新实例已停。不要把恢复副本直接覆盖回原实例，也不要用旧归档覆盖迁移后新增的数据。

下一步：记录这次恢复演练的镜像、日期、检查结果和未验证内容；按[升级与回滚](upgrade.zh-CN.md)维护版本。实例恢复成功仍不等于已完成 iPhone 整机恢复演练。
