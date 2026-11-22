# 群晖 DSM 套件（`.spk`）构建与安装说明

本目录提供适用于 **群晖 DSM 7.2+（Container Manager）** 的 `.spk` 套件打包脚手架，默认读取 [`release/manifest.env`](../../release/manifest.env) 中的发布版本封装**内置离线 Docker 镜像全量 `.spk`**（NAS 全程无需联网访问 `ghcr.io` 即可完成安装），并在安装与升级向导中提供完整的目录、端口与高级运行参数配置。

## 1. 构建 `.spk`

无需在本机启动 Docker 守护进程或安装群晖重型交叉编译工具链，打包工具会直接通过 Go 标准库从镜像仓库拉取并逐层校验 SHA-256 摘要，封装为 Container Manager 原生支持的 `preload-image` 离线归档：

```bash
# 构建 x86_64 离线全量套件（默认跟随 release/manifest.env 版本，输出至 ./dist/）
make spk

# 一次性构建 x86_64 与 armv8 两个架构的离线全量套件
make spk-all

# 如需指定其它已发布版本或本地已导出的镜像 tar/tar.gz
./packaging/synology/build_spk.sh --arch x86_64 --version v1.5.7
./packaging/synology/build_spk.sh --arch x86_64 --image-tar /path/to/image.tar.gz

# 构建不含镜像的在线轻量包（约 100 KB）
make spk-online
```

## 2. 三步安装与升级向导支持的配置项

上传 `.spk` 安装（或后续覆盖升级）时，`postinst` 会将向导参数直接渲染进 `$SYNOPKG_PKGDEST/compose/docker-compose.yml` 与 DSM 桌面入口 `$SYNOPKG_PKGDEST/ui/config`，并由 `force_recreate: true` 驱动 Container Manager 立即按新配置重建容器：

### 第 1 步：存储位置与网络端口
1. **共享文件夹名称（`pkgwizard_share_name`）**：默认 `iosbackup`（自动探测并提示所在 `/volumeX` 存储空间及可用容量，不存在时自动创建）；也可填写其它已有共享文件夹名（卸载套件**绝不会删除**该共享文件夹）。
2. **设备备份子目录（`pkgwizard_backup_subdir`）**：默认 `backups`；支持多级相对路径（如 `iosbackup/data/backups`）。
3. **应用配置子目录（`pkgwizard_config_subdir`）**：默认 `configs`（如 `iosbackup/data/configs`）。
4. **Web 控制台端口（`pkgwizard_port`）**：默认 `9000`；若检测到冲突会自动顺延为 `9001`。
5. **自定义访问地址（`pkgwizard_external_url`，选填）**：默认留空（点击「打开」跳转至 `http://<群晖IP>:<端口>/`）；若已在群晖配置反向代理域名（如 `https://ios.example.com`），填写后无论在套件中心还是 DSM 主菜单点击「打开」都会一律直接跳转至该自定义域名地址。

### 第 2 步：管理员密码与基础配置
6. **管理员密码（`pkgwizard_admin_password`）**：首次安装预填 32 位高强度随机密码（支持自定义 ≥24 位密码）；升级时留空则保持原密码不变。
7. **时区（`pkgwizard_timezone`）**：默认 `Asia/Shanghai`。
8. **日志级别（`pkgwizard_log_level`）**：默认 `INFO`（可选 `WARN` / `INFO` / `DEBUG`）。
9. **磁盘最低预留空间（`pkgwizard_min_free_gb`）**：默认 `10` GB（对应 `IOSBK_MIN_FREE_BYTES`，填 `0` 表示不限制）。

### 第 3 步：高级与通知选项
10. **最大并发备份数（`pkgwizard_max_heavy_jobs`）**：默认 `1`（可选 `1` 或 `2`）。
11. **清单解析上限（`pkgwizard_plist_max_mib`）**：默认 `64` MiB（范围 `1–256`）。
12. **内网 Webhook 网段（`pkgwizard_webhook_cidrs`）**：默认留空；如需向局域网自建通知服务推送，可填写英文逗号分隔的私网 CIDR（如 `192.168.0.0/16,10.0.0.0/8`）。
13. **实验性高级操作开关（`pkgwizard_enable_experimental`）**：默认关闭；勾选后启用整机恢复、本地解包与删除本地备份。
