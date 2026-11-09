# Changelog

This project follows Semantic Versioning. Release dates and source metadata are frozen in [`release/manifest.env`](release/manifest.env); this file does not duplicate the current release date.

## v1.5.6

### 中文

**v1.5.6 — 修复大文件历史导入与 CSV 清单完整性**

- 修复 v1.5.5 导入已有备份时，超过 8 MiB 的元数据可能导致存储准备失败、后续备份无法启动的问题。复制、历史保存和 TAR 导出改用流式 SHA-256，不再受 plist 解析大小限制。
- 新增 `IOSBK_PLIST_MAX_MIB`，默认 `64`，接受 `1–256` 的整数，限制检查和浏览时单个 plist 的输入及转换输出。超过上限的备份仍可保存为可导出的待确认历史点，不参与增量基线选择。
- 改善失败提示：显示发生问题的 plist 文件名和本次使用的上限；历史导入失败时标明“导入已有备份”阶段，并区分权限不足、只读挂载和空间不足。保留已有未完成策略，支持重试存储准备。
- 修复在线 CSV 清单中的长记录可能被遗漏的问题。单条记录支持到 1 MiB，超过上限时明确报错；下载已开始时中断传输，让客户端识别下载不完整。
- 同步中英文界面提示和配置、历史备份、浏览文档，沿用现有配置和备份格式。

### 升级须知

- 已在 v1.5.5 遇到存储准备失败的设备，升级到 v1.5.6 后，在“设备设置 → 备份保留策略”点击“重试存储准备”。不需要删除原备份或重新开启策略。
- 通常无需新增配置。若需要解析超过 64 MiB 的 plist，可在 Compose 的 `environment` 中设置 `IOSBK_PLIST_MAX_MIB: "128"` 并重建容器。该值仅控制 plist 解析，不是整份备份或 TAR 导出的大小限制；调高上限可能增加解析内存占用。
- 待确认历史点可保留和导出，但不能作为正常增量基线。检查通过及导出哈希一致不代表已验证整机恢复成功。

### English

**v1.5.6 — Fix large-metadata history imports and CSV list completeness**

- Fix a v1.5.5 failure where metadata larger than 8 MiB could leave an existing-backup import pending and prevent subsequent backups from starting. Copying, history preservation, and TAR export now stream SHA-256 independently of plist parsing limits.
- Add `IOSBK_PLIST_MAX_MIB`, defaulting to `64` and accepting integers from `1–256`. It limits each plist input and converted output during checks and browsing. An over-budget backup can still be preserved and exported as a pending history point, without becoming an incremental baseline.
- Show the failing plist and effective parsing limit. Identify the existing-backup import stage and distinguish permission or read-only storage failures from insufficient space. Preserve pending policy settings so storage preparation can be retried.
- Fix long records being omitted from online CSV file lists. Support individual records up to 1 MiB and report oversized records explicitly. Abort an already-started download so clients can detect an incomplete transfer.
- Update English and Simplified Chinese messages and configuration, history, and browsing documentation. Existing configuration and backup formats remain compatible.

### Upgrade notes

- If storage preparation failed on v1.5.5, upgrade to v1.5.6, then select **Device settings → Backup retention → Retry storage preparation**. Keep the original backup and saved policy settings.
- No new setting is normally required. To parse plists larger than 64 MiB, set `IOSBK_PLIST_MAX_MIB: "128"` under your Compose service's `environment` and recreate the container. This setting limits plist parsing, not the total backup or TAR export size. A higher limit can increase parsing memory use.
- Pending points remain available for preservation and export but cannot serve as normal incremental baselines. Passed checks and matching export hashes do not establish successful whole-device recovery.

### Validation / 验证

- ARM64 test-machine Go suite: 971 top-level cases passed, with conditional skips; all 173 frontend tests passed. The complete race suite passed on macOS; the test machine's 39-bit VMA kernel cannot start ThreadSanitizer.
- Verified 9 MiB and 65 MiB plist imports, the 128 MiB setting, recovery of a persisted v1.5.5 failure, repeated retry without duplicate history points, and preservation across restart.
- A real encrypted history-point export matched all 10,004 source-file SHA-256 hashes. A real unencrypted CSV contained 145,905 data records, matching an independent SQLite count. These checks did not perform a new device backup or whole-device restore.

