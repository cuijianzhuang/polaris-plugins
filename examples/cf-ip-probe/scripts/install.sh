#!/bin/sh
# cf-ip-probe one-click installer (Linux: systemd, OpenWrt procd, Alpine OpenRC).
#
#   curl -fsSL https://github.com/cuijianzhuang/polaris-plugins/releases/latest/download/install.sh | sh -s -- \
#     --api https://panel.example.com/<后台路径>/api/v1 --token plt_… --profile <配置档 ID> \
#     --source 上海电信 --host cdn.example.com
#
# Missing values are asked for when a terminal is attached. Run it again to
# upgrade or change settings; --uninstall removes everything.
#
# Options:
#   --api URL --token T --profile ID[,ID] --source NAME --host DOMAIN
#   --region CODE    measurement point province/region, e.g. CN-31 (Shanghai)
#   --carrier ct|cu|cm  this point's carrier (default: read from --source)
#   --instance NAME  independent service, binary and settings for each port/profile
#   --args "…"       extra cf-ip-probe flags, e.g. "-interval 2h -colo HKG -dd"
#   --version X.Y.Z  install this release instead of the latest
#   --mirror PREFIX  GitHub download prefix for networks where github.com is slow
#                    (PREFIX is put in front of each github.com URL; env GH_PROXY)
#   --dir DIR        where to put the binary (default /usr/local/bin, /usr/bin on OpenWrt)
#   --auto-update on|off  check for a new release once a day and install it
#                    (default on; settings are kept)
#   --update         install only when a newer release exists (used by the
#                    daily check; never asks questions)
#   --test           run one dry-run round before starting the service
#   --no-start       install only
#   --uninstall
set -eu

REPO=cuijianzhuang/polaris-plugins
APP=cf-ip-probe
INSTANCE=

API='' TOKEN='' PROFILE='' SOURCE='' CARRIER='' REGION='' HOST='' ARGS='' VERSION='' MIRROR=${GH_PROXY:-}
DIR='' TEST=0 START=1 UNINSTALL=0 UPDATE=0 AUTO_UPDATE=''

say() { printf '%s\n' "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

need_arg() { [ $# -ge 2 ] || die "$1 需要一个值"; }
while [ $# -gt 0 ]; do
  case $1 in
    --instance) need_arg "$@"; INSTANCE=$2; shift ;;
    --api) need_arg "$@"; API=$2; shift ;;
    --token) need_arg "$@"; TOKEN=$2; shift ;;
    --profile) need_arg "$@"; PROFILE=$2; shift ;;
    --source) need_arg "$@"; SOURCE=$2; shift ;;
    --host) need_arg "$@"; HOST=$2; shift ;;
    --carrier) need_arg "$@"; CARRIER=$2; shift ;;
    --region) need_arg "$@"; REGION=$2; shift ;;
    --args) need_arg "$@"; ARGS=$2; shift ;;
    --version) need_arg "$@"; VERSION=${2#v}; shift ;;
    --mirror) need_arg "$@"; MIRROR=$2; shift ;;
    --dir) need_arg "$@"; DIR=$2; shift ;;
    --auto-update) need_arg "$@"; AUTO_UPDATE=$2; shift ;;
    --update) UPDATE=1 ;;
    --test) TEST=1 ;;
    --no-start) START=0 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help) sed -n '2,25p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "未知参数 $1（--help 查看用法）" ;;
  esac
  shift
done

case $INSTANCE in *[!a-zA-Z0-9_-]*) die "--instance 只能包含字母、数字、_ 和 -" ;; esac
[ "${#INSTANCE}" -le 40 ] || die "--instance 最长 40 个字符"
case $INSTANCE in update|*-update) die "--instance 不能使用保留的 update 或 -update 后缀" ;; esac
NAME=$APP
[ -z "$INSTANCE" ] || NAME=$APP-$INSTANCE
ENVFILE=/etc/$NAME.env

[ "$(id -u)" = 0 ] || die "请用 root 运行（例如在命令前加 sudo）"
[ "$(uname -s)" = Linux ] || die "一键安装只支持 Linux；其他系统请到 Releases 页面下载对应压缩包"

# --- service manager -------------------------------------------------------
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
  INIT=systemd
