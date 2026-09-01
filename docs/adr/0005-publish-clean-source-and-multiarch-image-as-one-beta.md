---
status: accepted
---

# 以干净源码和双架构镜像进行单次 Beta 首发

现有私有仓库和完整开发历史继续保留；[`razeencheng/iosbackup`](https://github.com/razeencheng/iosbackup) 的 `main` 从私有候选提交按文件白名单导出，经过敏感扫描后形成独立干净历史中的公开发布提交，不改写或直接公开私有开发主线。私有候选提交与公开发布提交之间通过私有导出证据清单关联，清单记录来源 SHA、公开 tree hash、公开提交 SHA 和逐文件内容哈希映射；所有对外标签、镜像和 Release 只引用公开发布提交。

首次发布采用“私有准备、人工切换”的顺序：GitHub 仓库保持 private；首次 push 前 GHCR package 可以尚不存在，若已经存在则必须为 private。在公开发布提交上创建 `v*` 标签后，由 GitHub Actions 构建并首次 push `linux/amd64`、`linux/arm64` 镜像；流程必须立即确认新建或更新后的 package 仍为 private，才能继续生成 GitHub prerelease、SBOM、provenance、摘要和签名。门禁或产物验证失败时保持 private 且不宣布。全部成功后，在一次单独授权的人工 cutover 中先把 GHCR package 调整为 public 并从无登录环境确认可匿名拉取，再把仓库调整为 public 并确认 prerelease 可见，最后才宣布 Docker Beta。GitHub 仓库、GHCR package 和 Release 的可见性变更不是跨服务原子事务，检查清单必须记录顺序、暂停条件和可行的回退动作；生产版仍是后续独立阶段。

## Consequences

- 私有提交历史、内部引用、设备记录和个人信息不会因公开仓库而变成可达对象。
- 源码门禁或镜像构建失败时，仓库保持 private，package 保持不存在或 private，prerelease 保持私有可见，并且不宣布 Docker Beta。
- 首次 push 前不要求 GHCR package 预先存在，但若已存在且不是 private，发布门禁必须在 push 前失败；首次 push 后必须重新查询并确认 visibility。
- 首发镜像仓库以 GHCR 为准；Docker Hub 不属于首发门禁。
- 源码、文档、双架构镜像和供应链证明必须共同指向公开发布提交；私有候选提交仅作为可审计来源记录。
- 人工 cutover 可能出现短暂的可见性不一致，因此不承诺“无半完成状态”的原子发布；流程通过固定顺序、逐步验证、暂停和回退降低风险。
