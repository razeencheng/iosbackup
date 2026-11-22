#!/bin/sh
set -eu

OUT_FILE="${SYNOPKG_TEMP_LOGFILE:-/dev/stdout}"
BASE_PORT="${IOSBK_WIZARD_BASE_PORT:-9000}"
VOL_ROOT="${IOSBK_TEST_VOL_ROOT:-}"

is_port_in_use() {
  port_num=$1
  if command -v ss >/dev/null 2>&1; then
    if ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "(^|:|\\])${port_num}$"; then
      return 0
    fi
  elif command -v netstat >/dev/null 2>&1; then
    if netstat -an 2>/dev/null | grep -Ei 'LISTEN' | awk '{print $4}' | grep -Eq "(^|:|\\.)${port_num}$"; then
      return 0
    fi
  fi
  return 1
}

pick_default_port() {
  candidate=$BASE_PORT
  max_try=$((BASE_PORT + 99))
  while [ "$candidate" -le "$max_try" ]; do
    if [ "$candidate" != "5000" ] && [ "$candidate" != "5001" ] && [ "$candidate" != "27015" ]; then
      if ! is_port_in_use "$candidate"; then
        printf '%s' "$candidate"
        return 0
      fi
    fi
    candidate=$((candidate + 1))
  done
  printf '%s' "$BASE_PORT"
}

generate_default_password() {
  if command -v openssl >/dev/null 2>&1; then
    pwd_val=$(openssl rand -base64 24 2>/dev/null | tr '+/' '-_' | tr -d '=\r\n' || true)
    if [ "${#pwd_val}" -ge 24 ]; then
      printf '%s' "$pwd_val"
      return 0
    fi
  fi
  pwd_val=$(LC_ALL=C tr -dc 'A-Za-z0-9_-' </dev/urandom 2>/dev/null | head -c 32 || true)
  if [ "${#pwd_val}" -ge 24 ]; then
    printf '%s' "$pwd_val"
    return 0
  fi
  printf 'ChangeMe-iOSBackup-Admin-%s' "$(date +%s)"
}

build_storage_desc() {
  lang_mode=$1
  vol_count=0
  single_vol=""
  single_avail=""
  multi_list=""

  for vol in "${VOL_ROOT}"/volume[0-9]*; do
    [ -d "$vol" ] || continue
    vol_count=$((vol_count + 1))
    disp_vol="/$(basename "$vol")"
    avail=$(df -Ph "$vol" 2>/dev/null | awk 'NR==2 {print $4}' | tr -cd 'A-Za-z0-9.%' || true)

    if [ "$vol_count" -eq 1 ]; then
      single_vol="$disp_vol"
      single_avail="$avail"
    fi

    if [ "$lang_mode" = "chs" ]; then
      if [ -n "$avail" ]; then
        entry="${disp_vol} 可用 ${avail}"
      else
        entry="${disp_vol}"
      fi
      sep="、"
    else
      if [ -n "$avail" ]; then
        entry="${disp_vol} ${avail} free"
      else
        entry="${disp_vol}"
      fi
      sep=", "
    fi

    if [ -z "$multi_list" ]; then
      multi_list="$entry"
    else
      multi_list="${multi_list}${sep}${entry}"
    fi
  done

  if [ "$lang_mode" = "chs" ]; then
    if [ "$vol_count" -eq 1 ]; then
      if [ -n "$single_avail" ]; then
        printf '默认保存在 %s（可用 %s）的 iosbackup 共享文件夹下；也可填写其它已有共享文件夹名（卸载套件不删数据）。' "$single_vol" "$single_avail"
      else
        printf '默认保存在 %s 的 iosbackup 共享文件夹下；也可填写其它已有共享文件夹名（卸载套件不删数据）。' "$single_vol"
      fi
    elif [ "$vol_count" -gt 1 ]; then
      printf '新建文件夹将保存在套件安装的目标存储空间（检测到：%s）；填写已有共享文件夹名则直接使用其所在存储空间（卸载不删数据）。' "$multi_list"
    else
      printf '新建文件夹将保存在套件安装的目标存储空间；填写已有共享文件夹名则直接使用其所在存储空间（卸载套件不删数据）。'
    fi
  else
    if [ "$vol_count" -eq 1 ]; then
      if [ -n "$single_avail" ]; then
        printf 'Saved by default under %s (%s free) in shared folder iosbackup; or enter an existing shared folder name (kept on uninstall).' "$single_vol" "$single_avail"
      else
        printf 'Saved by default under %s in shared folder iosbackup; or enter an existing shared folder name (kept on uninstall).' "$single_vol"
      fi
    elif [ "$vol_count" -gt 1 ]; then
      printf 'New folder will be created on the package installation volume (detected: %s); entering an existing shared folder uses its volume directly (kept on uninstall).' "$multi_list"
    else
      printf 'New folder will be created on the package installation volume; entering an existing shared folder uses its volume directly (kept on uninstall).'
    fi
  fi
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

DEFAULT_PORT=$(pick_default_port)
DEFAULT_PWD=$(generate_default_password)

case "${SYNOPKG_DSM_LANGUAGE:-chs}" in
  chs|cht)
    STORAGE_DESC=$(build_storage_desc chs)
    if [ "$DEFAULT_PORT" != "$BASE_PORT" ]; then
      PORT_DESC="检测到 ${BASE_PORT} 端口已被占用，已自动切换为 ${DEFAULT_PORT}（插线测试 USB 前请先停用旧容器）；若配置了反向代理，可填写下方自定义访问地址以便点「打开」时直接跳转。"
    else
      PORT_DESC="设置容器监听端口（范围 1024–65535）；若配置了反向代理域名，可在下方选填完整访问地址，点「打开」将直接跳转该地址。"
    fi

    ESC_STORAGE_DESC=$(escape_sed "$STORAGE_DESC")
    ESC_PORT=$(escape_sed "$DEFAULT_PORT")
    ESC_PWD=$(escape_sed "$DEFAULT_PWD")
    ESC_PORT_DESC=$(escape_sed "$PORT_DESC")

    sed \
      -e "s/__STORAGE_DESC__/${ESC_STORAGE_DESC}/g" \
      -e "s/__DEFAULT_PORT__/${ESC_PORT}/g" \
      -e "s/__DEFAULT_PWD__/${ESC_PWD}/g" \
      -e "s/__PORT_DESC__/${ESC_PORT_DESC}/g" \
      <<'EOF' >"$OUT_FILE"
