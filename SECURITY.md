# Security policy

## Supported version

Security fixes are considered for the latest published Beta only. Older builds should be upgraded and reproduced on the latest Beta before a report is triaged. The maintainer will try to address reports but does not commit to response or fix times.

## Report a vulnerability privately

Use GitHub [Private Vulnerability Reporting](https://github.com/razeencheng/iosbackup/security/advisories/new). Include the affected version and commit, prerequisites, reproduction steps, impact, and any safe mitigation you have identified.

Do not open a public Issue with exploit details. Do not attach passwords, notification tokens/URLs, pairing records, device identifiers, backup contents, `secrets.enc`, `secret_key`, `admin_password`, private configuration files, or unredacted logs/screenshots. Newly generated administrator passwords appear in first-start logs; redact them before reporting. If Private Vulnerability Reporting is temporarily unavailable, open only a content-free public Issue asking the maintainer to restore the private channel.

Authentication bypass, arbitrary file access, command injection, SSRF, secret disclosure, backup corruption, and container escape are in scope. Direct public-Internet exposure contrary to the deployment guidance, a lost backup-encryption password, or damage caused by manually modifying backup files is not automatically a product vulnerability.

The maintainer will acknowledge and investigate reports on a best-effort basis, coordinate disclosure when practical, and will not promise a deadline before the impact and fix are understood.

## 中文摘要

安全修复只面向最新发布的 Beta。维护者会尽力处理，但不承诺响应或修复时间。请使用 GitHub [Private Vulnerability Reporting](https://github.com/razeencheng/iosbackup/security/advisories/new) 私密报告，并提供受影响版本/commit、前提、复现、影响和安全缓解建议。

不要在公开 Issue、日志或截图中披露密码、通知 token/URL、配对记录、设备标识、备份内容、`secrets.enc`、`secret_key`、`admin_password` 或私人配置文件。首次初始化日志包含新生成的管理员密码，报告前必须脱敏。认证绕过、任意文件访问、命令注入、SSRF、秘密泄露、备份破坏和容器逃逸属于安全范围；违反部署说明直接暴露公网、丢失备份加密密码或手工改坏备份，不会自动视为产品漏洞。