elif [ -f /etc/rc.common ] && [ -x /sbin/procd ]; then
  INIT=procd
elif command -v openrc-run >/dev/null 2>&1 || [ -x /sbin/openrc-run ]; then
  INIT=openrc
else
  INIT=none
fi

if [ -z "$DIR" ]; then
  if [ "$INIT" = procd ]; then DIR=/usr/bin; else DIR=/usr/local/bin; fi
fi
BIN=$DIR/$NAME
UPDATER=$DIR/$NAME-update
INSTALLER=$DIR/$NAME-install.sh
CRONTAB=/etc/crontabs/root

stop_service() {
  case $INIT in
    systemd) systemctl disable --now "$NAME" >/dev/null 2>&1 || true ;;
    procd|openrc)
      if [ -x /etc/init.d/"$NAME" ]; then
        /etc/init.d/"$NAME" stop >/dev/null 2>&1 || true
        if [ "$INIT" = procd ]; then /etc/init.d/"$NAME" disable >/dev/null 2>&1 || true
        else rc-update del "$NAME" default >/dev/null 2>&1 || true; fi
      fi ;;
  esac
}

# Daily update check: a systemd timer, or a cron line on OpenWrt / Alpine.
reload_cron() {
  if [ -x /etc/init.d/cron ]; then
    /etc/init.d/cron enable >/dev/null 2>&1 || true
    /etc/init.d/cron restart >/dev/null 2>&1 || true
  elif [ -x /etc/init.d/crond ]; then
    rc-update add crond default >/dev/null 2>&1 || true
    rc-service crond restart >/dev/null 2>&1 || true
  fi
}
remove_auto_update() {
  if [ "$INIT" = systemd ]; then
    systemctl disable --now "$NAME"-update.timer >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/"$NAME"-update.timer /etc/systemd/system/"$NAME"-update.service
  fi
  if [ -f "$CRONTAB" ]; then
    # Match the command field exactly; one instance must not remove another.
    cron_tmp=$(mktemp)
    awk -v updater="$UPDATER" '$6 != updater' "$CRONTAB" > "$cron_tmp"
    cat "$cron_tmp" > "$CRONTAB"
    rm -f "$cron_tmp"
    reload_cron
  fi
}

if [ "$UNINSTALL" = 1 ]; then
  stop_service
  remove_auto_update
  rm -f "$BIN" "$UPDATER" "$INSTALLER" "$ENVFILE" /etc/systemd/system/"$NAME".service /etc/init.d/"$NAME"
  if [ "$INIT" = systemd ]; then systemctl daemon-reload || true; fi
  say "已卸载 $NAME。面板里的测速点记录可在 CDN 配置档的「测速点」卡片里移除。"
  exit 0
fi

# The daily check reuses the mirror and switch saved at install time.
saved() { [ -f "$ENVFILE" ] && sed -n "s/^$1='\(.*\)'\$/\1/p" "$ENVFILE" | head -n 1; }
[ -n "$MIRROR" ] || MIRROR=$(saved PROBE_MIRROR || true)
# A pinned --version stays pinned unless --auto-update on is given.
if [ -n "$VERSION" ] && [ "$UPDATE" = 0 ] && [ -z "$AUTO_UPDATE" ]; then AUTO_UPDATE=off; fi
[ -n "$AUTO_UPDATE" ] || AUTO_UPDATE=$(saved PROBE_AUTO_UPDATE || true)
[ -n "$AUTO_UPDATE" ] || AUTO_UPDATE=on
case $AUTO_UPDATE in on|off) ;; *) die "--auto-update 只能是 on 或 off" ;; esac

# --- platform --------------------------------------------------------------
endian() { # 1 = little, 2 = big (ELF EI_DATA of /bin/sh)
  dd if=/bin/sh bs=1 skip=5 count=1 2>/dev/null | od -An -tu1 | tr -d ' \n'
}
case $(uname -m) in
  x86_64|amd64) ARCH=amd64 ;;
  i386|i486|i586|i686) ARCH=386 ;;
  aarch64|arm64|armv8*) ARCH=arm64 ;;
  armv7*) ARCH=armv7 ;;
  armv5*|armv6*) ARCH=armv5 ;;
  mips64) if [ "$(endian)" = 1 ]; then ARCH=mips64le; else ARCH=mips64; fi ;;
  mips) if [ "$(endian)" = 1 ]; then ARCH=mipsle; else ARCH=mips; fi ;;
  riscv64) ARCH=riscv64 ;;
  loongarch64) ARCH=loong64 ;;
  *) die "不支持的架构 $(uname -m)；可以自己编译：cd examples/cf-ip-probe && go build" ;;
