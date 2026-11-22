
MANIFEST_READER := ./scripts/read_release_manifest.sh
RELEASE_MANIFEST := release/manifest.env
MANIFEST_VALID := $(shell $(MANIFEST_READER) $(RELEASE_MANIFEST) >/dev/null 2>&1 && printf valid)
ifeq ($(MANIFEST_VALID),)
$(error release/manifest.env failed strict validation)
endif

IMAGE ?= iosbackup
TAG ?= dev
VERSION ?= $(shell $(MANIFEST_READER) $(RELEASE_MANIFEST) IOSBK_VERSION)
BUILD_DATE ?= $(shell $(MANIFEST_READER) $(RELEASE_MANIFEST) IOSBK_BUILD_DATE)
COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_URL ?= $(shell $(MANIFEST_READER) $(RELEASE_MANIFEST) IOSBK_SOURCE_URL)
PLATFORM ?= linux/amd64
MULTI_PLATFORMS ?= linux/amd64,linux/arm64/v8
OCI_OUTPUT ?= /tmp/iosbackup-$(TAG).oci
BUILDER ?=
BUILDER_FLAG = $(if $(BUILDER),--builder "$(BUILDER)",)

.PHONY: build build-multi check-public test push spk spk-all spk-online

# 默认 build 只在本机生成镜像，绝不隐式推送。
build:
	test -n "$(SOURCE_URL)"
	docker buildx build $(BUILDER_FLAG) --load --platform "$(PLATFORM)" \
		--build-arg IOSBK_VERSION="$(VERSION)" \
		--build-arg IOSBK_BUILD_DATE="$(BUILD_DATE)" \
		--build-arg IOSBK_COMMIT="$(COMMIT)" \
		--build-arg IOSBK_SOURCE_URL="$(SOURCE_URL)" \
		-t "$(IMAGE):$(TAG)" .

# 多架构镜像导出为本地 OCI archive；发布仍由显式 push 流程负责。
build-multi:
	test -n "$(SOURCE_URL)"
	test -n "$(OCI_OUTPUT)"
	docker buildx build $(BUILDER_FLAG) --platform "$(MULTI_PLATFORMS)" \
		--output "type=oci,dest=$(OCI_OUTPUT)" \
		--build-arg IOSBK_VERSION="$(VERSION)" \
		--build-arg IOSBK_BUILD_DATE="$(BUILD_DATE)" \
		--build-arg IOSBK_COMMIT="$(COMMIT)" \
		--build-arg IOSBK_SOURCE_URL="$(SOURCE_URL)" \
		-t "$(IMAGE):$(TAG)" .

check-public:
	./scripts/check_public_repo.sh --directory .

test:
	go test -count=1 ./...

# 构建群晖 DSM 7.2+ 内置离线镜像全量套件（默认跟随 release/manifest.env 版本，无需 NAS 联网拉取镜像）。
SPK_ARCH ?= x86_64
SPK_VERSION ?= $(VERSION)
SPK_BUILD_NUM ?= 0001
spk:
	./packaging/synology/build_spk.sh --offline --arch "$(SPK_ARCH)" --version "$(SPK_VERSION)" --build-num "$(SPK_BUILD_NUM)"

# 同时构建 x86_64 与 armv8 两个架构的离线全量套件。
spk-all:
	./packaging/synology/build_spk.sh --offline --arch x86_64 --version "$(SPK_VERSION)" --build-num "$(SPK_BUILD_NUM)"
	./packaging/synology/build_spk.sh --offline --arch armv8 --version "$(SPK_VERSION)" --build-num "$(SPK_BUILD_NUM)"

# 构建轻量在线拉取版套件（体积约 100 KB）。
spk-online:
	./packaging/synology/build_spk.sh --online --arch "$(SPK_ARCH)" --version "$(SPK_VERSION)" --build-num "$(SPK_BUILD_NUM)"

# 发布必须显式给出完整目标，例如：
# make push IMAGE=iosbackup TAG=$(IOSBK_VERSION) PUBLISH_IMAGE=ghcr.io/example/iosbackup:$(IOSBK_VERSION)
push:
	test -n "$(PUBLISH_IMAGE)"
	docker tag "$(IMAGE):$(TAG)" "$(PUBLISH_IMAGE)"
	docker push "$(PUBLISH_IMAGE)"
