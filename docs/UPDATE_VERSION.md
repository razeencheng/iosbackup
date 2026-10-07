# 维护者版本发布准备指南

[English](UPDATE_VERSION.en.md) · [开发指南](DEVELOPMENT.zh-CN.md)

本页供维护者准备**源码版本、构建元数据和发行产物** 。已经部署服务、希望更新容器的用户，请使用[实例升级与回滚](manual/upgrade.zh-CN.md)。文件名保留为 `UPDATE_VERSION.md`，以兼容既有链接。

## 发布流程与镜像标签

[release.yml](../.github/workflows/release.yml) 由推送 `v*` 标签触发，在指定的公开 GitHub 仓库 `razeencheng/iosbackup` 发布，检查 GHCR 包和 Docker Hub 仓库的公开可见性，并把同一镜像推送到 `ghcr.io/razeencheng/iosbackup` 和 `docker.io/razeencheng/iosbackup`。

| 版本 | GitHub Release | 镜像标签 |
|---|---|---|
| `vX.Y.Z` | 正式发行版 | 版本标签；当它是版本号最大的正式发行版时，同时更新 `latest` |
| `vX.Y.Z-beta.N` 等带后缀版本 | 预发布 | 仅版本标签，不改变 `latest` |

每个版本只构建一次 `linux/amd64`、`linux/arm64` 多架构镜像。镜像签名、供应链证据验证和 GitHub Release 创建成功后，发布步骤在两个镜像仓库把同一 index digest 标记为 `latest` 并分别核对摘要，没有第二次构建。发布任务串行更新标签，并按数字比较正式版本号，防止旧版本重跑或维护分支补丁把 `latest` 指回旧版本；GitHub 的 Latest 标记在镜像摘要核对后同步更新。失败重跑时保留原标签和候选提交，检查已生成的产物，先解决失败原因。若已打标签的工作流本身需要修改代码，应合并并验证修复后确定新版本，不能移动已发布标签或绕过校验。

普通 [CI](../.github/workflows/ci.yml) 和手动 [打包检查](../.github/workflows/docker-package-test.yml) 不推送镜像，也不更新 `latest`。用户仍需执行 `docker compose pull` 和 `docker compose up -d` 才会替换运行中的容器。

