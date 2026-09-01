# =============================================================================
# 生产镜像: usbmuxd2 (USB) + netmuxd (Wi-Fi/跨子网) + libimobiledevice + Go app
#
# 多阶段:
#   builder         Debian C 栈 + usbmuxd2 (--without-wifi, 无 avahi)
#   netmuxd-builder rust:bookworm 源码编译 netmuxd (需 cmake)
#   go-builder      Go 静态二进制 (CGO_ENABLED=0)
#   deps-analyzer   ldd 收集所有二进制(含 netmuxd)的运行时 .so
#   final-prep      组装最小文件系统 + OpenSSL 兼容 cnf
#   scratch         最终运行时
#
# 版本固定: 各组件用 ARG *_REF 引用(默认主分支)。**首次在 NAS 构建成功后, 用构建日志里
#   的 commit SHA 回填下方 *_REF, 锁定可复现构建**(design §1.6)。libimobiledevice 系列需
#   够新以支持 iPadOS 26, 故默认 master, 锁 SHA 时取构建当时的 master HEAD。
# 构建网络: 默认直连(已实测 github/crates/debian 直连可达且快); 网络受限环境的可选镜像见
#   design §B, 不写入默认构建。
# 架构: 通过 buildx TARGETARCH 支持 linux/amd64 与 linux/arm64/v8。
# =============================================================================

# 组件引用: 已锁定为 2026-06-19 NAS 真机构建通过的各仓库 commit SHA(可复现构建, design §1.6)。
# 各 clone 后 `git checkout <SHA>`(为空则跳过, 用默认分支)。升级组件时改这里并重新真机验证。
ARG LIBPLIST_REF=32428abacb909988e8e960a8845a6430b17b6a60
ARG LIBGLUE_REF=da770a7687f35fbb981db4d7b47b1b032cd5c2c7
ARG LIBUSBMUXD_REF=93eb168bf6b07472d17781328c21df0c60300524
ARG LIBTATSU_REF=60a39f36d719344360ec2e87563ed43f61f0530f
ARG LIBIMD_REF=fa0f79190142bc309307967c058f89c1b36eb6b8
ARG LIBGENERAL_REF=81e4636784f1bc3fda6a90911fc15bf529975577
ARG USBMUXD2_REF=744c46fc7faf61ed87b38bc97b1ac793d50e163d
ARG NETMUXD_REF=ac8da97420c2ab1f05efcf62b3c7aa0f9c596336

# -----------------------------------------------------------------------------
# Stage 1: C 栈 + usbmuxd2(--without-wifi → 只管 USB, 不跑 mDNS, 无 avahi 依赖)
# -----------------------------------------------------------------------------
# 钉到 bookworm（不用 debian:stable）：① 必须与 netmuxd-builder 的 rust:bookworm 同代，
# 否则 netmuxd 的 glibc 与 scratch 运行时库不匹配；② 避免 stable 滚动到新版（trixie）破坏构建。
FROM debian:bookworm@sha256:813017f3d62be4b5891a7acca6a01bdcd4b8513daa81b1ab99d3a50385b26931 AS builder

