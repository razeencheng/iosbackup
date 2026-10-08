# Support

## Supported boundary

The maintainer will try to help with problems in the latest published Beta, but does not commit to response or resolution times.

- Official platform: privileged Docker on a trusted Linux host/NAS, `linux/amd64` or `linux/arm64`.
- Core: USB pairing, manual/scheduled backup, Wi-Fi backup, backup encryption/change-password workflows, and read-only backup inspection.
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

Never upload backups, pairing records, passwords, private configuration files, notification targets/tokens, `secrets.enc`, full device identifiers, or unredacted screenshots. Report security-sensitive behavior through [SECURITY.md](SECURITY.md), not a public support Issue.

## 中文摘要

维护者会尽力处理最新发布的 Beta 的问题，但不承诺响应或解决时间。官方平台是可信 Linux 主机/NAS 上的 `amd64`/`arm64` 特权容器；Core 包括 USB 配对、手动/定时备份、Wi-Fi 备份、加密/改密和只读查看，恢复、解包、删除属于默认关闭的 Experimental。

求助前请在最新 Beta 复现，并提供 `/api/version` 的版本/commit、主机与架构、连接类型、复现步骤和已脱敏日志。不要上传备份、配对记录、密码、私人配置文件、通知秘密、`secrets.enc`、完整设备标识或未脱敏截图。安全问题使用 [SECURITY.md](SECURITY.md) 的私密渠道。
