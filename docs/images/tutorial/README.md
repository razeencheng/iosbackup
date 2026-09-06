# Tutorial screenshots / 教程截图

These are real Web UI captures from the project's Raspberry Pi ARM64 Beta candidate test deployment. They are not generated mockups. JPEG captures were converted to PNG without changing the displayed content so the existing public-image validation and sensitive-content scanner can inspect them.

这些图片来自项目在 Raspberry Pi ARM64 上运行的 Beta 候选实例，均为真实 Web 界面截图。JPEG 截图仅转换为 PNG 格式，未修改界面内容，以适配现有公开图片校验和敏感内容扫描。

| File / 文件 | Observed state / 实际状态 |
|---|---|
| [01-login.png](01-login.png) | Login form with no password displayed / 登录表单，未显示密码 |
| [02-login-error.png](02-login-error.png) | Incorrect credentials rejected / 错误密码被拒绝 |
| [03-onboarding-wifi.png](03-onboarding-wifi.png) | Optional Wi-Fi setup instructions / 可选 Wi-Fi 准备步骤 |
| [04-connect-device.png](04-connect-device.png) | Waiting for a USB connection / 等待 USB 接入 |
| [05-ready-backup.png](05-ready-backup.png) | USB pairing complete; first backup not started / 已通过 USB 配对，首次备份尚未开始 |
| [06-console-wifi.png](06-console-wifi.png) | Real device online over Wi-Fi; no completed backup / 真机通过 Wi-Fi 在线，尚无已完成备份 |
| [07-console-usb.png](07-console-usb.png) | Real device connected over USB and charging; no completed backup / 真机通过 USB 连接并充电，尚无已完成备份 |
| [08-usb-backup-progress.png](08-usb-backup-progress.png) | First USB backup receiving data; overall and current-file progress shown / 首次 USB 备份正在接收数据，显示总体与当前文件进度 |
| [09-usb-backup-completed.png](09-usb-backup-completed.png) | USB backup returned success, no active task, completion timestamp displayed / USB 备份返回成功，活动任务已结束，显示完成时间 |

An online or paired device is not evidence of a successful backup or restore. Screenshots do not include browser chrome, host addresses, passwords, complete device identifiers, or backup contents.

设备在线或完成配对，不等于备份或恢复成功。截图未包含浏览器地址栏、主机地址、密码、完整设备标识或备份内容。

The successful USB run was also checked on disk: the required metadata files existed and `Status.plist` reported `SnapshotState=finished`. The device already had backup encryption enabled. This deployment had no saved backup password, so decrypted manifest inspection and device restore were not verified. A successful task screenshot is not evidence of those checks.

成功的 USB 备份同时经过磁盘检查：必要元数据文件存在，`Status.plist` 的 `SnapshotState=finished`。设备原先已启用备份加密，而当前实例未保存备份密码，因此尚未验证清单解密或整机恢复。任务成功截图不能代替这些检查。

Copyright (C) 2025-2026 Razeen Cheng. Project-owned screenshots are distributed under AGPL-3.0-only; project names and logos remain subject to [TRADEMARKS.md](../../../TRADEMARKS.md).