[
  {
    "step_title": "存储位置与网络端口",
    "items": [
      {
        "type": "textfield",
        "desc": "__STORAGE_DESC__",
        "subitems": [
          {
            "key": "pkgwizard_share_name",
            "desc": "共享文件夹名称",
            "defaultValue": "iosbackup",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]{1,32}$/",
                "errorText": "请输入合法的共享文件夹名称（1–32 位字母、数字、点、下划线或连字符，不含斜杠）"
              }
            }
          },
          {
            "key": "pkgwizard_backup_subdir",
            "desc": "设备备份子目录",
            "defaultValue": "backups",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "请输入合法的相对子目录路径（如 backups 或 iosbackup/backups，开头结尾不加 /）"
              }
            }
          },
          {
            "key": "pkgwizard_config_subdir",
            "desc": "应用配置子目录",
            "defaultValue": "configs",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "请输入合法的相对子目录路径（如 configs 或 iosbackup/configs，不能与备份子目录相同）"
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "__PORT_DESC__",
        "subitems": [
          {
            "key": "pkgwizard_port",
            "desc": "Web 控制台端口",
            "defaultValue": "__DEFAULT_PORT__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(102[4-9]|10[3-9][0-9]|1[1-9][0-9]{2}|[2-9][0-9]{3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$/",
                "errorText": "请输入 1024 至 65535 之间的有效端口号"
              }
            }
          },
          {
            "key": "pkgwizard_external_url",
            "desc": "自定义访问地址",
            "defaultValue": "",
            "emptyText": "选填，如 https://ios.example.com（反向代理用）",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^https?:\\/\\/([A-Za-z0-9-]+\\.)*[A-Za-z0-9-]+(:[0-9]{1,5})?\\/?$|^https?:\\/\\/\\[[0-9A-Fa-f:]+\\](:[0-9]{1,5})?\\/?$/",
                "errorText": "留空，或填写以 http:// 或 https:// 开头的完整域名地址（如 https://ios.example.com:8443，不含子路径）"
              }
            }
          }
        ]
      }
    ]
  },
  {
    "step_title": "管理员密码与基础配置",
    "items": [
      {
        "type": "textfield",
        "desc": "请复制保存下方预生成的 32 位随机密码，或修改为自定义密码（至少 24 位）。",
        "subitems": [
          {
            "key": "pkgwizard_admin_password",
            "desc": "管理员密码（≥24位）",
            "defaultValue": "__DEFAULT_PWD__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^.{24,128}$/",
                "errorText": "请输入 24 至 128 个字符的高强度密码"
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "设置计划备份时区、日志级别（WARN / INFO / DEBUG）及存储空间最低保留余量（填 0 表示不限制）。",
        "subitems": [
          {
            "key": "pkgwizard_timezone",
            "desc": "时区（TZ）",
            "defaultValue": "Asia/Shanghai",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(UTC|[A-Za-z0-9_+-]+(\\/[A-Za-z0-9_+-]+)+)$/",
                "errorText": "请输入有效的 IANA 时区名称，例如 Asia/Shanghai 或 UTC"
              }
            }
          },
          {
            "key": "pkgwizard_log_level",
            "desc": "日志级别",
            "defaultValue": "INFO",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(WARN|INFO|DEBUG)$/",
                "errorText": "仅支持填写 WARN、INFO 或 DEBUG"
              }
            }
          },
          {
            "key": "pkgwizard_min_free_gb",
            "desc": "磁盘最低预留(GB)",
            "defaultValue": "10",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^([0-9]|[1-9][0-9]{1,3})$/",
                "errorText": "请输入 0 至 9999 之间的整数（填 0 表示不限制）"
              }
            }
          }
        ]
      }
    ]
  },
  {
    "step_title": "高级与通知选项",
    "items": [
      {
        "type": "textfield",
        "desc": "一般保持默认即可。如需向局域网自建服务发送 Webhook 通知，可填写私网 CIDR 白名单（多项用英文逗号分隔，如 192.168.0.0/16,10.0.0.0/8）。",
        "subitems": [
          {
            "key": "pkgwizard_max_heavy_jobs",
            "desc": "最大并发备份数",
            "defaultValue": "1",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[12]$/",
                "errorText": "仅支持填写 1 或 2"
              }
            }
          },
          {
            "key": "pkgwizard_plist_max_mib",
            "desc": "清单解析上限(MiB)",
            "defaultValue": "64",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-6])$/",
                "errorText": "请输入 1 至 256 之间的整数"
              }
            }
          },
          {
            "key": "pkgwizard_webhook_cidrs",
            "desc": "内网 Webhook 网段",
            "defaultValue": "",
            "emptyText": "例如 192.168.0.0/16,10.0.0.0/8",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^\\s*[0-9A-Fa-f:.\\/]+(\\s*,\\s*[0-9A-Fa-f:.\\/]+)*\\s*$/",
                "errorText": "留空或填写英文逗号分隔的私网 CIDR（如 192.168.0.0/16）"
              }
            }
          }
        ]
      },
      {
        "type": "multiselect",
        "desc": "默认关闭；仅在需要使用整机恢复、本地解包或删除本地备份时勾选。",
        "subitems": [
          {
            "key": "pkgwizard_enable_experimental",
            "desc": "启用实验性高级操作（整机恢复 / 本地解包 / 删除备份）",
            "defaultValue": false
          }
        ]
      }
    ]
  }
]
EOF
    ;;
  *)
    STORAGE_DESC=$(build_storage_desc enu)
    if [ "$DEFAULT_PORT" != "$BASE_PORT" ]; then
      PORT_DESC="Port ${BASE_PORT} is already in use; switched automatically to ${DEFAULT_PORT} (stop any legacy container before USB testing)."
    else
      PORT_DESC="Set the Web UI listening port (1024–65535; clicking the DSM Main Menu icon opens it automatically)."
    fi

    ESC_STORAGE_DESC=$(escape_sed "$STORAGE_DESC")
    ESC_PORT=$(escape_sed "$DEFAULT_PORT")
    ESC_PWD=$(escape_sed "$DEFAULT_PWD")
    ESC_PORT_DESC=$(escape_sed "$PORT_DESC")

    sed \
      -e "s/__STORAGE_DESC__/${ESC_STORAGE_DESC}/g" \
      -e "s/__DEFAULT_PORT__/${ESC_PORT}/g" \
      -e "s/__DEFAULT_PWD__/${ESC_PWD}/g" \
      -e "s/__PORT_DESC__/${ESC_PORT_DESC}/g" \
      <<'EOF' >"$OUT_FILE"
