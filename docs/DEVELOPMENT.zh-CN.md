# 开发与贡献入门

[English](DEVELOPMENT.md) · [贡献规则](../CONTRIBUTING.md) · [用户操作手册](OPERATIONS.zh-CN.md)

本页帮助贡献者准备能运行离线测试的开发环境。用户安装与真机操作见操作手册；通过 Go 测试不代表已经完成设备备份或恢复验收。

## 准备环境

在可信开发机安装 Git 和 Go。当前 [go.mod](../go.mod) 的语言版本为 `go 1.24`，工具链指令为 `go1.27.3`；使用与项目/CI 一致的工具链。自动工具链选择可能需要下载，因此“默认测试离线”指测试本身不访问外部服务，不代表首次安装 Go 可以不联网。需要完全断网时，先准备所需工具链与缓存。

Go 模块保持标准库依赖，不添加第三方 Go 包。Docker 镜像内的 libimobiledevice、netmuxd 等系统组件是另一套依赖，版本与许可记录在 [third_party](../third_party/components.lock.json)。纯 Go 编译或默认单元测试不需要连接真实手机。

在**源码仓库根目录** 确认环境：

```bash
git status --short
go version
go list -m all
```

`go list -m all` 应只列出模块 `iosbackup`。保留已有工作区改动，使用单独分支处理自己的变更。

## 找到代码

| 位置 | 职责 |
|---|---|
| `cmd/iosbackup` | 可执行入口、进程信号及应用启动 |
| `internal/app` | 应用编排、HTTP/API、设备状态、备份、调度、模板与静态资源 |
| `internal/config` | 启动参数和路径解析、校验 |
| `internal/i18n` | 共用语言解析与 API 消息资源 |
| `internal/notification` | 通知管理器、消息类型与渠道适配器 |
| `internal/backupstore` | 独立历史对象、复制/克隆准备、目录记录、读取占用和恢复事务 |
| `internal/persistence` | 原子文件写入与秘密存储支持 |
| `internal/boundedio` | 有界输出、行读取等工具 |
| `internal/buildinfo` | 构建身份与版本信息 |
| `release/manifest.env` | 发布版本、日期和源码入口的集中元数据 |
| `scripts`、`.github/workflows` | 测试、公开目录与许可检查、构建发布流程 |

当前项目不再是根目录单一 `package main`。运行单个应用测试时用 `./internal/app`，运行可执行构建时用 `./cmd/iosbackup`。依赖方向由 `TestPackageDependencyDirection` 检查；查看某模块的现有测试再修改其行为。


历史备份的生命周期和存储约定见 [backupstore 模块说明](../internal/backupstore/README.md)。默认保留含当天的 3 个自然日；策略确认、迁移与当前来源选择位于 `internal/app/backup_history*.go`。历史 API、导出与设置界面分阶段接入。


## 完成一次本地修改

1. 阅读 [CONTRIBUTING](../CONTRIBUTING.md) 和要修改的模块。会明显改变使用方式的改动，先在 Issue 中讨论并确定目标；安全漏洞按 [SECURITY](../SECURITY.md)报告。
2. 对行为修复先写能复现问题的测试。使用 `t.TempDir()`、可注入的命令执行器和本地测试服务器，不读取运行实例的 `/configs`，不连接个人设备。
3. 保持既有中文消息与对应英文 UI 语义一致；修改 HTTP/JSON、环境变量或持久化格式前明确兼容性。遵守锁、取消和占用释放约定。
4. 只对改动的 Go 文件运行 `gofmt -w`，先执行相关测试，再执行下列提交前检查。
5. 在 PR 中记录改了什么、运行过哪些检查、仍未验证什么；同步相关中英文手册。不要将源码检查描述为真机验收。

所有命令在**源码仓库根目录** 执行：

```bash
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go build -trimpath -o iosbackup ./cmd/iosbackup
```

`-race` 需要受支持的开发平台和相应 C 工具链；不要全局设置 `CGO_ENABLED=0` 后运行 race 测试。最后一条命令只编译程序，不会启动服务、访问手机或发送消息。

