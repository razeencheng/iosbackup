# Tutorial screenshots / 教程截图

These are real Web UI captures. Images 01–04 and 06–07 come from the project's Raspberry Pi ARM64 test deployment; their JPEG captures were converted to PNG without changing the displayed content. Images 05, 08, and 09 are replacements supplied by the maintainer. Image 05 shows the completed first-backup step, 08 shows a USB backup in progress, and 09 shows the maintainer's Synology NAS with Wi-Fi-connected devices and a completed backup. The current connection shown in image 09 does not identify the transport used by that completed job.

这些图片均为真实 Web 界面截图。01–04、06–07 来自 Raspberry Pi ARM64 测试实例，原 JPEG 仅转换为 PNG，未修改界面内容。05、08、09 由维护者替换：05 显示首次备份步骤已完成，08 显示 USB 备份进行中，09 来自维护者自用群晖 NAS，拍摄时设备通过 Wi-Fi 在线且页面显示一项备份已完成。09 中的当前连接方式不能说明该任务完成时使用的连接方式。

| File / 文件 | Observed state / 实际状态 |
|---|---|
| [01-login.png](01-login.png) | Login form with no password displayed / 登录表单，未显示密码 |
| [02-login-error.png](02-login-error.png) | Incorrect credentials rejected / 错误密码被拒绝 |
| [03-onboarding-wifi.png](03-onboarding-wifi.png) | Optional Wi-Fi setup instructions / 可选 Wi-Fi 准备步骤 |
| [04-connect-device.png](04-connect-device.png) | Waiting for a USB connection / 等待 USB 接入 |
| [05-ready-backup.png](05-ready-backup.png) | First-use wizard showing a completed first USB backup / 首次使用向导显示第一次 USB 备份已完成 |
| [06-console-wifi.png](06-console-wifi.png) | Real device online over Wi-Fi; no completed backup / 真机通过 Wi-Fi 在线，尚无已完成备份 |
| [07-console-usb.png](07-console-usb.png) | Real device connected over USB and charging; no completed backup / 真机通过 USB 连接并充电，尚无已完成备份 |
| [08-usb-backup-progress.png](08-usb-backup-progress.png) | USB backup receiving data; overall and current-file progress shown / USB 备份正在接收数据，显示总体与当前文件进度 |
| [09-usb-backup-completed.png](09-usb-backup-completed.png) | Synology NAS console with Wi-Fi-connected devices and a completed backup / 群晖 NAS 控制台，设备通过 Wi-Fi 在线并显示已完成的备份 |

An online or paired device is not evidence of a successful backup or restore. Screenshots do not include browser chrome, host addresses, passwords, complete device identifiers, or backup contents.

设备在线或完成配对，不等于备份或恢复成功。截图未包含浏览器地址栏、主机地址、密码、完整设备标识或备份内容。

The earlier Raspberry Pi USB run was also checked on disk: the required metadata files existed and `Status.plist` reported `SnapshotState=finished`. The device already had backup encryption enabled. This deployment had no saved backup password, so decrypted manifest inspection and device restore were not verified. A successful task screenshot is not evidence of those checks.

此前 Raspberry Pi 实例的成功 USB 备份同时经过磁盘检查：必要元数据文件存在，`Status.plist` 的 `SnapshotState=finished`。设备原先已启用备份加密，而当前实例未保存备份密码，因此尚未验证清单解密或整机恢复。任务成功截图不能代替这些检查。

Copyright (C) 2025-2026 Razeen Cheng. Project-owned screenshots are distributed under AGPL-3.0-only; project names and logos remain subject to [TRADEMARKS.md](../../../TRADEMARKS.md).