# Install basic build tools first（software-properties-common 未使用，已移除）
RUN apt-get update && apt-get install -y \
    build-essential \
    pkg-config \
    git \
    autoconf \
    automake \
    libtool-bin \
    cmake \
    patch \
    && rm -rf /var/lib/apt/lists/*

# Install GCC/G++ with C++20 support
RUN apt-get update && apt-get install -y \
    gcc \
    g++ \
    && rm -rf /var/lib/apt/lists/*

# Install required libraries
# 注意: 不再安装 libavahi-client-dev —— usbmuxd2 用 --without-wifi 编译, 不链接 avahi;
#       netmuxd 用纯 Rust mdns-sd。全系统无 avahi。
RUN apt-get update && apt-get install -y \
    libssl-dev \
    libcurl4-openssl-dev \
    libusb-1.0-0-dev \
    doxygen \
    && rm -rf /var/lib/apt/lists/*

# Build libplist first
WORKDIR /build
COPY third_party/patches/usbmuxd2/client-disconnect-exception.patch /build/patches/usbmuxd2-client-disconnect-exception.patch
ARG LIBPLIST_REF
RUN git clone https://github.com/libimobiledevice/libplist.git && \
    cd libplist && { [ -z "$LIBPLIST_REF" ] || git checkout "$LIBPLIST_REF"; } && \
    ./autogen.sh --without-cython && \
    make && \
    make install

# Clone and build libimobiledevice-glue
ARG LIBGLUE_REF
RUN git clone https://github.com/libimobiledevice/libimobiledevice-glue.git && \
    cd libimobiledevice-glue && { [ -z "$LIBGLUE_REF" ] || git checkout "$LIBGLUE_REF"; } && \
    ./autogen.sh && \
    make && \
    make install

# Clone and build libusbmuxd
ARG LIBUSBMUXD_REF
RUN git clone https://github.com/libimobiledevice/libusbmuxd.git && \
    cd libusbmuxd && { [ -z "$LIBUSBMUXD_REF" ] || git checkout "$LIBUSBMUXD_REF"; } && \
    ./autogen.sh && \
    make && \
    make install

# Clone and build libtatsu
ARG LIBTATSU_REF
RUN git clone https://github.com/libimobiledevice/libtatsu.git && \
    cd libtatsu && { [ -z "$LIBTATSU_REF" ] || git checkout "$LIBTATSU_REF"; } && \
    ./autogen.sh && \
    make && \
    make install

# Clone and build libimobiledevice (master 以支持 iPadOS 26)
COPY third_party/patches/libimobiledevice/afc-return-after-receive-error.patch /build/patches/libimobiledevice-afc-return-after-receive-error.patch
ARG LIBIMD_REF
RUN git clone https://github.com/libimobiledevice/libimobiledevice.git && \
    cd libimobiledevice && { [ -z "$LIBIMD_REF" ] || git checkout "$LIBIMD_REF"; } && \
    patch -p1 < /build/patches/libimobiledevice-afc-return-after-receive-error.patch && \
    ./autogen.sh --without-cython && \
    make && \
    make install

# 独立 Wireless Sync Power Assertion 诊断工具。协议/TLS/长度帧复用锁定版
# libimobiledevice 的 public property_list_service API；先运行纯请求构造测试再安装工具。
COPY tools/ideviceassertion_protocol.h /build/iosbk-tools/ideviceassertion_protocol.h
COPY tools/ideviceassertion_protocol.c /build/iosbk-tools/ideviceassertion_protocol.c
COPY tools/ideviceassertion_protocol_test.c /build/iosbk-tools/ideviceassertion_protocol_test.c
COPY tools/ideviceassertion.c /build/iosbk-tools/ideviceassertion.c
RUN cd /build/iosbk-tools && \
    cc -std=c11 -Wall -Wextra -Werror \
        ideviceassertion_protocol.c ideviceassertion_protocol_test.c \
        -o ideviceassertion_protocol_test \
        $(pkg-config --cflags --libs libplist-2.0) -lm && \
    LD_LIBRARY_PATH=/usr/local/lib ./ideviceassertion_protocol_test && \
    cc -std=c11 -Wall -Wextra -Werror \
        ideviceassertion_protocol.c ideviceassertion.c \
        -o /usr/local/bin/ideviceassertion \
        $(pkg-config --cflags --libs libimobiledevice-1.0 libplist-2.0)

# libgeneral + usbmuxd2(USB 守护进程, --without-wifi)
# 版本化补丁: 规避继承可变参数构造函数(usbmuxd2 上游未修)。
ARG LIBGENERAL_REF
ARG USBMUXD2_REF
RUN git clone https://github.com/tihmstar/libgeneral.git && \
    cd libgeneral && { [ -z "$LIBGENERAL_REF" ] || git checkout "$LIBGENERAL_REF"; } && \
    ./autogen.sh && \
    make && \
    make install && \
    cd .. && \
    git clone https://github.com/tihmstar/usbmuxd2.git && \
    cd usbmuxd2 && { [ -z "$USBMUXD2_REF" ] || git checkout "$USBMUXD2_REF"; } && \
    patch -p1 < /build/patches/usbmuxd2-client-disconnect-exception.patch && \
    ./autogen.sh --without-wifi && \
    make && \
    make install

# -----------------------------------------------------------------------------
# Stage 2: netmuxd(Rust 源码编译, Wi-Fi/跨子网, 纯 Rust mdns-sd 无 avahi)
# -----------------------------------------------------------------------------
# rust:bookworm 与所有 debian:bookworm 阶段同代 → glibc 一致, netmuxd 二进制可混入同一
# scratch 运行时。三处 Debian 基础镜像与此 rust 镜像必须保持同一 Debian 代号。
FROM rust:bookworm@sha256:82150a52ec202c1b14d7817e14516c392bb7f5cfebd88f1ed531cb37ebd39922 AS netmuxd-builder

ENV CARGO_HTTP_TIMEOUT=600
ENV CARGO_HTTP_LOW_SPEED_LIMIT=1

# cmake 给 aws-lc-sys(idevice→rustls→aws-lc 链需要, rust 镜像默认无 cmake)。直连, 无镜像。
RUN apt-get update && apt-get install -y \
    pkg-config libssl-dev git cmake patch jq \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY third_party/patches/netmuxd/observability.patch /build/patches/netmuxd-observability.patch
COPY third_party/patches/netmuxd/heartbeat-reconnect-after-sleep.patch /build/patches/netmuxd-heartbeat-reconnect-after-sleep.patch
COPY third_party/patches/netmuxd/restore-helper-binaries.patch /build/patches/netmuxd-restore-helper-binaries.patch
COPY third_party/components.lock.json /build/iosbackup-lock/components.lock.json
COPY third_party/netmuxd-cargo-licenses.tsv /build/iosbackup-lock/netmuxd-cargo-licenses.tsv
COPY third_party/cargo-license-fallbacks.tsv /build/iosbackup-lock/cargo-license-fallbacks.tsv
COPY third_party/cargo-license-fallbacks/ /build/iosbackup-lock/repository/third_party/cargo-license-fallbacks/
COPY third_party/licenses/ /build/iosbackup-lock/repository/third_party/licenses/
ARG NETMUXD_REF
RUN set -eu; \
    expected_ref=$(jq -er '.components[] | select(.name == "netmuxd") | .ref | select(test("^[0-9a-f]{40}$"))' /build/iosbackup-lock/components.lock.json); \
    expected_lock_sha=$(jq -er '.components[] | select(.name == "netmuxd") | .cargo_lock_sha256 | select(test("^[0-9a-f]{64}$"))' /build/iosbackup-lock/components.lock.json); \
    expected_count=$(jq -er '.components[] | select(.name == "netmuxd") | .cargo_resolved_packages | select(type == "number" and . > 0 and floor == .)' /build/iosbackup-lock/components.lock.json); \
    test "$NETMUXD_REF" = "$expected_ref"; \
    git clone https://github.com/jkcoxson/netmuxd.git && \
    cd netmuxd && git checkout "$expected_ref" && \
    actual_lock_sha=$(sha256sum Cargo.lock | awk '{print $1}'); \
    test "$actual_lock_sha" = "$expected_lock_sha" && \
    patch -p1 < /build/patches/netmuxd-observability.patch && \
    patch -p1 < /build/patches/netmuxd-heartbeat-reconnect-after-sleep.patch && \
    patch -p1 < /build/patches/netmuxd-restore-helper-binaries.patch && \
    cargo fetch --locked && \
    cargo metadata --locked --format-version 1 > /build/netmuxd-metadata.json
COPY scripts/collect_cargo_licenses.sh /build/iosbackup-lock/collect_cargo_licenses.sh
RUN set -eu; \
    expected_count=$(jq -er '.components[] | select(.name == "netmuxd") | .cargo_resolved_packages | select(type == "number" and . > 0 and floor == .)' /build/iosbackup-lock/components.lock.json); \
    expected_pinned=$(jq -er '.components[] | select(.name == "netmuxd") | .cargo_pinned_upstream_materials | select(type == "number" and . >= 0 and floor == .)' /build/iosbackup-lock/components.lock.json); \
    expected_metadata_only=$(jq -er '.components[] | select(.name == "netmuxd") | .cargo_metadata_only_fallbacks | select(type == "number" and . >= 0 and floor == .)' /build/iosbackup-lock/components.lock.json); \
    /build/iosbackup-lock/collect_cargo_licenses.sh \
        --metadata /build/netmuxd-metadata.json \
        --lockfile /build/netmuxd/Cargo.lock \
        --inventory /build/iosbackup-lock/netmuxd-cargo-licenses.tsv \
        --fallback-registry /build/iosbackup-lock/cargo-license-fallbacks.tsv \
        --fallback-root /build/iosbackup-lock/repository \
        --canonical-license-map /build/iosbackup-lock/repository/third_party/licenses/spdx-license-map.tsv \
        --source-root /build/netmuxd \
        --cargo-root "${CARGO_HOME:-/usr/local/cargo}" \
        --expected-count "$expected_count" \
        --expected-metadata-only-fallback-count "$expected_metadata_only" \
        --output /build/netmuxd-cargo-attribution; \
    grep -Fxq "pinned_upstream_material_count=$expected_pinned" /build/netmuxd-cargo-attribution/EVIDENCE.env; \
    grep -Fxq "metadata_only_fallback_count=$expected_metadata_only" /build/netmuxd-cargo-attribution/EVIDENCE.env; \
    awk -F '\t' '$2 == "plist-macro" && $3 == "0.1.6" && $6 ~ /MIT[.]txt@canonical-spdx/ { found++ } END { exit found != 1 }' /build/netmuxd-cargo-attribution/packages.tsv; \
    cd /build/netmuxd-cargo-attribution; \
    sha256sum -c SHA256SUMS >/dev/null
RUN cd /build/netmuxd && cargo build --release --locked
# 产物: /build/netmuxd/target/release/{netmuxd, add_device, passthrough}

# -----------------------------------------------------------------------------
# Stage 3: Go builder
# -----------------------------------------------------------------------------
FROM golang:1.26.6-alpine@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS go-builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the command and internal packages, including embedded UI assets.
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build Go application with CGO disabled for cross-platform compatibility
ENV CGO_ENABLED=0
ARG TARGETOS
ARG TARGETARCH

ARG IOSBK_VERSION
ARG IOSBK_BUILD_DATE
ARG IOSBK_COMMIT
ARG IOSBK_SOURCE_URL
RUN test -n "${IOSBK_BUILD_DATE}" \
    && test -n "${IOSBK_COMMIT}" \
    && test "${IOSBK_COMMIT}" != "unknown" \
    && test -n "${IOSBK_SOURCE_URL}" \
    && case "${IOSBK_SOURCE_URL}" in *OWNER*|*REPO*) exit 1;; esac \
    && test -n "${IOSBK_VERSION}" \
    && GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -ldflags="-w -s -X iosbackup/internal/buildinfo.Version=${IOSBK_VERSION} -X iosbackup/internal/buildinfo.BuildDate=${IOSBK_BUILD_DATE} -X iosbackup/internal/buildinfo.Commit=${IOSBK_COMMIT} -X iosbackup/internal/buildinfo.SourceURL=${IOSBK_SOURCE_URL}" -o iosbackup ./cmd/iosbackup

# -----------------------------------------------------------------------------
# Stage 4: Dependency analyzer(ldd 收集所有二进制的运行时 .so)
# -----------------------------------------------------------------------------
# 同样钉 bookworm：收集的 .so 必须与 builder/netmuxd-builder 同代
FROM debian:bookworm@sha256:813017f3d62be4b5891a7acca6a01bdcd4b8513daa81b1ab99d3a50385b26931 AS deps-analyzer

ARG TARGETARCH

# Install ldd and other tools
RUN apt-get update && \
    apt-get install -y --fix-missing --no-install-recommends \
    file \
    binutils \
    sqlite3 \
    && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Copy built binaries and libraries from builder
COPY --from=builder /usr/local/bin /usr/local/bin
COPY --from=builder /usr/local/sbin /usr/local/sbin
COPY --from=builder /usr/local/lib /usr/local/lib
COPY --from=builder /lib /lib
COPY --from=builder /usr/lib /usr/lib

# netmuxd 三个二进制(同 bookworm glibc, 可与 C 栈共用一个 scratch)
COPY --from=netmuxd-builder /build/netmuxd/target/release/netmuxd     /usr/local/bin/netmuxd
COPY --from=netmuxd-builder /build/netmuxd/target/release/add_device  /usr/local/bin/add_device
COPY --from=netmuxd-builder /build/netmuxd/target/release/passthrough /usr/local/bin/passthrough

# Create a script to collect all dependencies
RUN mkdir -p /collected/bin /collected/lib /collected/usr/lib

# Collect all required binaries
RUN cp /usr/local/bin/idevice_id /collected/bin/ || true
RUN cp /usr/local/bin/ideviceinfo /collected/bin/ || true
RUN cp /usr/local/bin/idevicepair /collected/bin/ || true
RUN cp /usr/local/bin/idevicebackup2 /collected/bin/ || true
RUN cp /usr/local/bin/ideviceassertion /collected/bin/ || true
RUN cp /usr/local/bin/iproxy /collected/bin/ || true
RUN cp /usr/local/sbin/usbmuxd /collected/bin/ || true
RUN cp /usr/bin/sqlite3 /collected/bin/
# netmuxd(Wi-Fi)及其 helper
RUN cp /usr/local/bin/netmuxd /collected/bin/ || true
RUN cp /usr/local/bin/add_device /collected/bin/ || true
RUN cp /usr/local/bin/passthrough /collected/bin/ || true

# Create dependency collection script
RUN echo '#!/bin/bash\n\
set -e\n\
\n\
# Create lists to track processed files\n\
processed_libs=/tmp/processed_libs\n\
touch $processed_libs\n\
\n\
# Function to copy library and its dependencies recursively\n\
copy_lib_deps() {\n\
    local lib="$1"\n\
    local lib_basename=$(basename "$lib")\n\
    \n\
    # Skip if already processed\n\
    if grep -q "^$lib$" $processed_libs 2>/dev/null; then\n\
        return\n\
    fi\n\
    \n\
    if [ -f "$lib" ] && [ ! -f "/collected/lib/$lib_basename" ]; then\n\
        echo "Copying $lib"\n\
        cp "$lib" /collected/lib/\n\
        echo "$lib" >> $processed_libs\n\
        \n\
        # Get dependencies and process them\n\
        ldd "$lib" 2>/dev/null | grep "=>" | awk "{print \$3}" | while read dep; do\n\
            if [ -n "$dep" ] && [ -f "$dep" ]; then\n\
                copy_lib_deps "$dep"\n\
            fi\n\
        done\n\
    fi\n\
}\n\
\n\
# Function to copy binary and its dependencies\n\
copy_bin_deps() {\n\
    local bin="$1"\n\
    if [ -f "$bin" ]; then\n\
        echo "Analyzing $bin"\n\
        ldd "$bin" 2>/dev/null | grep "=>" | awk "{print \$3}" | while read dep; do\n\
            if [ -n "$dep" ] && [ -f "$dep" ]; then\n\
                copy_lib_deps "$dep"\n\
            fi\n\
        done\n\
    fi\n\
}\n\
\n\
# Copy dependencies for all binaries\n\
for bin in /collected/bin/*; do\n\
    if [ -f "$bin" ]; then\n\
        copy_bin_deps "$bin"\n\
    fi\n\
done\n\
\n\
# Copy essential libraries and their dependencies\n\
find /usr/local/lib -name "*.so*" | while read lib; do\n\
    copy_lib_deps "$lib"\n\
done\n\
\n\
# Copy system libraries(libgcc_s 为 Rust netmuxd 所需)\n\
for lib in libc.so.6 libm.so.6 libpthread.so.0 libdl.so.2 librt.so.1 libssl.so.3 libcrypto.so.3 libz.so.1 libgcc_s.so.1; do\n\
    find /lib /usr/lib -name "$lib" 2>/dev/null | head -1 | while read found_lib; do\n\
        if [ -n "$found_lib" ]; then\n\
            copy_lib_deps "$found_lib"\n\
        fi\n\
    done\n\
done\n\
\n\
# Copy dynamic linker\n\
cp /lib/x86_64-linux-gnu/ld-linux-x86-64.so.2 /collected/lib/ 2>/dev/null || \\\n\
cp /lib64/ld-linux-x86-64.so.2 /collected/lib/ 2>/dev/null || true\n\
\n\
# List collected files\n\
echo "Collected binaries:"\n\
ls -la /collected/bin/\n\
echo "Collected libraries:"\n\
ls -la /collected/lib/\n\
echo "Dependency collection complete"\n\
' > /collect_deps.sh && chmod +x /collect_deps.sh

# Run dependency collection
RUN /collect_deps.sh

# The collector historically handled x86_64 only. Require the target loader so
# an image cannot build successfully with unusable dynamically linked tools.
RUN case "${TARGETARCH}" in \
        amd64) loader=/lib64/ld-linux-x86-64.so.2 ;; \
        arm64) loader=/lib/ld-linux-aarch64.so.1 ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && test -f "${loader}" \
    && cp -L "${loader}" /collected/lib/

# -----------------------------------------------------------------------------
# Stage 5: Prepare final filesystem
# -----------------------------------------------------------------------------
FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241 AS final-prep

# Install minimal runtime dependencies
RUN apt-get update && \
    apt-get install -y --fix-missing --no-install-recommends \
    ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Create directory structure
# /var/lib/lockdown: 配对记录持久化卷; /var/run: usbmuxd2 unix socket
RUN mkdir -p /final/usr/local/bin \
    /final/usr/local/lib \
    /final/lib \
    /final/lib64 \
    /final/etc/ssl/certs \
    /final/usr/share/licenses/iosbackup/third_party \
    /final/etc \
    /final/app \
    /final/backup \
    /final/var/lib/lockdown \
    /final/var/run \
    /final/tmp

# Copy binaries and libraries from deps-analyzer
COPY --from=deps-analyzer /collected/bin/* /final/usr/local/bin/
COPY --from=deps-analyzer /collected/lib/* /final/usr/local/lib/

# Copy Go application (templates are embedded, no need to copy template files)
COPY --from=go-builder /app/iosbackup /final/app/iosbackup
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /final/usr/share/licenses/iosbackup/
COPY third_party/licenses/ /final/usr/share/licenses/iosbackup/third_party/
COPY third_party/netmuxd-cargo-licenses.tsv /final/usr/share/licenses/iosbackup/third_party/
COPY --from=netmuxd-builder /build/netmuxd-cargo-attribution/ /final/usr/share/licenses/iosbackup/third_party/netmuxd-cargo/
COPY --from=deps-analyzer /usr/share/doc/sqlite3/copyright /final/usr/share/licenses/iosbackup/third_party/sqlite3-debian-copyright

# Create essential system files
RUN echo "root:x:0:0:root:/:/bin/sh" > /final/etc/passwd && \
    echo "root:x:0:" > /final/etc/group

# Create nsswitch.conf
RUN echo "hosts: files dns" > /final/etc/nsswitch.conf

# Copy CA certificates
RUN cp /etc/ssl/certs/ca-certificates.crt /final/etc/ssl/certs/ca-certificates.crt

# OpenSSL3 兼容 cnf: 放宽设备 TLS 的弱签名/seclevel(网络 lockdown 握手在 OpenSSL3 上常因
# "ca md too weak"/seclevel 失败)。**不设全局 ENV OPENSSL_CONF** —— 仅由 Go 侧对 idevice*
# 设备命令注入 OPENSSL_CONF=/etc/ssl/iosbk-openssl.cnf, 不影响 netmuxd 等其它进程。
RUN printf '%s\n' \
    'openssl_conf = openssl_init' \
    '[openssl_init]' \
    'ssl_conf = ssl_sect' \
    '[ssl_sect]' \
    'system_default = sysdef' \
    '[sysdef]' \
    'Options = UnsafeLegacyRenegotiation' \
    'CipherString = DEFAULT@SECLEVEL=0' \
    > /final/etc/ssl/iosbk-openssl.cnf

# Copy the dynamic linker to the interpreter path encoded in target binaries.
ARG TARGETARCH
RUN case "${TARGETARCH}" in \
        amd64) source=/final/usr/local/lib/ld-linux-x86-64.so.2; target=/final/lib64/ld-linux-x86-64.so.2 ;; \
        arm64) source=/final/usr/local/lib/ld-linux-aarch64.so.1; target=/final/lib/ld-linux-aarch64.so.1 ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && test -f "${source}" \
    && cp "${source}" "${target}"

# -----------------------------------------------------------------------------
# Stage 6: Final runtime image
# -----------------------------------------------------------------------------
FROM scratch

ARG IOSBK_VERSION
ARG IOSBK_BUILD_DATE
ARG IOSBK_COMMIT
ARG IOSBK_SOURCE_URL

LABEL org.opencontainers.image.title="iOS Backup" \
    org.opencontainers.image.version="${IOSBK_VERSION}" \
    org.opencontainers.image.created="${IOSBK_BUILD_DATE}T00:00:00Z" \
    org.opencontainers.image.revision="${IOSBK_COMMIT}" \
    org.opencontainers.image.source="${IOSBK_SOURCE_URL}" \
    org.opencontainers.image.licenses="AGPL-3.0-only"

# Copy the complete filesystem from preparation stage
COPY --from=final-prep /final/ /

# Set environment variables
ENV PATH="/usr/local/bin:/usr/bin:/bin"
ENV LD_LIBRARY_PATH="/usr/local/lib:/usr/lib:/lib"
ENV SSL_CERT_FILE="/etc/ssl/certs/ca-certificates.crt"
ENV SSL_CERT_DIR="/etc/ssl/certs"
ENV LOG_LEVEL="WARN"
ENV PORT="8080"

# Working directory
WORKDIR /app

# Expose port
EXPOSE ${PORT}

# Set volumes
# /var/lib/lockdown: usbmuxd2 与 netmuxd 都从此读配对记录, 必须持久化(否则重建容器丢配对)
VOLUME ["/backups"]
VOLUME ["/configs"]
VOLUME ["/var/lib/lockdown"]

# Start command
ENTRYPOINT ["/app/iosbackup"]