## 选择更小的验证范围

```bash
# 通知模块及应用中的通知行为
go test -count=1 ./internal/notification
go test -count=1 -run 'TestNotification|TestSendBackup|TestDeviceStatusNotifications' ./internal/app

# 包依赖约束
go test -count=1 -run '^TestPackageDependencyDirection$' ./internal/app

# 启动配置与持久化
go test -count=1 ./internal/config ./internal/persistence
```

完整的默认测试、覆盖率、基准测试和真实通知测试的启用条件见[测试指南](TESTING_GUIDE.md)。真实通知测试需要 `integration` 标签、明确发送确认变量、独立配置路径，日常开发不要启用。

## 国际化

应用目前支持 `en`、`zh-CN`。界面语言属于浏览器偏好，由登录、控制台、首次设置和通知设置共享；手动选择优先于浏览器语言，不支持的值回退到可用的浏览器偏好或英文。旧 `zh` 与向导语言偏好会迁移，语言 cookie 使登录前的服务端页面和错误也能使用同一选择。初始 HTML 与客户端切语使用同一份词典，首屏无需等待 JavaScript 替换中文后备文案。

资源随二进制嵌入，不访问远程翻译服务：

- `internal/app/static/ui-messages.json`：Go 模板与浏览器共用的界面词典，包含标题、占位符、无障碍标签和图片替代文本。`/static/ui-messages.js` 将此词典作为注册脚本返回。
- `internal/app/static/i18n.js`：语言选择、命名参数、属性、日期和请求语言。
- `internal/i18n/messages.json`：Go 与浏览器共用的 API 消息，浏览器通过 `/static/api-messages.js` 加载。
- `internal/notification/messages.json`：系统通知标题、默认正文和渠道标签。

`internal/i18n/messages.json` 中的 API 消息键同时用于生成 `internal/i18n/codes.go`。Go 调用处引用 `i18n.EncryptionPasswordRequired` 等常量，不直接写 `"api.encryption_password_required"`。增删消息键后运行 `go generate ./internal/i18n`，将生成文件与词典修改一起提交。不要手改 `codes.go`，也不要在常量重构时改变既有对外消息键的值。CI 以只读方式检查生成结果是否同步；Go 源码检查会拒绝业务代码中的完整 API 消息键字面量。协议测试可保留字面量，以发现意外的兼容性变化。

```bash
go generate ./internal/i18n
go run ./internal/i18n/cmd/gencodes -dir internal/i18n -check
```

使用语义明确的 key 和完整句子，参数使用名称（UI/API 为 `{name}`，通知模板为 `${name}`），各语言的参数名须一致。不拼接句子片段、不从译文生成 key、不按展示标签分支。设备名、路径、自定义模板保持原值。已知失败返回稳定的 `message_code` 与 `params`；`code` 保留端点既有的机器分类，新端点默认与消息键相同。保留可读的兼容 `error`/`message` 字段；工具原始输出放在诊断 `detail` 中。界面优先翻译 `message_code`，兼容回退到 `code`，并支持操作完成后切换语言。SSE 广播机器状态和标准时间点，不保存某个浏览器的最终译文。

界面语言、通知语言和备份调度相互独立。通知 `language` 按部署实例持久化，缺省仍为 `zh-CN`，兼容旧配置。正文覆盖为空时，发送时按通知语言选择默认模板。已有非空模板视为用户内容，即使它恰好等于旧中文默认值，也要保留，直到管理员主动清空。切换界面语言不改变通知或备份时刻。调度、页面、通知和应用日志统一使用部署 `TZ`（默认 `Asia/Shanghai`），显式时区无效时阻止启动。更改 `TZ` 保留已填钟点并按新时区解释；备份间隔继续按实际经过的小时数计算，历史实际时刻不变。墙钟时间使用 `app.nowTime()` / `app.inDeploymentTime()`；标准字段 `last_backup_at` 携带偏移，旧 `last_backup` 仍为上海钟点以兼容旧客户端。二进制嵌入标准库时区数据，支持 scratch 镜像。

