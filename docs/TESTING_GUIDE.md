# 测试指南

[English](TESTING_GUIDE.en.md) · [开发入门](DEVELOPMENT.zh-CN.md)

本指南适用于当前 `cmd/`、`internal/` 包结构。所有命令从**源码仓库根目录** 执行。默认测试使用临时状态、替身命令和本地测试服务，不向真实通知渠道发消息，也不需要连接手机。这里的“离线”仍允许 `httptest` 等本机测试服务监听端口；工具链首次下载和 CI 漏洞扫描也需要单独准备。

## 1. 先检查环境

```bash
go version
go list -m all
git status --short
```

工具链以 [go.mod](../go.mod) 为准，目前指定 `go1.27.2`，模块应只列出 `iosbackup`。不要使用生产配置、挂载实际备份目录，或给默认测试设置真实通知凭据。

## 2. 运行默认测试

日常改动先运行相关包，再做一次全量测试：

```bash
go test -count=1 ./internal/notification
go test -count=1 ./internal/config ./internal/persistence
go test -count=1 ./...
```

需要静态和并发检查时：

```bash
go vet ./...
go test -race -count=1 ./...
```

race 测试需要支持的平台和 C 工具链；不要使用静态发布构建的 `CGO_ENABLED=0` 环境来运行它。测试退出非零时保留失败包、测试名和错误输出，先定位失败再决定是否重跑。

已有的 `./scripts/run_tests.sh` 会依次运行通知/配置筛选、全量测试、覆盖率、全部基准，并生成 `coverage.out` 与 `coverage.html`。它会重复执行部分测试，适合明确需要这组报告时使用；仅修改文档时不必每次运行。脚本还检查默认测试列表中不得出现 `TestRealNotice`。

## 3. 定位单个测试或模块

```bash
# 应用中的通知行为和事件消息
go test -v -count=1 -run 'TestNotification|TestSendBackup|TestDeviceStatusNotifications' ./internal/app

# 新通知模块的规则与失败继续发送行为
go test -v -count=1 -run '^TestManagerFiltersRulesAndContinuesAfterNotifierFailure$' ./internal/notification

# 包依赖方向
go test -v -count=1 -run '^TestPackageDependencyDirection$' ./internal/app

# 仅列出当前测试名，不运行测试函数
go test -list . ./internal/notification ./internal/app
```

`go test ... .` 只指向当前包，不再是应用测试的有效通用入口。应用测试用 `./internal/app`，通知模块测试用 `./internal/notification`，全项目用 `./...`。`-list` 仍会编译和初始化测试程序，不是纯文件搜索。

## 4. 覆盖率与基准

```bash
go test -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html

go test -run='^$' -bench='^BenchmarkNotificationSend$' -benchmem ./internal/app
```

覆盖率产物写在当前目录，属于本地生成文件。覆盖率百分比与单次基准结果都不能证明所有路径正确。比较基准应使用相同机器、工具链和参数，并记录变化范围；报告字段见文末。`-run='^$'` 避免在基准命令中再次运行普通测试。

## 5. 真实通知测试：默认关闭

`TestRealNotice` 位于 [`internal/app/notifiers_integration_test.go`](../internal/app/notifiers_integration_test.go)。它需要**双重显式启用** ：`integration` build tag 与发送确认变量；还必须给出独立配置文件路径。只有 build tag 时会跳过；确认变量已启用但缺少配置路径时会失败。测试不会自行推断生产 `/configs` 的位置。

只有明确要向自己控制的测试收件人发送消息时，才在隔离环境执行下面的命令。本次文档更新没有执行它：

```bash
IOSBK_RUN_REAL_NOTIFICATIONS=YES_I_KNOW_THIS_SENDS_MESSAGES \
IOSBK_INTEGRATION_NOTIFICATION_CONFIG=/absolute/path/to/test-notifications.json \
go test -tags=integration -run '^TestRealNotice$' -count=1 ./internal/app
```

配置必须启用总开关，含至少一个已启用且通过校验的测试渠道。当前辅助函数加载 Telegram、Email、企业微信、Webhook；**不包含 Bark** 。每个已启用渠道会直接发送 6 条测试消息（开始、成功、失败、上线、离线、系统错误），不是通过事件规则筛选后的单条测试。