esac

# --- download --------------------------------------------------------------
fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else die "需要 curl 或 wget"; fi
}
fetch_text() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL --max-time 15 "$1"
  else wget -q -T 15 -O - "$1"; fi
}

if [ -n "$VERSION" ]; then
  BASE=https://github.com/$REPO/releases/download/$APP-v$VERSION
else
  TAG=$(fetch_text "https://api.github.com/repos/$REPO/releases?per_page=30" 2>/dev/null |
    sed -n 's/.*"tag_name": *"\('$APP'-v[^"]*\)".*/\1/p' | head -n 1 || true)
  if [ -n "$TAG" ]; then
    BASE=https://github.com/$REPO/releases/download/$TAG
  else
    BASE=https://github.com/$REPO/releases/latest/download
  fi
fi

if [ "$UPDATE" = 1 ]; then
  [ -x "$BIN" ] || die "还没有安装 $NAME，先不带 --update 运行一次"
  [ -n "${TAG:-}" ] || [ -n "$VERSION" ] || { say "没能查到最新版本，下次再试"; exit 0; }
  have=$("$BIN" -version 2>/dev/null | sed -n "s/^$APP //p")
  want=${VERSION:-${TAG#"$APP"-v}}
  if [ "$have" = "$want" ]; then say "已是最新版本 $have"; exit 0; fi
  say "发现新版本：$have → $want"
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
PKG=$APP-linux-$ARCH.tar.gz
say "下载 $BASE/$PKG"
fetch "$MIRROR$BASE/$PKG" "$TMP/$PKG" || die "下载失败；github.com 访问慢时可以加 --mirror <加速前缀>"
if fetch "$MIRROR$BASE/sha256sums.txt" "$TMP/sha256sums.txt" 2>/dev/null && command -v sha256sum >/dev/null 2>&1; then
  want=$(grep " $PKG\$" "$TMP/sha256sums.txt" | cut -d' ' -f1)
  got=$(sha256sum "$TMP/$PKG" | cut -d' ' -f1)
  if [ -z "$want" ] || [ "$want" != "$got" ]; then die "$PKG 校验失败（sha256 不一致），已停止安装"; fi
  say "sha256 校验通过"
else
  say "提示：没能校验 sha256（缺少 sha256sums.txt 或 sha256sum 命令）"
fi
tar -xzf "$TMP/$PKG" -C "$TMP" $APP || die "解压失败"
"$TMP/$APP" -version >/dev/null 2>&1 || die "下载的程序在这台机器上不能运行（架构 $ARCH）"
# Keep this release's installer (sha256 checked) for the daily update check.
HAVE_INSTALLER=0
if [ "$AUTO_UPDATE" = on ] && [ -f "$TMP/sha256sums.txt" ] && command -v sha256sum >/dev/null 2>&1 &&
  fetch "$MIRROR$BASE/install.sh" "$TMP/install.sh" 2>/dev/null; then
  want=$(grep " install.sh\$" "$TMP/sha256sums.txt" | cut -d' ' -f1)
  if [ -n "$want" ] && [ "$want" = "$(sha256sum "$TMP/install.sh" | cut -d' ' -f1)" ]; then HAVE_INSTALLER=1; fi
fi

# --- settings --------------------------------------------------------------
# Keep earlier answers on re-install.
if [ -f "$ENVFILE" ]; then
  old() { sed -n "s/^$1='\(.*\)'\$/\1/p" "$ENVFILE" | head -n 1; }
  [ -n "$API" ] || API=$(old POLARIS_API)
  [ -n "$TOKEN" ] || TOKEN=$(old POLARIS_TOKEN)
  [ -n "$PROFILE" ] || PROFILE=$(old POLARIS_CDN_PROFILE)
  [ -n "$SOURCE" ] || SOURCE=$(old PROBE_SOURCE)
  [ -n "$HOST" ] || HOST=$(old PROBE_HOST)
  [ -n "$CARRIER" ] || CARRIER=$(old PROBE_CARRIER)
  [ -n "$REGION" ] || REGION=$(old PROBE_REGION)
  [ -n "$ARGS" ] || ARGS=$(old PROBE_ARGS)
fi

TTY=0
if [ "$UPDATE" = 0 ] && [ -r /dev/tty ] && (exec </dev/tty) 2>/dev/null; then TTY=1; fi
ask() { # var prompt [default]
  eval "cur=\${$1}"
  [ -n "$cur" ] && return 0
  [ "$TTY" = 1 ] || die "缺少 --$(echo "$1" | tr '[:upper:]' '[:lower:]')（没有终端可以提问）"
  printf '%s' "$2" >/dev/tty
  [ -n "${3:-}" ] && printf ' [%s]' "$3" >/dev/tty
  printf '：' >/dev/tty
  read -r ans </dev/tty || ans=''
  [ -n "$ans" ] || ans=${3:-}
  [ -n "$ans" ] || die "$2 不能为空"
  eval "$1=\$ans"
}
ask API "面板 API 地址（例如 https://panel.example.com/后台路径/api/v1）"
ask TOKEN "API 令牌（只需「上报 CDN 优选 IP」权限）"
ask PROFILE "CDN 配置档 ID"
ask SOURCE "测速点名称（例如 上海电信）"
ask HOST "用来验证 IP 的域名（填你的 CDN 回源域名）" www.cloudflare.com

case $CARRIER in ''|ct|cu|cm) ;; *) die "--carrier 只能是 ct（电信）、cu（联通）、cm（移动）" ;; esac
for v in "$API" "$TOKEN" "$PROFILE" "$SOURCE" "$HOST" "$ARGS" "$MIRROR" "$REGION"; do
  case $v in *"'"*|*'
