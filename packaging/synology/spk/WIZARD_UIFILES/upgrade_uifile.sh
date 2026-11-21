#!/bin/sh
set -eu

OUT_FILE="${SYNOPKG_TEMP_LOGFILE:-/dev/stdout}"
PKG_VAR="${SYNOPKG_PKGVAR:-/var/packages/iosbackup/var}"

read_saved_or_default() {
  file_path=$1
  fallback=$2
  if [ -f "$file_path" ]; then
    val=$(tr -d '\r\n' <"$file_path" 2>/dev/null || true)
    printf '%s' "$val"
    return 0
  fi
  printf '%s' "$fallback"
}

read_nonempty_or_default() {
  file_path=$1
  fallback=$2
  if [ -s "$file_path" ]; then
    val=$(tr -d '\r\n' <"$file_path" 2>/dev/null || true)
    if [ -n "$val" ]; then
      printf '%s' "$val"
      return 0
    fi
  fi
  printf '%s' "$fallback"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

CUR_SHARE=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/share_name" "iosbackup")")
CUR_BACKUP_SUB=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/backup_subdir" "backups")")
CUR_CONFIG_SUB=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/config_subdir" "configs")")
CUR_PORT=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/port" "9000")")
CUR_EXTERNAL_URL=$(escape_sed "$(read_saved_or_default "${PKG_VAR}/external_url" "")")
CUR_MIN_FREE_GB=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/min_free_gb" "10")")
CUR_TZ=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/timezone" "Asia/Shanghai")")
CUR_LOG=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/log_level" "INFO")")
CUR_MAX_HEAVY=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/max_heavy_jobs" "1")")
CUR_PLIST_MIB=$(escape_sed "$(read_nonempty_or_default "${PKG_VAR}/plist_max_mib" "64")")
CUR_WEBHOOK_CIDRS=$(escape_sed "$(read_saved_or_default "${PKG_VAR}/webhook_cidrs" "")")
RAW_EXP=$(read_nonempty_or_default "${PKG_VAR}/enable_experimental" "false")
if [ "$RAW_EXP" = "true" ]; then
  EXP_BOOL="true"
else
  EXP_BOOL="false"
fi

case "${SYNOPKG_DSM_LANGUAGE:-chs}" in
  chs|cht)
    sed \
      -e "s/__CUR_SHARE__/${CUR_SHARE}/g" \
      -e "s/__CUR_BACKUP_SUB__/${CUR_BACKUP_SUB}/g" \
      -e "s/__CUR_CONFIG_SUB__/${CUR_CONFIG_SUB}/g" \
      -e "s/__CUR_PORT__/${CUR_PORT}/g" \
      -e "s/__CUR_EXTERNAL_URL__/${CUR_EXTERNAL_URL}/g" \
      -e "s/__CUR_MIN_FREE_GB__/${CUR_MIN_FREE_GB}/g" \
      -e "s/__CUR_TZ__/${CUR_TZ}/g" \
      -e "s/__CUR_LOG__/${CUR_LOG}/g" \
      -e "s/__CUR_MAX_HEAVY__/${CUR_MAX_HEAVY}/g" \
      -e "s/__CUR_PLIST_MIB__/${CUR_PLIST_MIB}/g" \
      -e "s/__CUR_WEBHOOK_CIDRS__/${CUR_WEBHOOK_CIDRS}/g" \
      -e "s/__EXP_BOOL__/${EXP_BOOL}/g" \
      <<'EOF' >"$OUT_FILE"
