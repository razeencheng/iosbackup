# Support

## Supported boundary

Support is best effort for the latest published Beta, with no response-time or resolution SLA.

- Official platform: privileged Docker on a trusted Linux host/NAS, `linux/amd64` or `linux/arm64`.
- Core: USB pairing, manual/scheduled backup, and read-only backup inspection.
- Preview: Wi-Fi backup and backup encryption/change-password workflows.
- Experimental and disabled by default: whole-device restore, local unpack, and local backup deletion.

Native device operation on Windows/macOS, direct public-Internet deployment, multi-user authorization, guaranteed disaster recovery, encrypted-backup unpack, and old Beta maintenance are outside the supported boundary. Optional notifications are best effort and depend on third-party services.

See [Feature status](docs/FEATURE_STATUS.md) and the [Operations manual](docs/OPERATIONS.md) before opening an Issue.

## Requesting help

Search existing Issues, reproduce on the latest Beta, and include:

- version and commit from `/api/version`;
- Linux distribution/NAS model and `amd64` or `arm64`;
- USB or Wi-Fi connection type;
- exact steps, expected result, and actual result;
- a minimal redacted log window around the failure.

Never upload backups, pairing records, passwords, `.env`, notification targets/tokens, `secrets.enc`, full device identifiers, or unredacted screenshots. Report security-sensitive behavior through [SECURITY.md](SECURITY.md), not a public support Issue.

## 中文摘要

项目只对最新发布的 Beta 提供 best effort 支持，没有响应或解决 SLA。官方平台是可信 Linux 主机/NAS 上的 `amd64`/`arm64` 特权容器；Core 包括 USB 配对、手动/定时备份和只读查看，Wi-Fi 与加密/改密属于 Preview，恢复、解包、删除属于默认关闭的 Experimental。

求助前请在最新 Beta 复现，并提供 `/api/version` 的版本/commit、主机与架构、连接类型、复现步骤和已脱敏日志。不要上传备份、配对记录、密码、`.env`、通知秘密、`secrets.enc`、完整设备标识或未脱敏截图。安全问题使用 [SECURITY.md](SECURITY.md) 的私密渠道。
