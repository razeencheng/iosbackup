---
status: superseded
superseded-by: 0005-publish-clean-source-and-multiarch-image-as-one-beta.md
---

# 以干净公开历史分三阶段发布

现有私有仓库和完整开发历史继续保留；GitHub 的公开 `main` 从独立工作树中的清理后历史创建，不改写或强推私有分支。发布顺序固定为源码 Beta、官方多架构 Docker Beta、生产版，以便分别关闭源码公开、二进制合规和生产可靠性风险。

## Consequences

- 公开历史不继承私有设备信息、内部地址、个人邮箱和陈旧发布记录。
- 私有仓库继续作为开发来源，公开发布必须经过显式的同步、清理和审计步骤。
- 官方镜像不能早于第三方许可证、SBOM、多架构和供应链门禁完成。
