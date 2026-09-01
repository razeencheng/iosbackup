---
status: superseded
superseded-by: 0007-use-a-balanced-package-layout-before-publication.md
---

# 在公开发布前重构为保持兼容的深模块

> 本决策已由 ADR-0007 取代。首次公开 Beta 不再重写设备、备份和 Web 的状态所有权；本文件描述的深模块接口保留为 Beta 后的演进方向。

iOS Backup 在首次 Docker Beta 前完成全面内部重构，但保持现有环境变量、HTTP 接口、配置格式、备份目录和用户数据兼容，也不在重构期间增加普通新功能。新的实现由设备连接、备份任务、持久化、通知和 Web 等深模块组成，`main` 只承担依赖组装和根生命周期；现有内嵌 HTML、CSS 和原生 JavaScript 拆分整理，但不引入独立前端框架或新的运行时依赖。重构按模块逐步替换，每一步保持可构建、可测试和可回退，全部门禁完成后才形成公开发布候选。

备份任务模块的外部接口固定为四类行为：`Start(context.Context, StartRequest) (Receipt, error)` 原子接纳手动或定时备份任务，`Observe(context.Context, Revision) (Snapshot, error)` 同时支持即时状态快照和等待后续变化，`Cancel(BackupJobID) error` 幂等请求取消单个任务，`Shutdown(context.Context) error` 停止准入并有界收口所有活动任务。HTTP 和调度器不能直接操作设备连接、共享状态 map、锁、命令、进程或备份路径；USB/Wi-Fi 选择、Wireless Sync assertion、互斥、容量、进度、取消、备份代和完整性验证都隐藏在模块接口之后。

恢复、删除、改密和其他破坏性操作不通过通用 `Intent` 塞进备份任务模块；它们以后形成各自的深模块，并共享具体的受管设备操作仲裁。行为、安全和回归测试继续保留；只有在新模块接口上建立等价测试后，才删除依赖旧内部实现的白盒测试。

## Consequences

- 单纯拆分大文件不算完成重构；共享状态所有权和调用接口必须真正迁入对应模块。
- 调用方与测试通过同一模块接口观察结果，不能继续依赖 `App` 的内部 map、锁或 `*Unsafe` 方法。
- 定时器保留为模块外的调用方，但定时资格、同设备互斥和全局容量仍由备份任务模块在 `Start` 中原子判定。
- 全面重构不采用一次性大提交；每个迁移步骤必须保持外部兼容并具备独立验证和回退点。