不要复用生产配置。配置文件可能包含 token、邮箱密码和完整目标地址，应放在仓库外并限制权限；测试结束后清理测试收件箱和凭据。不要把该命令加入默认 CI，也不要把确认变量长期导出到 shell 配置。

## 6. 按职责选择或新增测试

以下索引合并测试位置与代表性职责，范围核对日期为 2026-10-06；有对应测试不代表所有设备、错误场景或第三方服务均已覆盖。

| 修改对象 | 测试入口 | 代表性检查 |
|---|---|---|
| 通知管理器 | [manager_test.go](../internal/notification/manager_test.go) | 事件规则、模板、有界队列、消息副本、取消、单渠道失败后继续处理 |
| 通知渠道适配器 | [adapters_test.go](../internal/notification/adapters_test.go) | Telegram/SMTP/企业微信/Bark/Webhook 的请求与错误、输入校验、出站地址限制、SMTP TLS 与超时 |
| 应用通知接入 | [notification_test.go](../internal/app/notification_test.go)、[notification_security_test.go](../internal/app/notification_security_test.go) | 备份/设备/系统事件消息、规则接入、通知配置保存、秘密格式与保护 |
| 启动配置与持久化 | [config](../internal/config)、[persistence](../internal/persistence) 中的 `*_test.go` | 参数校验、默认值、实验开关、原子写入与秘密存储 |
| 应用工作流 | [app](../internal/app) 中对应行为的 `*_test.go` | HTTP 认证/CSRF、设备移除、任务占用、备份进度与超时、实验功能门禁 |
| 包结构 | [architecture_test.go](../internal/app/architecture_test.go) | 入口与叶子模块依赖方向、应用导出边界 |
| 分发边界 | 公开检出中 [scripts](../scripts) 下对应的 `*_test.sh` | 公开文件/敏感内容、发布元数据、许可闭包及检查器回归 |

测试应检查实际行为：执行操作后，检查输出、保存的数据或对其他模块的调用。异步测试用有界 channel/context 等待确定事件，避免靠增大固定 sleep 通过。使用临时目录和固定假标识，保护共享状态并在结束时清理服务器/进程。

## 7. 常见失败

- **无法下载工具链** ：先准备匹配 Go 版本；这不等于测试需要真实通知网络。
- **本地端口监听被沙箱拒绝** ：确认测试环境允许本机测试服务器，不要改成真实远端服务绕过限制。
- **race 报告或超时** ：保存第一处失败栈和相关测试输出，检查状态所有权、锁与取消；不能把重跑偶然成功当成修复。
- **测试名不存在或没有测试运行** ：用 `-list` 核对名称和包路径，检查正则是否选中了目标。

准确 CI 顺序见 [ci.yml](../.github/workflows/ci.yml)。

## 8. 记录证据与验证边界

分别记录三类结果，不能互相替代：

1. **默认离线测试** ：验证所选代码路径与回归断言，不证明真实设备通信或外部服务投递成功。
2. **真实通知集成测试** ：证明本次适配器调用的发送结果；运行门禁和渠道范围见第 5 节，不代替 Web UI 配置保存、规则路由或真实备份事件验收。
3. **设备验收** ：需要设备、存储、连接和操作者。USB/Wi-Fi 备份、加密读取和真机恢复各自记录结果，不能用 Go 测试或截图中的在线状态代替。

既有 [Beta ARM64 教程记录](images/tutorial/README.md)包含一次 USB 成功与磁盘完成标记检查；未验证加密清单读取或整机恢复。群晖手册流程也未在此次文档更新中逐步真机复验。

在 PR、CI 或测试记录中保留以下字段：

| 字段 | 内容 |
|---|---|
| 代码身份 | 提交 SHA；有未提交变更时注明 |
| 环境 | Go 版本、操作系统/架构；真机测试另记镜像摘要、设备系统与连接方式 |
| 命令与操作 | 完整命令、标签、包路径或设备操作；秘密值用脱敏说明，不贴凭据 |
| 结果 | 退出码、失败测试、必要日志或任务最终结果；跳过和未运行单独列出 |
| 附件 | 覆盖率或基准报告的生成时间、对照条件；截图来源和隐私处理 |
| 限制 | 哪些场景未执行，哪些结论仍依赖外部服务或真机 |

本指南不声明当前工作区已通过所有检查。实际结果必须来自对应提交的命令输出或 CI；没有新证据时保持“未执行/待验证”，不要把旧记录中的“所有测试通过”当作当前结果。