[
  {
    "step_title": "存储位置与网络端口",
    "items": [
      {
        "type": "textfield",
        "desc": "以下已自动带出您当前的存储目录配置，可直接点击下一步保持不变，或按需调整。",
        "subitems": [
          {
            "key": "pkgwizard_share_name",
            "desc": "共享文件夹名称",
            "defaultValue": "__CUR_SHARE__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]{1,32}$/",
                "errorText": "请输入合法的共享文件夹名称"
              }
            }
          },
          {
            "key": "pkgwizard_backup_subdir",
            "desc": "设备备份子目录",
            "defaultValue": "__CUR_BACKUP_SUB__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "请输入合法的相对子目录路径"
              }
            }
          },
          {
            "key": "pkgwizard_config_subdir",
            "desc": "应用配置子目录",
            "defaultValue": "__CUR_CONFIG_SUB__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "请输入合法的相对子目录路径"
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "确认容器监听端口；若填写了自定义访问地址（如反向代理域名），点击「打开」将直接跳转该地址（清空则恢复按 群晖地址:端口 打开）。",
        "subitems": [
          {
            "key": "pkgwizard_port",
            "desc": "Web 控制台端口",
            "defaultValue": "__CUR_PORT__",
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
            "defaultValue": "__CUR_EXTERNAL_URL__",
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
        "desc": "留空则保持原密码不变，输入 ≥24 位字符则重置密码。",
        "subitems": [
          {
            "key": "pkgwizard_admin_password",
            "desc": "重置密码（留空不改）",
            "defaultValue": "",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^.{24,128}$/",
                "errorText": "留空保持原密码，或输入 24 至 128 个字符的新密码"
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
            "defaultValue": "__CUR_TZ__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(UTC|[A-Za-z0-9_+-]+(\\/[A-Za-z0-9_+-]+)+)$/",
                "errorText": "请输入有效的 IANA 时区名称"
              }
            }
          },
          {
            "key": "pkgwizard_log_level",
            "desc": "日志级别",
            "defaultValue": "__CUR_LOG__",
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
            "defaultValue": "__CUR_MIN_FREE_GB__",
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
            "defaultValue": "__CUR_MAX_HEAVY__",
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
            "defaultValue": "__CUR_PLIST_MIB__",
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
            "defaultValue": "__CUR_WEBHOOK_CIDRS__",
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
            "defaultValue": __EXP_BOOL__
          }
        ]
      }
    ]
  }
]
EOF
    ;;
  *)
    sed \
      -e "s/__CUR_SHARE__/${CUR_SHARE}/g" \
      -e "s/__CUR_BACKUP_SUB__/${CUR_BACKUP_SUB}/g" \
      -e "s/__CUR_CONFIG_SUB__/${CUR_CONFIG_SUB}/g" \
      -e "s/__CUR_PORT__/${CUR_PORT}/g" \
      -e "s/__CUR_EXTERNAL_URL__/${CUR_EXTERNAL_URL}/g" \
      -e "s/__CUR_MIN_FREE_GB__/${CUR_MIN_FREE_GB}/g" \
      -e "s/__CUR_TZ__/${CUR_TZ}/g" \
      -e "s/__CUR_LOG__/${CUR_LOG}/g" \
      -e "s/__CUR_MAX_HEAVY__/${CUR_MAX_HEAVY}/g" \
      -e "s/__CUR_PLIST_MIB__/${CUR_PLIST_MIB}/g" \
      -e "s/__CUR_WEBHOOK_CIDRS__/${CUR_WEBHOOK_CIDRS}/g" \
      -e "s/__EXP_BOOL__/${EXP_BOOL}/g" \
      <<'EOF' >"$OUT_FILE"
[
  {
    "step_title": "Storage Location & Network Port",
    "items": [
      {
        "type": "textfield",
        "desc": "Your existing storage paths are pre-filled below. Click Next to keep them unchanged or update as needed.",
        "subitems": [
          {
            "key": "pkgwizard_share_name",
            "desc": "Shared folder name",
            "defaultValue": "__CUR_SHARE__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]{1,32}$/",
                "errorText": "Enter a valid shared folder name."
              }
            }
          },
          {
            "key": "pkgwizard_backup_subdir",
            "desc": "Backups subdirectory",
            "defaultValue": "__CUR_BACKUP_SUB__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "Enter a clean relative path."
              }
            }
          },
          {
            "key": "pkgwizard_config_subdir",
            "desc": "Configs subdirectory",
            "defaultValue": "__CUR_CONFIG_SUB__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^[A-Za-z0-9._-]+(\\/[A-Za-z0-9._-]+)*$/",
                "errorText": "Enter a clean relative path."
              }
            }
          }
        ]
      },
      {
        "type": "textfield",
        "desc": "Confirm container port and optional custom external URL (when set, clicking Open launches that URL directly; clear to revert to NAS-host:port).",
        "subitems": [
          {
            "key": "pkgwizard_port",
            "desc": "Web console port",
            "defaultValue": "__CUR_PORT__",
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
            "defaultValue": "__CUR_EXTERNAL_URL__",
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
        "desc": "Leave blank to keep your existing administrator password, or enter >=24 characters to reset it.",
        "subitems": [
          {
            "key": "pkgwizard_admin_password",
            "desc": "Reset password (optional)",
            "defaultValue": "",
            "validator": {
              "allowBlank": true,
              "regex": {
                "expr": "/^$|^.{24,128}$/",
                "errorText": "Leave blank to keep existing password, or enter 24–128 characters."
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
            "defaultValue": "__CUR_TZ__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^(UTC|[A-Za-z0-9_+-]+(\\/[A-Za-z0-9_+-]+)+)$/",
                "errorText": "Enter a valid IANA timezone."
              }
            }
          },
          {
            "key": "pkgwizard_log_level",
            "desc": "Log level",
            "defaultValue": "__CUR_LOG__",
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
            "defaultValue": "__CUR_MIN_FREE_GB__",
            "validator": {
              "allowBlank": false,
              "regex": {
                "expr": "/^([0-9]|[1-9][0-9]{1,3})$/",
                "errorText": "Enter an integer between 0 and 9999."
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
            "defaultValue": "__CUR_MAX_HEAVY__",
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
            "defaultValue": "__CUR_PLIST_MIB__",
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
            "defaultValue": "__CUR_WEBHOOK_CIDRS__",
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
            "defaultValue": __EXP_BOOL__
          }
        ]
      }
    ]
  }
]
EOF
    ;;
esac