安装 Node.js 20 或更新版本运行离线 JavaScript 行为测试，无需 npm 依赖。CI 显式执行 Node 测试，避免因缺少 Node 而静默跳过国际化检查：

```bash
node internal/app/testdata/i18n.test.js
go test -count=1 ./internal/i18n ./internal/notification
go test -count=1 -run 'TestSharedI18n|TestI18n|TestDeviceConnectionsAreLanguageIndependent' ./internal/app
```

新增用户文案时更新双语资源，并验证对应成功和失败路径。新增语言时补齐资源、更新支持语言解析及选择入口、说明回退规则并审校术语。检查实时切换、未保存表单、存储失败、未知错误回退、窄屏和较长译文；资源完整性检查不能替代浏览器验收。

应用自身生成的运维诊断使用固定英文和 `event=backup.scheduled` 等稳定事件标记，范围包括启动失败及日志中嵌入的底层错误，不能只修改外层日志格式。不新增日志翻译词典或日志语言设置；界面或通知语言变化不影响日志。共用消息错误可以复用英文词条生成诊断文本。界面、API 用户提示和系统通知仍在展示边界进行本地化。外部工具与操作系统的原始诊断、设备名、路径及其他用户数据保留原文，允许包含中文。测试须覆盖完整错误链和原文保留，不能只检查事件标记。

## 需要镜像时

必须先有 Docker/Buildx，并能下载镜像与系统组件。在**源码仓库根目录** 本地构建：

```bash
make build IMAGE=iosbackup TAG=dev PLATFORM=linux/amd64
```

ARM64 主机按实际目标改为 `PLATFORM=linux/arm64`。当前 `make build` 只加载本地镜像，不推送；`make build-multi` 导出本地 OCI archive；发布使用单独显式流程。查看 [Makefile](../Makefile) 和 [release workflow](../.github/workflows/release.yml)，不要把开发验证与公开发布混在一起。

使用本地镜像验证代码改动。将 `compose.yaml` 保存到独立的新测试部署目录，把其中的 `services.iosbackup.image` 改为 `iosbackup:dev`，再在该目录执行。本地已有 `iosbackup:dev`，不要运行远程 pull：

```bash
docker compose up -d
docker compose logs iosbackup
```

测试镜像选择保存在 Compose 中，后续重建继续使用同一镜像。按[安装指南](manual/installation.zh-CN.md)核对生成文件和登录；确认没有生产实例占用同一设备、数据卷或 mux 服务。这会启动真实设备服务，应使用专门测试环境。单独的 Go 二进制没有替你安装设备工具链。

## 文档和公开仓库检查

文档修改按职责维护：`README` 负责概览，`QUICKSTART` 负责首次 USB 备份的简要步骤，`OPERATIONS` 负责导航，`docs/manual/` 负责完整任务步骤；功能分级以 `FEATURE_STATUS` 为准。通知字段与事件统一在通知手册，Webhook 协议在 `WEBHOOK_GUIDE`；测试范围和结果记录要求统一在 `TESTING_GUIDE`。旧通知指南和测试总结仅保留入口，不再复制正文。修改正文时同步中英文并更新入口链接。

公开检出的仓库根目录可运行：

```bash
./scripts/check_public_repo.sh --directory .
./scripts/verify_licensing.sh
```

第一项检查公开仓库中允许包含的文件及其内容，不应对包含私有开发资料的源仓库直接套用；维护者在实际公开导出树中执行。修改检查脚本时运行相应 `*_test.sh`。CI 的准确检查顺序以 [ci.yml](../.github/workflows/ci.yml)为准，漏洞扫描等额外步骤可能需要联网工具。

提交前检查 `git diff --check` 和 `git status --short`，确保没有密码、私人配置文件、配对记录、UDID、个人备份内容、覆盖率产物或临时构建文件进入提交。真实数据演练与默认离线测试分开记录。

维护者准备版本元数据、发行说明和签名产物时，使用[版本发布准备指南](UPDATE_VERSION.md)；用户容器升级仍按操作手册执行。
