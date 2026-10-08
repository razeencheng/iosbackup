# 升级应用，并在必要时回滚

[English](upgrade.md) · [返回操作手册](../OPERATIONS.zh-CN.md)

## 目标与准备

让已有部署运行目标版本，同时保留设备配置、配对记录、备份数据和秘密。以下步骤适用于通用 Linux Compose；自定义项目名或配置文件需在每条命令中使用原来的 `-p` / `-f` 参数。

开始前阅读目标版本的 [CHANGELOG](../../CHANGELOG.md)、[功能状态](../FEATURE_STATUS.zh-CN.md)和迁移要求。准备准确的旧镜像引用/摘要、可用的新镜像、原主密钥和[一致的实例副本](instance-recovery.zh-CN.md)。没有发布说明或无法确认数据格式兼容时，先用独立副本测试，不直接升级唯一生产实例。

## 升级步骤

1. 在 Web UI 关闭各设备“自动备份”并保存原计划，等待所有已启动操作结束。确认界面空闲，查看日志；`/healthz` 只证明 HTTP 有响应。不要在备份、检查、加密、解包或恢复进行时重建容器。
2. 在主机部署目录记录当前版本和镜像。默认容器名为 `iosbackup`，如已修改请替换。

   ```bash
   docker inspect --format '{{.Config.Image}} {{.Image}}' iosbackup
   iosbk_old_image_id=$(docker inspect --format '{{.Image}}' iosbackup)
   docker image inspect --format '{{json .RepoDigests}}' "$iosbk_old_image_id"
   curl --user iosbackup http://127.0.0.1:9000/api/version
   ```

   保存输出中的完整 `ghcr.io/razeencheng/iosbackup@sha256:…` 引用用于回滚；不能只记录 `latest`。本地构建镜像没有 RepoDigest 时，保留原镜像及其构建来源，先确认可恢复后再升级。

   保存所有使用的 Compose 配置文件和镜像记录到受保护存储；完整环境输出可能包含秘密，不要公开。按照[实例备份](instance-recovery.zh-CN.md)停服并保存三个完整卷（含配置目录的 `secret_key`、密码和摘要）及外部凭据。归档完成后可让旧服务继续运行，但自动计划保持关闭，升级前再确认空闲。
3. 核对 Compose 使用的镜像并拉取更新。默认 `image` 使用 `latest`，不需要每次修改版本标签。

   ```bash
   docker compose config --quiet
   docker compose config --images
   docker compose pull
   ```

   默认输出应为 `ghcr.io/razeencheng/iosbackup:latest`。若之前固定了版本，直接将 Compose 中的 `services.iosbackup.image` 改回这个值，保留其他设置。遇到镜像不存在、权限不足、网络错误或平台不匹配时，先解决拉取问题，再继续重建。
4. 保留已有配置、默认 `secret_key` 及实际使用的自定义密码和主密钥。阅读版本说明后逐项合并必要 Compose 变更，不用新样例覆盖原挂载、密码和项目名。确认没有活动操作后重建。

   ```bash
   docker compose up -d
   docker compose ps
   ```

   `latest` 不会自动替换已经运行的容器，需要执行拉取和重建操作。需要审核并固定特定版本时，直接将 Compose 的 `image` 改为官方版本标签或 digest，再按相同步骤操作。不要将完整 `docker compose config` 输出作为排错附件。
5. 核对实际运行的版本与启动日志。

   ```bash
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
   curl --user iosbackup http://127.0.0.1:9000/api/version
   docker compose logs --tail=200 iosbackup
   ```

   `/api/version` 的版本/commit 应与该镜像发布说明一致。登录，检查设备设置、已保存秘密的可用状态、原备份路径及配对。若出现配置格式或秘密读取错误，不要继续发起备份。
6. 先用一台非关键设备完成[手动 USB 备份](usb-backup.zh-CN.md)，再检查[备份信息和文件清单](inspection.zh-CN.md)。看到“备份已完成”并核对最近成功时间后，逐台恢复原自动计划；Wi-Fi 和通知需要各自的真实事件验证。

**预期结果：** 实际运行的是目标版本，原配置/配对仍在，选定设备完成真实备份；记录加密清单、Wi-Fi 等尚未验证项。仅仅能打开网页，还不能确认升级成功。

## 回滚步骤

1. 遇到无法启动、配置不兼容、原数据不可读或原本正常的主要备份功能失效时，先停用计划。等待可正常结束的活动操作完成并保存最小日志；不要重复重启来打断仍有进展的任务。保留升级后的整个目录/快照，便于调查及保住升级后新增数据。
2. 阅读版本说明，确认旧应用可以读取当前持久化格式。若不兼容或无法确认，**停止此处的原地回滚** ，按[实例恢复](instance-recovery.zh-CN.md)把升级前副本恢复到独立目录，以旧镜像隔离验证；不要覆盖现有卷。
3. 格式兼容时，将 Compose 的 `services.iosbackup.image` 改为提前记录的旧 tag/digest（不是 `latest`），其他设置、管理员密码和主密钥保持不变。先确认镜像可用，再切换。

   ```bash
   docker compose config --quiet
   docker compose config --images
   docker compose pull
   docker compose up -d
   curl -fsS --retry 12 --retry-connrefused --retry-delay 5 --retry-max-time 90 http://127.0.0.1:9000/healthz
   curl --user iosbackup http://127.0.0.1:9000/api/version
   docker compose logs --tail=200 iosbackup
   ```

   若固定 digest 已在本机但仓库暂不可达，先核验本地镜像确实匹配记录，再决定使用它；不要把 pull 失败当成回滚成功。
4. 按升级时相同顺序验证登录、配置、配对、只读信息和一次 USB 备份，再开启自动计划。仍失败时保留两侧数据，转[排错](troubleshooting.zh-CN.md)或提交脱敏问题。

**预期结果：** 应用已回到原版本，主要操作也再次测试通过。切换镜像不会自动撤销磁盘数据变化；只有已确认损坏或版本不兼容且另有可恢复副本时，才安排独立的数据恢复。

下一步：记录失败版本、运行 commit、复现条件和回滚结果。确认新版本稳定前保留升级前副本和失败现场，不要刚回滚并启动成功就清理这些副本和记录。
