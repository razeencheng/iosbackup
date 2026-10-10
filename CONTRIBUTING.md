# Contributing

Thank you for contributing. Before a large change, discuss in an Issue which user-visible behavior you want to change. Report vulnerabilities through [SECURITY.md](SECURITY.md), not a public Issue.

## Development

Start with the [development guide](docs/DEVELOPMENT.md) ([中文](docs/DEVELOPMENT.zh-CN.md)) for the source layout, safe local setup and testing paths.

Use Go 1.21 or newer. Automatic toolchain selection is pinned to Go 1.27.2 as the security baseline for project builds. The Go module must remain standard-library-only; the container's device toolchain is tracked separately.

Run `gofmt -w` on the Go files you changed, then validate from the repository root:

```bash
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go build -trimpath ./cmd/iosbackup
```

Default tests must remain offline. The real-notification test requires all three deliberate inputs: the `integration` build tag, `IOSBK_RUN_REAL_NOTIFICATIONS=YES_I_KNOW_THIS_SENDS_MESSAGES`, and `IOSBK_INTEGRATION_NOTIFICATION_CONFIG` pointing to a separate local configuration. Do not add real credentials, device identifiers, pairing records, or backup data to code or fixtures.

## Pull requests

- Keep each pull request focused and explain compatibility/security risk and verification evidence.
- For behavior fixes, add a failing regression test before the implementation.
- Preserve Simplified Chinese in existing user-facing messages and keep paired English UI text semantically aligned.
- Keep HTTP paths/JSON, environment variables, persisted formats, locking, and cancellation behavior compatible unless the change explicitly proposes a migration.
- Release changes must keep `release/manifest.env`, `internal/buildinfo`, `CHANGELOG.md`, image metadata, and documentation consistent. Follow the [maintainer release preparation guide](docs/UPDATE_VERSION.en.md) ([中文](docs/UPDATE_VERSION.md)).
- New or changed image components require updates to `third_party/components.lock.json`, license materials, notices, and verification.

## Contribution license

By submitting a contribution, you confirm that you have the right to provide it and agree that it is licensed **inbound = outbound** under the project's `AGPL-3.0-only` license. The project requires no CLA, no DCO, and no `Signed-off-by` line.

## 中文摘要

较大改动请先在 Issue 中讨论，确认要改变哪些行为；安全问题按 [SECURITY.md](SECURITY.md) 私密报告。Go 模块继续只使用标准库，默认测试必须离线，代码/夹具不得包含真实凭据、设备标识、配对记录或备份数据。

提交贡献表示你有权提供这些内容，并同意按项目相同的 `AGPL-3.0-only` 许可，即 inbound = outbound。本项目不要求 CLA、DCO 或 `Signed-off-by`。