'*) die "参数里不能有单引号或换行：$v" ;; esac
done
case $API in http://*|https://*) ;; *) die "--api 要以 http:// 或 https:// 开头" ;; esac

# --- install ---------------------------------------------------------------
stop_service
mkdir -p "$DIR"
install -m 0755 "$TMP/$APP" "$BIN" 2>/dev/null || { cp "$TMP/$APP" "$BIN" && chmod 0755 "$BIN"; }
umask 077
cat > "$ENVFILE" <<EOF
# cf-ip-probe settings; edit and restart the service to apply.
POLARIS_API='$API'
POLARIS_TOKEN='$TOKEN'
POLARIS_CDN_PROFILE='$PROFILE'
PROBE_SOURCE='$SOURCE'
PROBE_HOST='$HOST'
PROBE_CARRIER='$CARRIER'
PROBE_REGION='$REGION'
PROBE_ARGS='$ARGS'
PROBE_MIRROR='$MIRROR'
PROBE_AUTO_UPDATE='$AUTO_UPDATE'
EOF
chmod 600 "$ENVFILE"
umask 022
say "已安装 $("$BIN" -version) 到 $BIN，配置在 $ENVFILE"

# --- daily update check --------------------------------------------------
remove_auto_update
UPD=''
if [ "$AUTO_UPDATE" = on ] && [ "$HAVE_INSTALLER" = 1 ] && [ "$INIT" != none ]; then
  install -m 0755 "$TMP/install.sh" "$INSTALLER" 2>/dev/null || { cp "$TMP/install.sh" "$INSTALLER" && chmod 0755 "$INSTALLER"; }
  cat > "$UPDATER" <<EOF
#!/bin/sh
# Installs a newer cf-ip-probe release when there is one; settings are kept.
exec sh $INSTALLER --update --dir $DIR${INSTANCE:+ --instance $INSTANCE}
EOF
  chmod 0755 "$UPDATER"
  if [ "$INIT" = systemd ]; then
    cat > /etc/systemd/system/"$NAME"-update.service <<EOF
[Unit]
Description=Update cf-ip-probe to the latest release
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$UPDATER
EOF
    cat > /etc/systemd/system/"$NAME"-update.timer <<EOF
[Unit]
Description=Daily cf-ip-probe update check

[Timer]
OnCalendar=daily
RandomizedDelaySec=6h
Persistent=true

