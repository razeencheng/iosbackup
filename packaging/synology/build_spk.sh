#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)

MANIFEST_READER="${REPO_ROOT}/scripts/read_release_manifest.sh"
RELEASE_MANIFEST="${REPO_ROOT}/release/manifest.env"

SPK_ARCH="x86_64"
BUILD_NUM="0001"
OUTPUT_DIR="${REPO_ROOT}/dist"
OUTPUT_FILE=""
CONTAINER_IMAGE=""
VERSION_OVERRIDE=""
BUNDLE_MODE="offline"
LOCAL_IMAGE_TAR=""
EXPECTED_MANIFEST_DIGEST=""

usage() {
  cat >&2 <<EOF
Usage: $0 [options]

Options:
  --arch <x86_64|armv8>              Target Synology architecture (default: x86_64)
  --version <vX.Y.Z>                 Release version to package (default: from release/manifest.env)
  --offline                          Bundle offline Docker image into .spk (default)
  --online                           Build lightweight online-pull .spk (no bundled image)
  --image-tar <path>                 Use an existing local docker-save .tar/.tar.gz archive
  --expected-manifest-digest <sha>   Verify exact sha256:<hex> platform manifest digest when pulling
  --build-num <NNNN>                 4-digit SPK build number (default: 0001)
  --image <ref>                      Container image reference (default: ghcr.io/razeencheng/iosbackup:<version>)
  --output-dir <dir>                 Output directory for generated .spk (default: ./dist)
  --output-file <path>               Explicit output .spk file path (overrides default filename)
  -h, --help                         Show this help message
EOF
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --arch)
      [ "$#" -ge 2 ] || usage
      SPK_ARCH=$2
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || usage
      VERSION_OVERRIDE=$2
      shift 2
      ;;
    --offline)
      BUNDLE_MODE="offline"
      shift
      ;;
    --online)
      BUNDLE_MODE="online"
      shift
      ;;
    --image-tar)
      [ "$#" -ge 2 ] || usage
      LOCAL_IMAGE_TAR=$2
      BUNDLE_MODE="offline"
      shift 2
      ;;
    --expected-manifest-digest)
      [ "$#" -ge 2 ] || usage
      EXPECTED_MANIFEST_DIGEST=$2
      shift 2
      ;;
    --build-num)
      [ "$#" -ge 2 ] || usage
      BUILD_NUM=$2
      shift 2
      ;;
    --image)
      [ "$#" -ge 2 ] || usage
      CONTAINER_IMAGE=$2
      shift 2
      ;;
    --output-dir)
      [ "$#" -ge 2 ] || usage
      OUTPUT_DIR=$2
      shift 2
      ;;
    --output-file)
      [ "$#" -ge 2 ] || usage
      OUTPUT_FILE=$2
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      printf 'Unknown argument: %s\n' "$1" >&2
      usage
      ;;
  esac
done

case "$SPK_ARCH" in
  x86_64) OCI_ARCH="amd64" ;;
  armv8)  OCI_ARCH="arm64" ;;
  "x86_64 armv8")
    if [ "$BUNDLE_MODE" = "offline" ]; then
      printf 'Offline mode requires a single architecture (--arch x86_64 or --arch armv8)\n' >&2
      exit 1
    fi
    OCI_ARCH=""
    ;;
  *)
    printf 'Unsupported --arch value: %s (expected x86_64 or armv8)\n' "$SPK_ARCH" >&2
    exit 1
    ;;
esac

printf '%s' "$BUILD_NUM" | grep -Eq '^[0-9]{4}$' || {
  printf 'Invalid --build-num: %s (must be 4 digits, e.g. 0001)\n' "$BUILD_NUM" >&2
  exit 1
}

if [ -n "$VERSION_OVERRIDE" ]; then
  case "$VERSION_OVERRIDE" in
    v*) RAW_VERSION="$VERSION_OVERRIDE" ;;
    *)  RAW_VERSION="v${VERSION_OVERRIDE}" ;;
  esac
else
  RAW_VERSION=$("$MANIFEST_READER" "$RELEASE_MANIFEST" IOSBK_VERSION)
fi
SOURCE_URL=$("$MANIFEST_READER" "$RELEASE_MANIFEST" IOSBK_SOURCE_URL)
SEMVER="${RAW_VERSION#v}"
BASE_SEMVER="${SEMVER%%-*}"
SPK_VERSION="${BASE_SEMVER}-${BUILD_NUM}"

printf '%s' "$SPK_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+-[0-9]{4}$' || {
  printf 'Derived SPK version %s is invalid for Synology INFO\n' "$SPK_VERSION" >&2
  exit 1
}

if [ -z "$CONTAINER_IMAGE" ]; then
  CONTAINER_IMAGE="ghcr.io/razeencheng/iosbackup:${RAW_VERSION}"
fi

if [ "$BUNDLE_MODE" = "offline" ]; then
  PULL_POLICY="never"
else
  PULL_POLICY="missing"
fi

