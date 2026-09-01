---
status: accepted
supersedes: 0006-refactor-around-deep-modules-before-publication.md
---

# 公开发布前采用平衡分包并保持行为兼容

iOS Backup 在首次公开 Docker Beta 前完成目录和包级整理，但不重写现有业务状态模型。入口迁入 `cmd/iosbackup`；设备发现、备份、调度、HTTP、进程生命周期和内嵌 UI 继续由 `internal/app` 统一编排；已经具备稳定边界的构建信息、运行时配置、通用有界 I/O、持久化机制和通知能力分别迁入 `internal/buildinfo`、`internal/config`、`internal/boundedio`、`internal/persistence` 和 `internal/notification`。依赖只允许从 `cmd` 指向 `app`，再由 `app` 指向叶子包，叶子包之间不得形成循环依赖。

本次整理必须保持现有环境变量、HTTP 路径和 JSON、磁盘配置格式、备份目录、命令调用、锁和并发语义兼容。`internal/app` 内部可按职责拆分大文件，但不为了目录整齐而提前拆出 `device`、`backup`、`web` 或 `mobiledevice` 包；这些区域目前共享 `App` 的锁、状态和生命周期，强行拆分会把公开前整理扩大成行为重构。只有在后续建立清晰所有权和黑盒契约后，才按新的 ADR 继续拆分。

迁移采用逐包、测试先行的方式：先把现有行为测试复制或改写到目标包并确认失败，再迁移最小实现；每一步都必须保持仓库可构建、默认测试离线且可回退。内嵌前端继续使用原生 HTML、CSS 和 JavaScript，Go 模块继续保持标准库零第三方依赖。

## Consequences

- 公开源码具备常见的 `cmd`/`internal` 结构，同时避免在 Beta 前引入大规模接口重写。
- `App` 暂时仍是业务编排和共享状态边界；这是一项明确取舍，不代表所有根包代码都应永久集中。
- 通用机制可被独立测试和复用，业务兼容性测试则随实现迁入 `internal/app`，不因换目录而删除。
- Dockerfile、CI、测试脚本、许可证扫描、公开文件清单和文档必须同步认识新的目录结构。
- 依赖方向由解析 `go list -json` 的自动测试执行，并作为默认测试和 CI 的失败门禁；仅打印依赖列表不构成验证。