仓库和 GHCR 包的可见性由维护者单独管理，工作流不会自动修改。已有包必须为 public；首次包不存在时允许创建，但推送后必须通过 public 检查。GitHub 首次创建的个人包默认私有时，需要在包设置中改为 public 后重跑；未通过检查不会继续创建 Release 或更新 `latest`。参见 [GitHub 的容器仓库说明](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

## Docker Hub 配置

1. 将 [razeencheng/iosbackup](https://hub.docker.com/r/razeencheng/iosbackup) 保持为 Public；工作流会在长时间构建前检查可见性。
2. 在 Docker 账号的 Account settings → Personal access tokens 中创建 GitHub Actions 专用令牌，选择 **Read & Write** 权限和合适的有效期，不需要 Delete 权限。参见 [Docker 官方令牌说明](https://docs.docker.com/security/access-tokens/personal-access-tokens/)。
3. 在 GitHub 仓库 Settings → Secrets and variables → Actions 中，添加 Repository variable `DOCKERHUB_USERNAME`，值为 `razeencheng`；添加 Repository secret `DOCKERHUB_TOKEN`，值为访问令牌。令牌不要写入源码、构建参数、日志或聊天。
4. 发布时两个镜像仓库都必须登录成功。缺少配置、认证失败或 Docker Hub 仓库非公开/不存在时，流程在构建前停止。重试 Release 作业时也需保持令牌有效，该作业会重新登录以更新 `latest`。

一次构建推送两个版本标签，Docker Hub 的 index digest 必须与构建输出一致后才能继续签名。两处均保存 index 的 keyless 签名和各平台 SBOM 证明，并针对准确的标签工作流身份验证。GitHub Release 的九个附件保留 GHCR 规范引用；Docker Hub 验证结果另见 Actions 日志，两边镜像的 index digest 相同。

跨仓库写入无法保证原子性：推送或更新 `latest` 时，可能一边成功而另一边失败。工作流失败不代表完成双仓库发布；重试前核对两边版本和 `latest` 的摘要，只有两边最终摘要检查均通过才更新 GitHub Latest 标记。重跑构建可能产生新的候选镜像摘要，应保持标签源码提交不变，以成功运行的证据为准。

## 1. 同步版本和发布说明

按对外兼容性决定 major/minor/patch；内部重构本身不必然要求 major。版本格式使用清单解析器接受的形式，如 `vX.Y.Z` 或 `vX.Y.Z-beta.N`。

| 位置 | 需要维护的内容 |
|---|---|
| [release/manifest.env](../release/manifest.env) | 唯一的发布清单：`IOSBK_VERSION`、`IOSBK_BUILD_DATE`、`IOSBK_SOURCE_URL`，恰好三个键。冻结目标版本、构建日期及公开源码地址。 |
| [internal/buildinfo/buildinfo.go](../internal/buildinfo/buildinfo.go) | `Version` 与清单同步；`Description` 概括当前变化。`BuildDate`、`Commit` 的开发默认值继续为 `unknown`。 |
| [buildinfo_test.go](../internal/buildinfo/buildinfo_test.go) | 同步版本和描述的开发默认值断言。 |
| [CHANGELOG.md](../CHANGELOG.md) | 添加唯一的 `## <版本>` 标题；正文按 `### Fixed`、`### Changed` 等分组，说明行为、兼容性、迁移和支持边界。 |
| [compose.yaml](../compose.yaml)、相关安装手册 | 默认镜像保持 `latest`，仅部署参数或操作步骤变化时同步更新中英文；版本号变化不需要修改 Compose、README 或 `publicReleaseImage` 断言。历史记录和旧截图来源保留原版本。 |

发布说明以 CHANGELOG 为准。README 使用“最新版本”表述并链接到 Compose 和 CHANGELOG，不写具体项目版本号；仅版本号变化时无需修改 README。使用 `rg -n -F` 搜索旧版本的引用，逐项判断是否属于当前默认值；不要全仓替换历史版本。

清单必须通过 `scripts/read_release_manifest.sh` 读取，不要 `source` 或 `eval`。Docker 构建向 `iosbackup/internal/buildinfo` 注入 `Version`、`BuildDate`、`Commit`、`SourceURL`；commit 来自待发布源码的完整 SHA，`Description` 则来自源码。不要把构建日期复制回 Go 源码，也不要沿用已删除的根目录 `version.go`。

## 2. 在推标签前检查发布说明

在**待发布源码根目录** 执行。当前工作流要求版本标题唯一、后面存在下一条 `## ` 作为提取边界，且当前段包含至少一个 `### ` 小标题。下面的预检查与这些格式要求一致：

```bash
(
  set -eu
  iosbk_version=$(./scripts/read_release_manifest.sh release/manifest.env IOSBK_VERSION)
  test "$(grep -Fxc "## $iosbk_version" CHANGELOG.md)" = 1
  awk -v heading="## $iosbk_version" '
    $0 == heading { found=1; next }
    found && /^## / { boundary=1; exit }
    found && /^### / { subsection=1 }
    END { if (!boundary || !subsection) exit 1 }
  ' CHANGELOG.md
  printf 'Release notes format OK: %s\n' "$iosbk_version"
)
```

失败时先修复 CHANGELOG。现有工作流在镜像推送和签名后才提取发布说明，格式错误可能导致镜像已推送、Release 却未创建，因此不要把这项检查留到推标签之后。首次发行没有旧版本边界时，应先调整并验证提取逻辑，不要编造历史版本。

## 3. 本地检查与打包预演

仍在**待发布源码根目录** 执行：

```bash
./scripts/read_release_manifest.sh release/manifest.env
./scripts/read_release_manifest_test.sh
go test -count=1 ./internal/buildinfo
go test -count=1 -run 'TestReleaseWorkflow|TestReleaseCosign|TestReleaseDockerHub|TestReleaseLatestPromotion|TestReleasePublicVisibilityGates|TestReleaseManifest|TestDockerPackageTestWorkflow|TestDockerBuildContextIncludesBuildInfoPackage|TestPublicMetadataMatchesReleaseManifest|TestPublicComposeUsesOfficialReleaseImage|TestPublicReadmesDocumentReleaseDeployment' ./internal/app
git diff --check
```

这些命令检查发布元数据及工作流要求。提交候选还需通过 [CI](../.github/workflows/ci.yml) 的完整检查，包括 `go vet ./...`、默认测试、race 测试、静态构建和许可检查；具体环境与命令见[开发指南](DEVELOPMENT.zh-CN.md)及[测试指南](TESTING_GUIDE.md)。不要为发版启用真实通知测试。

在**实际公开候选源码树** 检查分发内容：

```bash
./scripts/check_public_repo.sh --directory .
./scripts/verify_licensing.sh
```

包含私有开发文档的工作树不是这项公开目录检查的对象。导出后应在候选树再次执行相关测试；发布 commit 必须对应实际分发的源码，不能用另一个工作树的 SHA 替代。

| 方式 | 证明什么、产出什么 |
|---|---|
| `make build` | 默认构建并加载本地 `linux/amd64` 镜像，不推送；目标平台可显式指定。 |
| `make build-multi` | 本地双架构 OCI archive，不推送。 |
| [docker-package-test.yml](../.github/workflows/docker-package-test.yml) | 手动触发的 amd64/arm64 OCI 打包检查；不推送、不上传 archive、不签名，也不启动容器。 |
| `make push` | 显式推送镜像；不包含官方工作流的签名、SBOM、可见性和 Release 检查，不能替代完整发行。 |

打包成功不证明镜像已发布、设备可用或恢复成功。需要联网下载组件时使用项目锁定的来源，遇到临时网络失败先排查网络。

## 4. 发布前复核与发行证据

只有在候选内容、CI 和目标版本已确认后，才安排推送发布标签；**推送 `v*` 标签会立即触发构建和发布工作流** 。逐项核对：

- 标签与 `IOSBK_VERSION` 完全一致，检出的 HEAD、标签目标与工作流事件 SHA 一致，工作区没有未纳入候选的改动。
- 目标仓库为工作流允许的 `razeencheng/iosbackup`；`IOSBK_SOURCE_URL` 与该源码入口一致。只纳入审核过的文件，不使用无差别的 `git add .` 代替审查。
- 仓库为 public，已有 GHCR 包为 public，推送后能够通过公开可见性检查。所需 Actions/OIDC、包写入和 Release 写入权限已经准备。
- 支持范围、迁移/回滚说明、许可证材料和测试证据与该 commit 一致。失败重试前检查是否已产生镜像、签名或 Release，不能把失败状态等同于“什么也没发布”。

成功流程构建 `linux/amd64` 与 `linux/arm64` 镜像，记录多架构 index 和各平台 digest，生成每平台 SPDX SBOM 与 BuildKit SLSA provenance，在两个镜像仓库使用 Cosign 签名 index、证明各平台 SBOM，并验证结果。

GitHub Release 正文来自 CHANGELOG；附件包括 **9 个证据文件** ：两份 SBOM、两份 provenance、平台 manifest 列表、index digest、三份 Cosign 验证结果。中间 Actions artifact 另含发布说明，共 10 个文件，当前仅保留 1 天。没有独立 Go 可执行文件或完整镜像 tar 发行附件。

发布后按实际 digest 检查两架构镜像及构建身份，保存工作流运行和验证证据；参照用户[升级与回滚](manual/upgrade.zh-CN.md)进行受控验收。同时验证匿名源码访问和匿名镜像拉取；最高正式版本还需核对两个仓库的 `latest` 与版本标签是否均指向同一个多架构 index digest。USB、加密读取、恢复等能力的证据分别记录，未验证项继续按[功能状态](FEATURE_STATUS.zh-CN.md)标注，不能由打包或签名成功推断。