**Related / 关联**: [#29](https://github.com/razeencheng/iosbackup/issues/29), [#30](https://github.com/razeencheng/iosbackup/pull/30).

**Full changelog / 完整变更**: https://github.com/razeencheng/iosbackup/compare/v1.5.5...v1.5.6

## v1.5.5

### 中文

**v1.5.5 — 历史备份、离线浏览与更完善的备份管理**

本次更新新增历史备份保留、完整导出和离线检查，让备份更容易查看、管理和排查问题，同时完善中英文支持、通知和自动备份控制。

- **保留多个历史备份**：支持按设备开启历史保留，保存最近 N 份正常备份，并固定重要版本。开启后，每次备份使用独立工作副本，失败或停止不会改写已保存的历史备份；空间不足时优先保护已有历史。
- **完整导出备份**：可将选中的历史备份下载为完整 `.tar` 归档，保留原始目录结构和加密状态。补充通过 Finder、Apple Devices 恢复备份的中英文指南。
- **手机离线也能查看备份**：直接读取服务器或 NAS 上的备份概况及未加密文件清单，支持搜索、分页和完整 CSV 清单下载，已保存的索引可跨重启保留。
- **停止备份与暂缓自动计划**：运行中的备份可直接停止，并暂缓该设备的自动备份，避免立即重新启动。暂缓状态跨重启保留，可主动恢复，或在下一次手动备份通过启动检查后解除。
- **备份检查与任务记录**：备份执行成功后自动进行轻量状态检查；未加密备份支持离线文件存在性检查。任务记录支持筛选、分页、详情和日志导出，分别展示执行、检查及历史保存结果。
- **更清晰的控制台**：设备卡片统一为“备份列表 / 任务记录 / 设备设置”，记住展开状态和页签。优化备份进度、等待提示与停止确认，并提供单次强制全量备份入口。
- **完善中英文体验**：统一登录、控制台、引导页及通知设置的翻译。切换语言时保留未保存输入和进行中的操作；通知语言可独立设置，保留已有自定义模板。
- **减少通知干扰**：短暂离线和偶发配对检查失败不再反复推送告警，持续影响自动备份的问题才触发提醒。最终备份通知合并执行、检查和历史保存结果，避免重复通知。
- **支持部署时区**：自动备份窗口、页面时间、通知和应用日志统一使用 `TZ`，默认仍为 `Asia/Shanghai`。界面语言不影响调度。
- **文档与基础维护**：新增双语官网和恢复指南，更新 Go 工具链及构建依赖，完善多架构构建和回归检查。

### 升级须知

- 历史备份默认关闭，开启时默认预填保留 **2 份**，可设置 **1–365 份**。固定备份、旧加密版本等受保护数据另计，实际数量可能超过设置值；独立副本也需要额外存储空间。已有旧加密代不会因升级被自动合并或删除。
- 未开启历史时仍会原地更新当前备份，停止任务可能使当前备份集不完整。
- 修改 `TZ` 后，原有备份窗口的钟点保持不变，按新时区解释，请核对计划。
- 旧通知配置继续默认使用中文。需要英文通知时，请单独设置通知语言；已有自定义模板不会自动翻译。清空相应模板并保存后，可使用所选语言的默认正文。
- 新增的检查和历史异常通知规则，仅在缺失时继承原备份失败规则的渠道；明确关闭的规则保持关闭。
- 加密备份暂不支持离线解密文件清单或逐项文件检查。检查通过不代表已验证整机恢复成功，Finder / Apple Devices 的识别、解密和实际恢复仍需单独验证。

### English

**v1.5.5 — Backup history, offline browsing, and improved backup management**

This release adds backup history, complete exports, and offline checks to make backups easier to inspect, manage, and troubleshoot. It also improves English and Chinese support, notifications, and automatic backup controls.

- **Keep multiple historical backups**: Enable history per device, retain the latest N normal backups, and pin important versions. Each history-enabled backup uses an independent working copy, so a failed or stopped attempt does not overwrite saved history. When space is insufficient, existing protected backups are preserved.
- **Export complete backups**: Download a selected historical backup as a complete `.tar` archive, preserving its original directory structure and encryption state. New English and Chinese guides explain the recovery workflow with Finder and Apple Devices.
- **Inspect backups while the phone is offline**: Read backup summaries and unencrypted file lists directly from your server or NAS. Search and paginate the preview, download the complete saved file list as CSV, and retain saved indexes across restarts.
- **Stop backups and hold automatic scheduling**: Stop an active backup and place automatic backups for that device on hold to prevent an immediate restart. The hold survives application restarts and can be cleared explicitly or when the next manual backup passes its startup checks.
- **Backup checks and task records**: Run a lightweight status check after successful backup execution, with offline file-existence checks available for unencrypted backups. Filter and paginate task records, inspect details, and export logs. Execution, check, and history-saving results are shown separately.
- **A clearer console**: Device cards now organize controls into Backup list, Task records, and Device settings, remembering expanded cards and selected tabs. Progress, waiting states, and stop confirmations are clearer, with an advanced option to request a full backup for a single run.
- **More complete English and Chinese support**: Consistent translations across login, the console, onboarding, and notification settings. Language changes preserve unsaved inputs and ongoing operations. Notification language is configured independently, and existing custom templates are retained.
- **Less notification noise**: Transient disconnections and occasional pairing-check failures no longer produce repeated alerts. Persistent pairing problems that block automatic backups trigger actionable warnings. Final backup notifications combine execution, check, and history-saving results to avoid duplicate messages.
- **Deployment timezone support**: Backup windows, displayed times, notifications, and application logs use the deployment's `TZ`, defaulting to `Asia/Shanghai`. Interface language does not affect scheduling.
- **Documentation and maintenance**: Added a bilingual website and recovery guides, updated the Go toolchain and build dependencies, and expanded multi-architecture build and regression checks.

### Upgrade notes

- Backup history is **off by default**. The retention field starts at **2 backups** and accepts **1–365**. Pinned backups, older encryption generations, and other protected data are retained separately, so the total may exceed the configured count. Independent copies also require additional storage. Existing older encryption generations are not automatically merged or deleted on upgrade.
- With history disabled, backups continue to update the current set in place. Stopping a backup can leave that set incomplete.
- Changing `TZ` preserves the configured clock times and interprets them in the new timezone. Review your backup window after changing it.
- Existing notification configurations continue to default to Chinese. Select the notification language separately to receive English notifications. Existing custom templates are not translated automatically; clear and save the relevant template to use the default text in the selected language.
- New check-attention and history-attention notification rules inherit the existing backup-failure channels only when those rules are missing. Explicitly disabled rules stay disabled.
- Offline decryption of encrypted file lists and per-file checks of encrypted backups are not supported. A passed check does not establish successful whole-device recovery. Finder / Apple Devices recognition, decryption, and actual recovery still require separate validation.

**Full changelog / 完整变更**: https://github.com/razeencheng/iosbackup/compare/v1.5.4...v1.5.5

## v1.5.4

### Fixed

- Accept Cosign's verified SBOM attestation object stream when assembling release evidence, while preserving signature, identity, issuer, and nonempty-output checks. This fixes the validation error that stopped the v1.5.3 workflow before creating its GitHub Release and promoting `latest`.

### Added

- Push the same Linux amd64/arm64 image build to GHCR and Docker Hub, compare the index digests, and sign and verify each registry's index and platform SBOMs.
- Check Docker Hub credentials and public repository visibility before building; promote the newest stable version to `latest` in both registries and verify the digests before marking the GitHub Release as Latest.

### Compatibility

- No application behavior, configuration, pairing records, or backup-format changes; no data migration is required. Wi-Fi backups and encryption remain Core; restore, unpacking, and deletion remain Experimental and disabled by default.
- Keep the existing v1.5.3 source tag unchanged. Cross-registry publication is not atomic; a failed workflow may leave partial results and must be checked before retrying.

中文：修复 Cosign 已验证 SBOM 证明的输出格式误判；同一次双架构构建同步发布到 GHCR 与 Docker Hub，两处均校验摘要、签名及 SBOM，并同步最新正式版的 `latest`。本版本不改变应用功能、配置或备份格式，无需数据迁移；保留原 v1.5.3 源码标签。

## v1.5.3

### Added

- Generate and securely persist instance credentials on first startup, with a simpler Compose configuration and complete English and Chinese user manuals.
- Publish the official multi-architecture GHCR image and GitHub Release with provenance, per-platform SBOMs, and verified keyless signatures; promote the newest stable image to `latest`.

### Fixed

- Detect unhealthy device connection services and recover them with bounded retries while coordinating active device operations.
- Report connection-service health and recovery state consistently in device discovery, the first-use guide, and the main console.

### Compatibility

- Preserve existing instance credentials, pairing records, backup settings, and backup data. Wi-Fi backups and encryption are documented as Core; restore, unpacking, and deletion remain Experimental and disabled by default.

中文：简化首次部署并安全生成实例凭据；完善中英文使用手册；修复设备连接服务异常后的恢复和状态显示。本版本通过 GitHub Release 与 GHCR 发布双架构镜像、来源证明、SBOM 和签名，并将最新正式版同步到 `latest`。已有配置、配对及备份数据保留。

## v1.5.2

Released 2026-09-12.

### Fixed

- Stop manual and automatic backup sessions that exceed phase-specific inactivity limits, even when the device remains visible. Gracefully terminate the process group, release the job, and report `backup_stalled` without changing the last successful backup time.
- Use payload-free mobilebackup2 activity records from the bundled tool; repeated progress output, device heartbeats, and power assertions do not keep a dead session alive. Active transfers have no total-duration deadline.
- Show authorization, sending, receiving, and device-processing phases with the last activity time. Require a success acknowledgement from the current command before recording success.
- Add configurable authorization, transfer inactivity, and preparation limits (defaults: 5, 10, and 30 minutes). Existing configs and backup sets need no migration. This bounds indefinite waits; it does not establish why an individual iOS service stopped responding.

中文：修复手机仍在线时备份无限等待的问题；增加分阶段无活动超时、准确阶段和最后活动时间，正常慢速传输不会仅因总体百分比不变而中止。失败会释放任务并保留最后成功记录，配置与备份数据无需迁移。

## v1.5.1

### Fixed

- Recover previously registered Wi-Fi devices after heartbeat disconnects with a background worker independent of automatic backups and the browser UI.
- Reuse the last successfully registered network address for up to 24 hours in memory, including mDNS-discovered devices with no manual IP, with bounded retry backoff.
- Serialize network registration, use the correct netmuxd socket, and check the actual device registry before reporting IP-test success, including when the helper times out.
- Skip devices that are busy or being removed, and cancel recovery work when the application stops.

### Packaging

- Use the `v1.5.1` version without a prerelease suffix and update the release metadata and installation examples.
- Keep Wi-Fi and encryption classified as Preview. Recovery requires a reachable, paired device; it does not resume an interrupted backup or prevent device sleep and network interruptions.
- Keep the existing configuration and backup formats; no data migration is required.

## v1.5.0-beta.1

### Packaging

- Reorganized the Go source into a balanced `cmd/iosbackup` and `internal/*` package layout without changing the public HTTP/configuration contract.
- Prepared the official `ghcr.io/razeencheng/iosbackup:v1.5.0-beta.1` image definition for Linux `amd64` and `arm64`.
- Added strict release-manifest parsing, a reproducible public-file allowlist, sensitive-content gates, and offline license verification.

### Security and safety

- Kept single-administrator authentication enabled in the Compose example and documented the privileged-container/network boundary.
- Kept restore disabled per device by default and added a strict, default-off global gate for local unpack and backup deletion.
- Preserved CSRF checks, destructive-action confirmations, bounded command output, per-device concurrency guards, and encrypted secret persistence.

### Documentation

- Replaced the legacy monolithic README with paired English/Chinese quick-start, operations, and feature-status guides.
- Classified USB/scheduled/read-only workflows as Core; Wi-Fi and encryption/change-password as Preview; restore/unpack/delete as Experimental.
- Documented health checks, Trust/pairing, persistent volumes, upgrade/rollback, troubleshooting, security reporting, support, privacy, licensing, and trademarks.

## v1.4.1 - 2026-08-10

### Security

- Restricted `test-ip` to safe IP literals and rejected loopback, link-local, multicast, broadcast, and unspecified addresses before dialing.
- Validated all enabled notification channels before persistence and stopped reporting false success for empty or failed test notifications.
- Required release builds to inject commit, build date, and public source URL.

### Fixed

- Cancelled the full command process group and released task locks after the Wi-Fi disconnect grace period.
- Replaced the unreliable device-side `unback` path with a local, atomic export for unencrypted `Manifest.db` backups.

## v1.4.0 - 2026-06-20

- Added Wi-Fi backup, bilingual UI, SSE status updates, backup inspection, and encryption Preview workflows.

## 中文摘要

`v1.5.1` 修复 Wi-Fi 心跳断连后设备长期离线的问题：后台按退避间隔重试注册，支持手动 IP 与已由 mDNS 成功注册的设备，测试 IP 以真实注册结果为准。地址仅在内存中保留最多 24 小时，应用重启后需要重新发现或使用手动 IP。此版本无需迁移配置或备份数据，Wi-Fi 仍属于预览功能。

`v1.5.0-beta.1` 将代码按职责拆分为多个包，增加公开文件、敏感内容和许可证检查，并补充双语文档；功能分级为 Core（USB、定时、只读查看）、Preview（Wi-Fi、加密/改密）、Experimental（恢复、解包、删除，默认关闭）。当前发布日期只由 `release/manifest.env` 冻结，避免多处记录的日期不一致。
