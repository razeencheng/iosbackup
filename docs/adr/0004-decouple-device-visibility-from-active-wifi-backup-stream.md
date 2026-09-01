---
status: accepted
---

# 将设备可见性与活动 Wi-Fi 备份流存活解耦

Wi-Fi 备份经 netmuxd 建立 mobilebackup2 数据流。项目当前锁定的 netmuxd 会在辅助 heartbeat 单次收发失败后删除设备，并主动关闭该设备的全部代理 socket；真机长备份由此出现 `Could not receive from mobilebackup2 (-4)`，而同一设备随后通过 USB 可以完成备份。设备是否仍被发现与已经建立的数据流是否仍能传输，是两个不同事实。

项目维护一个基于锁定 netmuxd commit 的小型下游补丁，并同步向上游提交：heartbeat 单次失败只把设备标记为疑似异常并触发重连，不关闭活动 socket；只有持续失败超过宽限且没有活动流时，才移除设备。应用侧不再仅因 `ListDevices` 暂时缺少设备而取消活动 Wi-Fi 备份，真实数据流错误、应用关闭或独立且有界的失败判定才可以结束任务。

`--disable-heartbeat` 仅用于 A/B 诊断，不作为生产配置。项目继续锁定明确的 netmuxd commit，不直接追随上游 HEAD。Wi-Fi 在两台设备各连续完成三次长备份并通过一次主动断网测试前，保持为预览能力。

## Consequences

- 辅助心跳抖动不再自动破坏仍健康的长备份流。
- 真实断网仍需有界退出，不能以“不误杀”为由让任务无限挂起。
- 项目需要维护、审计并上游化一个很小的 Rust 补丁，同时记录补丁基线和构建来源。
- 运行时必须能区分 heartbeat 失败、数据流失败、设备发现缺失、用户取消和应用关闭。
- 首页可以显示设备可见性变化，但不能把该显示状态直接当作活动任务的终止信号。

## Considered Options

- 直接升级 netmuxd HEAD：最新上游仍会在 `HeartbeatFailed` 后移除设备并关闭活动 socket，不能解决问题，且引入更大的 USB 架构变更面。
- 生产环境永久使用 `--disable-heartbeat`：可以验证根因，但会保留陈旧设备条目，不能独立承担在线状态管理。
- 只延长应用的离线宽限：netmuxd 会在应用宽限生效前主动关闭数据流，无法处理根因。
- 不修 Wi-Fi、只承诺 USB：实现成本最低，但不符合产品已承诺的家庭 Wi-Fi 自动备份方向。