[
  {
    "step_title": "Storage Location & Network Port",
    "items": [
      {
        "type": "textfield",
        "desc": "__STORAGE_DESC__",
        "subitems": [
          {
            "key": "pkgwizard_share_name",
            "desc": "Shared folder name",
            "defaultValue": "iosbackup",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]{1,32}$/",
                "errorText": "Enter a valid shared folder name (1–32 chars: letters, digits, ., _, -)."
              }
            }
          },
          {
            "key": "pkgwizard_backup_subdir",
            "desc": "Backups subdirectory",
            "defaultValue": "backups",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "Enter a clean relative path such as backups or iosbackup/backups."
              }
            }
          },
          {
            "key": "pkgwizard_config_subdir",
            "desc": "Configs subdirectory",
            "defaultValue": "configs",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "Enter a clean relative path such as configs (must differ from backups subdirectory)."
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "__PORT_DESC__",
        "subitems": [
          {
            "key": "pkgwizard_port",
            "desc": "Web console port",
            "defaultValue": "__DEFAULT_PORT__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(102[4-9]|10[3-9][0-9]|1[1-9][0-9]{2}|[2-9][0-9]{3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$/",
                "errorText": "Please enter a valid port between 1024 and 65535."
              }
            }
          },
          {
            "key": "pkgwizard_external_url",
            "desc": "Custom external URL",
            "defaultValue": "",
            "emptyText": "Optional, e.g. https://ios.example.com (for reverse proxy)",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^https?:\\/\\/([A-Za-z0-9-]+\\.)*[A-Za-z0-9-]+(:[0-9]{1,5})?\\/?$|^https?:\\/\\/\\[[0-9A-Fa-f:]+\\](:[0-9]{1,5})?\\/?$/",
                "errorText": "Leave blank or enter an http(s):// domain URL without subpath (e.g. https://ios.example.com)."
              }
            }
          }
        ]
      }
    ]
  },
  {
    "step_title": "Admin Password & Base Settings",
    "items": [
      {
        "type": "textfield",
        "desc": "Copy and save the generated 32-character password below, or replace it with your own (>=24 characters).",
        "subitems": [
          {
            "key": "pkgwizard_admin_password",
            "desc": "Admin password (>=24 chars)",
            "defaultValue": "__DEFAULT_PWD__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^.{24,128}$/",
                "errorText": "Please enter a strong password between 24 and 128 characters."
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "Configure scheduled backup timezone, log verbosity (WARN / INFO / DEBUG), and minimum free disk space (0 disables limit).",
        "subitems": [
          {
            "key": "pkgwizard_timezone",
            "desc": "Timezone (TZ)",
            "defaultValue": "Asia/Shanghai",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(UTC|[A-Za-z0-9_+-]+(\\/[A-Za-z0-9_+-]+)+)$/",
                "errorText": "Enter a valid IANA timezone such as Asia/Shanghai or UTC."
              }
            }
          },
          {
            "key": "pkgwizard_log_level",
            "desc": "Log level",
            "defaultValue": "INFO",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(WARN|INFO|DEBUG)$/",
                "errorText": "Allowed values: WARN, INFO, DEBUG."
              }
            }
          },
          {
            "key": "pkgwizard_min_free_gb",
            "desc": "Min free space (GB)",
            "defaultValue": "10",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^([0-9]|[1-9][0-9]{1,3})$/",
                "errorText": "Enter an integer between 0 and 9999 (0 disables check)."
              }
            }
          }
        ]
      }
    ]
  },
  {
    "step_title": "Advanced & Notification Options",
    "items": [
      {
        "type": "textfield",
        "desc": "Defaults work well for most NAS setups. For LAN webhook notifications, enter comma-separated private CIDRs (e.g. 192.168.0.0/16,10.0.0.0/8).",
        "subitems": [
          {
            "key": "pkgwizard_max_heavy_jobs",
            "desc": "Max concurrent backups",
            "defaultValue": "1",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[12]$/",
                "errorText": "Allowed values: 1 or 2."
              }
            }
          },
          {
            "key": "pkgwizard_plist_max_mib",
            "desc": "Max plist size (MiB)",
            "defaultValue": "64",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^([1-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-6])$/",
                "errorText": "Enter an integer between 1 and 256."
              }
            }
          },
          {
            "key": "pkgwizard_webhook_cidrs",
            "desc": "LAN Webhook CIDRs",
            "defaultValue": "",
            "emptyText": "e.g. 192.168.0.0/16,10.0.0.0/8",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^\\s*[0-9A-Fa-f:.\\/]+(\\s*,\\s*[0-9A-Fa-f:.\\/]+)*\\s*$/",
                "errorText": "Leave blank or enter comma-separated private CIDRs (e.g. 192.168.0.0/16)."
              }
            }
          }
        ]
      },
      {
        "type": "multiselect",
        "desc": "Disabled by default; enable only if you need whole-device restore, local backup unpack, or local backup deletion.",
        "subitems": [
          {
            "key": "pkgwizard_enable_experimental",
            "desc": "Enable experimental operations (restore / unpack / delete)",
            "defaultValue": false
          }
        ]
      }
    ]
  }
]
EOF
    ;;
esac
