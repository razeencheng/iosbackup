# Third-party notices

iOS Backup 的 Go 应用本身仅使用 Go 标准库，源码采用 `AGPL-3.0-only`。Docker 镜像另外编译和分发下列独立组件；它们继续适用各自的许可证，并不会因为与本项目一起发布而改为 AGPL。

## Direct build dependencies

下表列出直接从源码编译的组件。版本通过完整 Git commit SHA 固定；下方仅为便于阅读的短 SHA，完整值、上游地址、许可证文件和补丁映射以 `third_party/components.lock.json` 为准。

| Component | Pinned commit | Upstream | License | Patches |
|-----------|--------------|----------|---------|---------|
| libplist | `32428aba...` | https://github.com/libimobiledevice/libplist | LGPL-2.1 | 无 |
| libimobiledevice-glue | `da770a76...` | https://github.com/libimobiledevice/libimobiledevice-glue | LGPL-2.1 | 无 |
| libusbmuxd | `93eb168b...` | https://github.com/libimobiledevice/libusbmuxd | LGPL-2.1 | 无 |
| libtatsu | `60a39f36...` | https://github.com/libimobiledevice/libtatsu | LGPL-2.1 | 无 |
| libimobiledevice | `fa0f7919...` | https://github.com/libimobiledevice/libimobiledevice | LGPL-2.1 | `afc-return-after-receive-error.patch` |
| libgeneral | `81e46367...` | https://github.com/tihmstar/libgeneral | LGPL-2.1 | 无 |
| usbmuxd2 | `744c46fc...` | https://github.com/tihmstar/usbmuxd2 | LGPL-3.0 | `client-disconnect-exception.patch` |
| netmuxd | `ac8da974...` | https://github.com/jkcoxson/netmuxd | LGPL-2.1-only | `observability.patch`, `heartbeat-reconnect-after-sleep.patch`, `restore-helper-binaries.patch` |

### Patches

本地补丁位于 `third_party/patches/`。每个补丁都记录在对应组件的 `patches` 数组中。补丁内容：

- **libimobiledevice/afc-return-after-receive-error.patch**：修复 AFC 接收错误后的返回处理
- **usbmuxd2/client-disconnect-exception.patch**：修复客户端断开时的异常处理
- **netmuxd/observability.patch**：增加可观察性日志
- **netmuxd/heartbeat-reconnect-after-sleep.patch**：修复睡眠后心跳重连
- **netmuxd/restore-helper-binaries.patch**：恢复辅助二进制文件

## System libraries (Debian bookworm)

以下组件来自 Debian bookworm 基础镜像（digest 已固定于 `third_party/components.lock.json`）：

| Component | Source | License | Notes |
|-----------|--------|---------|-------|
| OpenSSL 3 | https://www.openssl.org/source/ | Apache-2.0 | 需保留 Apache notice |
| libusb | https://libusb.info/ | LGPL-2.1-or-later | 动态链接 |
| glibc | https://www.gnu.org/software/libc/ | LGPL-2.1-or-later | 动态链接 |
| SQLite | https://sqlite.org/src/ | Public domain | Debian packaging notices 包含在镜像中 |
| libcurl | https://curl.se/ | curl license (MIT-like) | 动态链接 |
| zlib | https://zlib.net/ | zlib license | 动态链接 |
| libgcc | GCC runtime | GPL-3.0 with GCC Runtime Library Exception | 动态链接 |

完整的运行时闭包（`ldd` 输出）记录在：
- `third_party/runtime-closure-linux-amd64.txt` (linux/amd64)
- `third_party/runtime-closure-linux-arm64.txt` (linux/arm64)

## LGPL source and relinking materials

镜像中的 C/C++ 辅助程序动态链接 LGPL 库。用于重新构建或替换这些库的材料包括：

1. `third_party/components.lock.json` 中记录的上游仓库和完整 commit
2. `third_party/patches/` 中记录的本地修改
3. Dockerfile 中的构建与动态链接步骤
4. 镜像内 `/usr/share/licenses/iosbackup/` 保存的许可证文本

直接组件表中的 `LGPL-2.1`、`LGPL-3.0` 是当前锁文件记录的许可证系列。`-only` 或 `-or-later` 的精确范围仍须以每个锁定上游 commit 的许可证声明为准，不能由本表推定。

### netmuxd Cargo dependencies

`third_party/netmuxd-cargo-licenses.tsv` 由锁定的 netmuxd commit 和 `Cargo.lock` 通过 `cargo metadata --locked --offline --format-version 1` 生成。清单记录解析出的 245 个 package 的名称、版本、来源和包元数据声明的许可证表达式；生成时没有 `UNDECLARED` 项。

该 TSV 是依赖审计清单，不是最终 Linux 二进制的精简 SBOM。构建会先把实际 checkout 的 `Cargo.lock` SHA-256 与公开锁文件核对，再以 `--locked` 解析和编译；同一构建阶段运行 `scripts/collect_cargo_licenses.sh`，从每个解析 package 的实际分发源码收集 LICENSE、LICENCE、COPYING、NOTICE、COPYRIGHT 和 AUTHORS 材料。缺失、空文件、符号链接、路径逃逸或 package 数不匹配都会中止构建。

当前锁定图中，242 个 package 直接使用 crate/workspace 随附材料；`defmt-parser 1.0.0` 和 `idevice 0.1.65` 使用与各自 `.cargo_vcs_info.json` 固定 commit/path 逐字段、逐摘要核对的上游仓库根许可证材料（`pinned_upstream_material`）。唯一的 metadata-only 例外是 `plist-macro 0.1.6`：锁定的 crate 与对应上游 commit 都没有独立许可证文件，因此 bundle 会保留原始 `Cargo.toml`、`README.md`、`.cargo_vcs_info.json`、Cargo metadata 作者/仓库/许可证字段、规范 MIT 正文和一份明确的 provenance NOTICE；规范正文不冒充上游文件。公开的精确回退表在 `third_party/cargo-license-fallbacks.tsv`，未知或第二个无材料 package 仍会中止构建。

最终镜像同时包含标准 SPDX 正文映射和逐 crate 原始归属材料：前者位于 `/usr/share/licenses/iosbackup/third_party/`，后者位于 `/usr/share/licenses/iosbackup/third_party/netmuxd-cargo/`。标准模板不能替代每个 crate 的版权声明与 NOTICE；归属 bundle 也不能替代最终二进制 SBOM。

## Build toolchains

构建时使用的工具链（不包含在最终镜像中）：

- **Debian bookworm** build tools (apt packages): `build-essential`, `pkg-config`, `git`, `autoconf`, `automake`, `libtool-bin`, `cmake`, `patch`
- **Rust** toolchain: rust:bookworm 镜像；netmuxd 的 Cargo 依赖由锁定的上游 commit 决定，包级许可证表达式记录在 `third_party/netmuxd-cargo-licenses.tsv`
- **Go** toolchain: golang:1.26.6-alpine

## Verification and updates

完整的源码 URL、构建输入和许可证文件映射见 [`third_party/components.lock.json`](third_party/components.lock.json)。构建镜像会把本仓库的许可证集合复制到 `/usr/share/licenses/iosbackup/`。

> **发布阻断条件**：正式公开镜像前，仍必须从最终镜像重新生成 `ldd`/SBOM 结果并核对运行库版本与 Debian package copyright 文件。netmuxd Cargo attribution bundle 已由实际锁定构建生成和打包，但它只覆盖 Cargo package 源码随附材料、两个固定上游根材料来源和一个透明的 metadata-only 例外，不声称覆盖 Debian 运行库或最终二进制依赖分析。