STAGE_TMP=$(mktemp -d "${TMPDIR:-/tmp}/iosbackup-spk-build.XXXXXX")
cleanup() {
  if [ -n "${STAGE_TMP:-}" ] && [ -d "$STAGE_TMP" ]; then
    rm -rf "$STAGE_TMP"
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

PKG_STAGE="${STAGE_TMP}/package"
SPK_STAGE="${STAGE_TMP}/spk"
mkdir -p \
  "${PKG_STAGE}/compose" \
  "${PKG_STAGE}/ui/images" \
  "${SPK_STAGE}/conf" \
  "${SPK_STAGE}/scripts" \
  "${SPK_STAGE}/WIZARD_UIFILES"

escape_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

ESC_SPK_VERSION=$(escape_sed_replacement "$SPK_VERSION")
ESC_SPK_ARCH=$(escape_sed_replacement "$SPK_ARCH")
ESC_SOURCE_URL=$(escape_sed_replacement "$SOURCE_URL")
ESC_IMAGE=$(escape_sed_replacement "$CONTAINER_IMAGE")
ESC_PULL_POLICY=$(escape_sed_replacement "$PULL_POLICY")

# 1. 组装内层 package.tgz 内容（保留 .in 模板供 postinst 按向导参数动态渲染，并预生成默认文件）
sed \
  -e "s/__CONTAINER_IMAGE__/${ESC_IMAGE}/g" \
  -e "s/__PULL_POLICY__/${ESC_PULL_POLICY}/g" \
  "${SCRIPT_DIR}/package/compose/docker-compose.yml.in" \
  >"${PKG_STAGE}/compose/docker-compose.yml.in"

sed \
  -e "s/@PORT@/9000/g" \
  -e "s/@TZ@/Asia\/Shanghai/g" \
  -e "s/@LOG_LEVEL@/INFO/g" \
  -e "s/@BACKUP_SUB@/backups/g" \
  -e "s/@CONFIG_SUB@/configs/g" \
  -e "s/@MIN_FREE_BYTES@/10737418240/g" \
  -e "s/@MAX_HEAVY_JOBS@/1/g" \
  -e "s/@PLIST_MAX_MIB@/64/g" \
  -e "s/@WEBHOOK_CIDRS@//g" \
  -e "s/@EXP_VAL@/false/g" \
  -e "s/@DATA_MOUNT@/\/var\/packages\/iosbackup\/var\/share_mount/g" \
  -e "s/@LOCKDOWN_MOUNT@/\/var\/packages\/iosbackup\/var\/lockdown/g" \
  -e "s/@SECRETS_MOUNT@/\/var\/packages\/iosbackup\/var\/secrets/g" \
  "${PKG_STAGE}/compose/docker-compose.yml.in" \
  >"${PKG_STAGE}/compose/docker-compose.yml"

cp "${SCRIPT_DIR}/package/ui/config.in" "${PKG_STAGE}/ui/config.in"
sed "s/@PORT@/9000/g" "${PKG_STAGE}/ui/config.in" >"${PKG_STAGE}/ui/config"

cp "${SCRIPT_DIR}/package/ui/index.cgi.in" "${PKG_STAGE}/ui/index.cgi.in"
sed -e "s/@PORT@/9000/g" -e "s/@EXTERNAL_URL@//g" "${PKG_STAGE}/ui/index.cgi.in" >"${PKG_STAGE}/ui/index.cgi"

cp "${SCRIPT_DIR}/assets/ui_icons/"*.png "${PKG_STAGE}/ui/images/"

PKG_ENTRIES="compose ui"
if [ "$BUNDLE_MODE" = "offline" ]; then
  mkdir -p "${PKG_STAGE}/images"
  TARGET_IMAGE_TAR="${PKG_STAGE}/images/iosbackup.tar.gz"
  if [ -n "$LOCAL_IMAGE_TAR" ]; then
    [ -f "$LOCAL_IMAGE_TAR" ] || {
      printf 'Local image tar does not exist: %s\n' "$LOCAL_IMAGE_TAR" >&2
      exit 1
    }
    # 支持传入 .tar 或 .tar.gz
    if head -c 2 "$LOCAL_IMAGE_TAR" | od -An -tx1 | tr -d ' \n' | grep -iq '^1f8b'; then
      cp "$LOCAL_IMAGE_TAR" "$TARGET_IMAGE_TAR"
    else
      gzip -nc "$LOCAL_IMAGE_TAR" >"$TARGET_IMAGE_TAR"
    fi
  else
    (
      cd "$REPO_ROOT"
      if [ -n "$EXPECTED_MANIFEST_DIGEST" ]; then
        go run ./packaging/synology/cmd/pull_image_tar \
          --image "$CONTAINER_IMAGE" \
          --arch "$OCI_ARCH" \
          --expected-manifest-digest "$EXPECTED_MANIFEST_DIGEST" \
          --output "$TARGET_IMAGE_TAR"
      else
        go run ./packaging/synology/cmd/pull_image_tar \
          --image "$CONTAINER_IMAGE" \
          --arch "$OCI_ARCH" \
          --output "$TARGET_IMAGE_TAR"
      fi
    )
  fi
  PKG_ENTRIES="compose images ui"
fi

find "$PKG_STAGE" -type d -exec chmod 755 {} +
find "$PKG_STAGE" -type f -exec chmod 644 {} +
chmod 755 "${PKG_STAGE}/ui/index.cgi" "${PKG_STAGE}/ui/index.cgi.in"

# 2. 组装外层 .spk 内容
sed \
  -e "s/__SPK_VERSION__/${ESC_SPK_VERSION}/g" \
  -e "s/__SPK_ARCH__/${ESC_SPK_ARCH}/g" \
  -e "s/__SOURCE_URL__/${ESC_SOURCE_URL}/g" \
  "${SCRIPT_DIR}/spk/INFO.in" \
  >"${SPK_STAGE}/INFO"

cp "${SCRIPT_DIR}/assets/PACKAGE_ICON.PNG" "${SPK_STAGE}/PACKAGE_ICON.PNG"
cp "${SCRIPT_DIR}/assets/PACKAGE_ICON_256.PNG" "${SPK_STAGE}/PACKAGE_ICON_256.PNG"
cp "${SCRIPT_DIR}/spk/conf/privilege" "${SPK_STAGE}/conf/privilege"
if [ "$BUNDLE_MODE" = "offline" ]; then
  cp "${SCRIPT_DIR}/spk/conf/resource.offline.json" "${SPK_STAGE}/conf/resource"
else
  cp "${SCRIPT_DIR}/spk/conf/resource.online.json" "${SPK_STAGE}/conf/resource"
fi
cp "${SCRIPT_DIR}/spk/scripts/"* "${SPK_STAGE}/scripts/"
cp "${SCRIPT_DIR}/spk/WIZARD_UIFILES/"* "${SPK_STAGE}/WIZARD_UIFILES/"

find "$SPK_STAGE" -type d -exec chmod 755 {} +
find "$SPK_STAGE" -type f -exec chmod 644 {} +
chmod 755 "${SPK_STAGE}/scripts/"* "${SPK_STAGE}/WIZARD_UIFILES/"*.sh

# 3. 兼容 macOS bsdtar 与 Linux GNU tar，杜绝 AppleDouble (._*)、xattr 与 ./ 前缀
export COPYFILE_DISABLE=1
TAR_VER=$(tar --version 2>/dev/null || true)

run_clean_tar() {
  mode=$1
  archive=$2
  chdir=$3
  shift 3
  # 离线模式下内层已含 .tar.gz 镜像，用快速压缩避免无意义二次重度压缩
  gzip_env=""
  if [ "$BUNDLE_MODE" = "offline" ]; then
    gzip_env="-1"
  fi
  if printf '%s' "$TAR_VER" | grep -qi 'bsdtar'; then
    if [ "$mode" = "tgz" ]; then
      GZIP="${gzip_env}" tar --no-xattrs --no-mac-metadata --format ustar --uid 0 --gid 0 --uname root --gname root -czf "$archive" -C "$chdir" "$@"
    else
      tar --no-xattrs --no-mac-metadata --format ustar --uid 0 --gid 0 --uname root --gname root -cf "$archive" -C "$chdir" "$@"
    fi
  else
    if [ "$mode" = "tgz" ]; then
      GZIP="${gzip_env}" tar --no-xattrs --format=ustar --owner=0 --group=0 --numeric-owner -czf "$archive" -C "$chdir" "$@"
    else
      tar --no-xattrs --format=ustar --owner=0 --group=0 --numeric-owner -cf "$archive" -C "$chdir" "$@"
    fi
  fi
}

# shellcheck disable=SC2086
run_clean_tar tgz "${SPK_STAGE}/package.tgz" "$PKG_STAGE" $PKG_ENTRIES
chmod 644 "${SPK_STAGE}/package.tgz"

if [ -n "$OUTPUT_FILE" ]; then
  mkdir -p "$(dirname -- "$OUTPUT_FILE")"
  OUT_SPK="$OUTPUT_FILE"
else
  mkdir -p "$OUTPUT_DIR"
  ARCH_SLUG=$(printf '%s' "$SPK_ARCH" | tr ' ' '-')
  if [ "$BUNDLE_MODE" = "online" ]; then
    OUT_SPK="${OUTPUT_DIR}/iosbackup-${ARCH_SLUG}-${SPK_VERSION}-online.spk"
  else
    OUT_SPK="${OUTPUT_DIR}/iosbackup-${ARCH_SLUG}-${SPK_VERSION}.spk"
  fi
fi

run_clean_tar spk "$OUT_SPK" "$SPK_STAGE" \
  INFO \
  PACKAGE_ICON.PNG \
  PACKAGE_ICON_256.PNG \
  conf \
  scripts \
  WIZARD_UIFILES \
  package.tgz

printf 'Built Synology SPK (%s): %s\n' "$BUNDLE_MODE" "$OUT_SPK"