[Install]
WantedBy=timers.target
EOF
    systemctl daemon-reload
    systemctl enable --now "$NAME"-update.timer >/dev/null 2>&1 || true
    UPD="systemctl list-timers $NAME-update.timer"
  else
    # A random time of day, so many probes do not all ask GitHub at once.
    r=$(od -An -N2 -tu2 /dev/urandom | tr -d ' ')
    mkdir -p "$(dirname "$CRONTAB")"
    printf '%s %s * * * %s >/dev/null 2>&1\n' $((r % 60)) $((r / 60 % 24)) "$UPDATER" >> "$CRONTAB"
    reload_cron
    UPD="grep $NAME-update $CRONTAB"
  fi
else
  rm -f "$UPDATER" "$INSTALLER"
  if [ "$AUTO_UPDATE" = on ]; then say "提示：没能取得并校验这个版本的安装脚本，这次没有开启自动更新"; fi
fi

if [ "$TEST" = 1 ]; then
  say "试跑一轮（不上报），约 1–3 分钟…"
  # shellcheck disable=SC1090,SC2086
  (set -a; . "$ENVFILE"; set +a; "$BIN" -once -dry-run $PROBE_ARGS) || say "试跑失败，请检查上面的输出"
fi

case $INIT in
  systemd)
    cat > /etc/systemd/system/"$NAME".service <<EOF
[Unit]
Description=cf-ip-probe (Polaris CDN speed-test point)
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=$ENVFILE
ExecStart=$BIN \$PROBE_ARGS
Restart=always
RestartSec=30
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    LOGS="journalctl -u $NAME -f"
    if [ "$START" = 1 ]; then systemctl enable --now "$NAME" >/dev/null 2>&1 && systemctl restart "$NAME"; fi
    ;;
  procd)
    cat > /etc/init.d/"$NAME" <<EOF
#!/bin/sh /etc/rc.common
START=99
USE_PROCD=1

start_service() {
	. $ENVFILE
	procd_open_instance
	procd_set_param command $BIN \$PROBE_ARGS
	procd_set_param env POLARIS_API="\$POLARIS_API" POLARIS_TOKEN="\$POLARIS_TOKEN" POLARIS_CDN_PROFILE="\$POLARIS_CDN_PROFILE" PROBE_SOURCE="\$PROBE_SOURCE" PROBE_HOST="\$PROBE_HOST" PROBE_CARRIER="\$PROBE_CARRIER" PROBE_REGION="\$PROBE_REGION"
	procd_set_param respawn 3600 30 0
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
EOF
    chmod 755 /etc/init.d/"$NAME"
    LOGS="logread -f -e $NAME"
    if [ "$START" = 1 ]; then /etc/init.d/"$NAME" enable && /etc/init.d/"$NAME" restart; fi
    ;;
  openrc)
    cat > /etc/init.d/"$NAME" <<EOF
#!/sbin/openrc-run
name=$NAME
description="Polaris CDN speed-test point"
command=$BIN
command_background=true
pidfile=/run/$NAME.pid
output_log=/var/log/$NAME.log
error_log=/var/log/$NAME.log

depend() { need net; }

start_pre() {
	set -a
	. $ENVFILE
	set +a
	command_args="\$PROBE_ARGS"
}
EOF
    chmod 755 /etc/init.d/"$NAME"
    LOGS="tail -f /var/log/$NAME.log"
    if [ "$START" = 1 ]; then rc-update add "$NAME" default >/dev/null && rc-service "$NAME" restart; fi
    ;;
  none)
    say "没有识别到 systemd / procd / OpenRC，请自己让它常驻，例如："
    say "  (set -a; . $ENVFILE; set +a; nohup $BIN \$PROBE_ARGS >/var/log/$NAME.log 2>&1 &)"
    exit 0
    ;;
esac

if [ "$START" = 1 ]; then
  say "已启动。第一轮大约 1–3 分钟后上报，结果在面板 CDN 配置档的「测速点」卡片里。"
else
  say "已安装，未启动。"
fi
say "查看日志：$LOGS"
say "修改设置：编辑 $ENVFILE 后重启服务；或者带上新参数重新运行本脚本。"
if [ -n "$UPD" ]; then say "自动更新：每天检查一次新版本（查看：$UPD；关闭：重新运行并加 --auto-update off）"; fi
