#!/bin/sh
# =============================================================================
#  Porch 门廊 · 一键在线安装脚本
#
#  支持: Linux（含 OpenWrt / 各发行版）/ macOS / FreeBSD
#        CPU: x86_64 i386 arm64 armv7 armv6 armv5 mips mipsle riscv64
#  用法: sh install.sh                 安装最新版
#        sh install.sh -v 1.1.0        安装指定版本
#        sh install.sh --check         只做环境检测，不安装
#        sh install.sh --uninstall     卸载
#        sh install.sh -h              完整参数
#
#  设计原则:
#    · 单个文件、无外部依赖（只用 POSIX sh + 系统自带 curl/wget/tar）
#    · 任何一步失败都在报错里给"下一步怎么做"，并把完整日志留在磁盘
#    · 装完必须能跑：用 `porch -v` 实跑一次才认为安装成功（顺带识别 mips 浮点差异）
# =============================================================================

set -u

# ------------------------------- 常量 ---------------------------------------
REPO="${PORCH_REPO:-likz0421/porch}"
PROJECT="Porch"
BIN_NAME="porch"
DEFAULT_PORT="16601"

# 安装位置（Linux/FreeBSD 用 /usr/local，OpenWrt 用 /usr，符合各自习惯）
INSTALL_DIR="${PORCH_DIR:-}"
CONF_DIR="${PORCH_CONF_DIR:-}"
PORT="${PORCH_PORT:-}"
VERSION_ARG="${PORCH_VERSION:-}"
MIRROR_ARG="${PORCH_MIRROR:-}"
NO_SERVICE=0
ASSUME_YES=0
FORCE=0
DRY_RUN=0
CHECK_ONLY=0
UNINSTALL=0
PURGE=0
INSECURE=0
USER_INSTALL=0
ASSET=""
PKG_PATH=""          # --file：从本地已有的 tar.gz 安装（离线/内网）
DEBUG=0

# ------------------------------- 输出 ---------------------------------------
if [ -t 1 ] && [ "${TERM:-dumb}" != "dumb" ]; then
	C_RESET=$(printf '\033[0m'); C_DIM=$(printf '\033[2m')
	C_RED=$(printf '\033[31m'); C_GRN=$(printf '\033[32m')
	C_YEL=$(printf '\033[33m'); C_BLU=$(printf '\033[36m'); C_BLD=$(printf '\033[1m')
else
	C_RESET=''; C_DIM=''; C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''
fi

LOG_FILE="${PORCH_INSTALL_LOG:-}"
if [ -z "$LOG_FILE" ]; then
	if [ -w /var/log ] 2>/dev/null; then
		LOG_FILE="/var/log/porch-install.log"
	else
		LOG_FILE="${TMPDIR:-/tmp}/porch-install.log"
	fi
fi

_ts() { date '+%Y-%m-%d %H:%M:%S'; }

_log() { # level text
	printf '[%s] %-5s %s\n' "$(_ts)" "$1" "$2" >>"$LOG_FILE" 2>/dev/null || true
}

info() { _log INFO "$1"; printf '  %s\n' "$1"; }
step() { _log INFO "== $1"; printf '\n%s==>%s %s\n' "$C_BLU$C_BLD" "$C_RESET" "$1"; }
ok()   { _log OK "$1"; printf '  %s✓%s %s\n' "$C_GRN" "$C_RESET" "$1"; }
warn() { _log WARN "$1"; printf '  %s!%s %s\n' "$C_YEL" "$C_RESET" "$1" >&2; }
dbg()  { [ "$DEBUG" = 1 ] && { _log DBG "$1"; printf '  %s·%s %s\n' "$C_DIM" "$C_RESET" "$1"; }; return 0; }

die() { # text [hint]
	_log ERR "$1"
	printf '\n%s✗ 安装中止%s\n  %s\n' "$C_RED$C_BLD" "$C_RESET" "$1" >&2
	[ $# -ge 2 ] && printf '  %s建议:%s %s\n' "$C_YEL" "$C_RESET" "$2" >&2
	printf '  %s完整日志: %s%s\n' "$C_DIM" "$LOG_FILE" "$C_RESET" >&2
	exit 1
}

# ------------------------------- 参数 ---------------------------------------
usage() {
	cat <<'USAGE'
Porch 门廊 · 一键在线安装

用法: sh install.sh [选项]

  -v, --version VER    安装指定版本（如 1.1.0 或 v1.1.0；默认最新）
  -d, --dir DIR        程序安装目录（默认 /usr/local/bin）
  -c, --conf DIR       配置目录（默认 /etc/porch/porchconf）
  -p, --port PORT      管理端口（默认沿用配置里的 16601）
      --file PATH      从本地 tar.gz 安装（离线 / 内网 / 已下载好的包）
      --mirror LIST    下载镜像，逗号分隔；空串表示只走直连
                       例: --mirror ",https://ghfast.top/"  (逗号前为空=先试直连)
      --user           安装到当前用户目录（~/.local/bin、~/.config/porch），不装服务
      --no-service     只放二进制，不创建开机自启/服务
      --insecure       跳过 TLS 证书校验（旧设备 CA 过期时用，不安全）
      --check          只输出环境检测结果，不做任何修改
      --dry-run        打印将执行的动作，不落盘
      --uninstall      卸载（默认保留配置）
      --purge          卸载时连配置一起删
  -f, --force          已安装同版本时也重装
  -y, --yes            全部确认（非交互）
      --debug          输出调试信息（等同 set -x）
  -h, --help           显示本帮助

环境变量: PORCH_VERSION PORCH_DIR PORCH_CONF_DIR PORCH_PORT PORCH_MIRROR
          PORCH_REPO（仓库，默认 likz0421/porch）PORCH_INSTALL_LOG

示例:
  curl -fsSL https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
  sh install.sh -v 1.1.0 --mirror ",https://ghfast.top/"
  sh install.sh --check
  sh install.sh --file ./porch_1.1.0_linux_x86_64.tar.gz
  sh install.sh --uninstall --purge
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
		-v|--version) VERSION_ARG="${2:-}"; [ -z "$VERSION_ARG" ] && die "-v 需要一个版本号"; shift 2 ;;
		-d|--dir)     INSTALL_DIR="${2:-}"; [ -z "$INSTALL_DIR" ] && die "-d 需要一个目录"; shift 2 ;;
		-c|--conf)    CONF_DIR="${2:-}"; [ -z "$CONF_DIR" ] && die "-c 需要一个目录"; shift 2 ;;
		-p|--port)    PORT="${2:-}"; [ -z "$PORT" ] && die "-p 需要端口号"; shift 2 ;;
		--file)       PKG_PATH="${2:-}"; [ -z "$PKG_PATH" ] && die "--file 需要一个文件路径"; shift 2 ;;
		--file=*)     PKG_PATH="${1#*=}"; shift ;;
		--mirror)     MIRROR_ARG="${2:-}"; shift 2 ;;
		--mirror=*)   MIRROR_ARG="${1#*=}"; shift ;;
		--user)       USER_INSTALL=1; shift ;;
		--no-service) NO_SERVICE=1; shift ;;
		--insecure)   INSECURE=1; shift ;;
		--check)      CHECK_ONLY=1; shift ;;
		--dry-run)    DRY_RUN=1; shift ;;
		--uninstall)  UNINSTALL=1; shift ;;
		--purge)      PURGE=1; shift ;;
		-f|--force)   FORCE=1; shift ;;
		-y|--yes)     ASSUME_YES=1; shift ;;
		--debug)      DEBUG=1; set -x; shift ;;
		-h|--help)    usage; exit 0 ;;
		*)            die "未知参数: $1" "运行 sh install.sh -h 查看全部参数" ;;
	esac
done

[ "$DEBUG" = 1 ] && set -x

# ------------------------------- 环境 ---------------------------------------
IS_ROOT=0
[ "$(id -u 2>/dev/null || echo 1)" = "0" ] && IS_ROOT=1

OS=""; ARCH=""; DISTRO=""; DISTRO_VER=""; INIT_SYS=""; PKG_MGR=""

detect_os_arch() {
	_raw_os=$(uname -s 2>/dev/null || echo unknown)
	_raw_arch=$(uname -m 2>/dev/null || echo unknown)

	case "$_raw_os" in
		Linux)   OS=linux ;;
		Darwin)  OS=darwin ;;
		FreeBSD) OS=freebsd ;;
		MINGW*|MSYS*|CYGWIN*|Windows_NT)
			die "当前是 Windows 环境（${_raw_os}）" \
			    "Windows 请用 PowerShell 安装: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" ;;
		*) die "不支持的操作系统: $_raw_os" "目前支持 Linux / macOS / FreeBSD；如需其他平台请提交 issue" ;;
	esac

	case "$_raw_arch" in
		x86_64|amd64)          ARCH=x86_64 ;;
		i386|i486|i586|i686)   ARCH=i386 ;;
		aarch64|arm64)         ARCH=arm64 ;;
		armv8l|armv8)          ARCH=armv7 ;;   # 64 位内核跑 32 位用户态
		armv7l|armv7*)         ARCH=armv7 ;;
		armv6l|armv6*)         ARCH=armv6 ;;
		armv5l|armv5tel|armv5*) ARCH=armv5 ;;
		mips64le)              ARCH=mips64le_softfloat ;;
		mipsel|mipsle)         ARCH=mipsle_hardfloat ;;
		mips)                  ARCH=mips_hardfloat ;;
		riscv64)               ARCH=riscv64 ;;
		*) die "不支持的 CPU 架构: $_raw_arch" "请把 uname -a 的输出提到 issue 里" ;;
	esac

	# 组合校验：不是每个 OS 都有全部架构
	case "$OS" in
		darwin)
			case "$ARCH" in arm64|x86_64) ;; *) die "macOS 不支持 $ARCH 架构" ;; esac ;;
		freebsd)
			case "$ARCH" in x86_64|i386|arm64) ;; *) die "FreeBSD 不支持 $ARCH 架构" ;; esac ;;
	esac
}

detect_distro() {
	[ "$OS" != linux ] && return 0
	if [ -f /etc/openwrt_release ]; then
		DISTRO=openwrt
		DISTRO_VER=$(. /etc/openwrt_release 2>/dev/null; echo "${DISTRIB_RELEASE:-}")
	elif [ -f /etc/os-release ]; then
		DISTRO=$(. /etc/os-release 2>/dev/null; echo "${ID:-unknown}")
		DISTRO_VER=$(. /etc/os-release 2>/dev/null; echo "${VERSION_ID:-}")
	fi
}

detect_init() {
	INIT_SYS=none
	if [ "$OS" = linux ]; then
		if [ "$DISTRO" = openwrt ] || [ -x /sbin/procd ]; then
			INIT_SYS=procd
		elif [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
			INIT_SYS=systemd
		elif command -v rc-service >/dev/null 2>&1; then
			INIT_SYS=openrc
		elif [ -d /etc/init.d ]; then
			INIT_SYS=sysvinit
		fi
	elif [ "$OS" = freebsd ]; then
		INIT_SYS=rcng
	elif [ "$OS" = darwin ]; then
		INIT_SYS=launchd
	fi
	# OpenWrt 上没有 systemd，强制 procd
	[ "$DISTRO" = openwrt ] && INIT_SYS=procd
}

detect_pkgmgr() {
	PKG_MGR=""
	for c in apk opkg apt-get dnf yum zypper pacman pkg; do
		if command -v "$c" >/dev/null 2>&1; then PKG_MGR=$c; break; fi
	done
}

have() { command -v "$1" >/dev/null 2>&1; }

DL_TOOL=""; DL_KIND=""
detect_downloader() {
	if have curl; then
		DL_TOOL=curl; DL_KIND=curl
	elif have wget; then
		DL_TOOL=wget; DL_KIND=wget
	elif have uclient-fetch; then
		DL_TOOL=uclient-fetch; DL_KIND=wget
	else
		DL_KIND=""
	fi
}

SHA_TOOL=""
detect_sha() {
	if have sha256sum; then SHA_TOOL="sha256sum"
	elif have shasum; then SHA_TOOL="shasum -a 256"
	elif have openssl; then SHA_TOOL="openssl dgst -sha256 -r"
	elif have busybox && busybox sha256sum </dev/null >/dev/null 2>&1; then SHA_TOOL="busybox sha256sum"
	else SHA_TOOL=""
	fi
}

# ------------------------------- 下载 ---------------------------------------
# 镜像前缀：直接把 GitHub 的完整 URL 接在前缀后面。
# 顺序 = 用户指定的顺序；未指定时先直连，再依次尝试公共加速。
MIRRORS=""
build_mirrors() {
	if [ -n "$MIRROR_ARG" ]; then
		MIRRORS="$MIRROR_ARG"
	elif [ -n "${PORCH_BASE_URL:-}" ]; then
		MIRRORS=""
	else
		MIRRORS=",https://ghfast.top/,https://gh-proxy.com/,https://ghproxy.net/"
	fi
	dbg "镜像列表: [$MIRRORS]"
}

_fetch_once() { # url dest
	case "$DL_KIND" in
		curl)
			if [ "$INSECURE" = 1 ]; then
				curl -fsSL -k -m 300 --connect-timeout 15 -o "$2" "$1"
			else
				curl -fsSL -m 300 --connect-timeout 15 -o "$2" "$1"
			fi ;;
		wget)
			if [ "$DL_TOOL" = uclient-fetch ]; then
				uclient-fetch -q -T 60 -O "$2" "$1" 2>/dev/null
			elif [ "$INSECURE" = 1 ]; then
				wget --no-check-certificate -q -T 60 -O "$2" "$1"
			else
				wget -q -T 60 -O "$2" "$1"
			fi ;;
		*) return 1 ;;
	esac
}

# 取 URL 的 host。故意不用 sed：BSD sed 不认 GNU 的 \?，
# 会把 https://x/ 硬切成 "https:"，macOS 上就是这么翻车的。
_short_host() {
	_h=$1
	case "$_h" in
		*://*) _h=${_h#*://} ;;
	esac
	printf '%s' "${_h%%/*}"
}

# 人类可读的镜像顺序，例如: (直连) → ghfast.top → gh-proxy.com
_mirror_display() {
	if [ -n "${PORCH_BASE_URL:-}" ]; then
		_out=""
		_ifs=$IFS; IFS=,
		for _p in $PORCH_BASE_URL; do
			IFS=$_ifs
			_n=$(_short_host "$_p"); [ -z "$_n" ] && _n=$_p
			if [ -z "$_out" ]; then _out=$_n; else _out="$_out → $_n"; fi
			IFS=,
		done
		IFS=$_ifs
		printf '%s' "$_out"
		return 0
	fi
	if [ -z "$MIRRORS" ]; then printf '(直连)'; return 0; fi
	_out=""
	_ifs=$IFS; IFS=,
	for _p in $MIRRORS; do
		IFS=$_ifs
		if [ -z "$_p" ]; then _n="(直连)"; else _n=$(_short_host "$_p"); fi
		if [ -z "$_out" ]; then _out=$_n; else _out="$_out → $_n"; fi
		IFS=,
	done
	IFS=$_ifs
	printf '%s' "$_out"
}

# download_asset <asset> <dest>
# 每个源最多试 3 次（1s/2s/4s 退避），全失败就换下一个源。
# 候选源 = 镜像前缀 × GitHub 基础 URL；PORCH_BASE_URL 可用逗号分隔给多个自建源。
# MIRRORS 为空表示"只走直连"——用 set -- 展开，避免空列表导致循环体一次都不执行。
download_asset() {
	asset=$1; dest=$2
	[ -n "$DL_KIND" ] || die "没有可用的下载工具（curl / wget / uclient-fetch 都没有）" \
	                        "OpenWrt 可试: opkg update && opkg install uclient-fetch ca-bundle"
	[ -n "$BASE_URL" ] || die "内部错误: BASE_URL 未设置"

	# 组装候选 base（元素形如 https://github.com/u/r/releases/download 或自建源目录）
	if [ -n "${PORCH_BASE_URL:-}" ]; then
		_bases=$PORCH_BASE_URL
	else
		if [ -z "$MIRRORS" ]; then
			set -- ""
		else
			_ifs=$IFS; IFS=,
			# shellcheck disable=SC2086
			set -- $MIRRORS
			IFS=$_ifs
		fi
		_bases=""
		for _prefix in "$@"; do
			# 镜像站在前缀后面看到的必须是完整的原始 URL
			_bases="${_bases:+$_bases,}${_prefix}${BASE_URL}"
		done
	fi

	_ifs=$IFS; IFS=,
	# shellcheck disable=SC2086
	set -- $_bases
	IFS=$_ifs

	for _base in "$@"; do
		url="${_base%/}/$TAG/$asset"
		host=$(_short_host "$url"); [ -z "$host" ] && host=$_base
		attempt=1
		while [ "$attempt" -le 3 ]; do
			if [ "$DRY_RUN" = 1 ]; then
				info "[dry-run] 下载 $url"
				return 0
			fi
			info "下载 $asset  ← ${host}（第 $attempt 次）"
			if _fetch_once "$url" "$dest.part"; then
				mv -f "$dest.part" "$dest" 2>/dev/null || { rm -f "$dest.part"; continue; }
				ok "下载完成 $(du -h "$dest" 2>/dev/null | cut -f1)"
				return 0
			fi
			rm -f "$dest.part"
			warn "$host 第 $attempt 次失败"
			case $attempt in 1) sleep 1 ;; 2) sleep 2 ;; *) sleep 4 ;; esac
			attempt=$((attempt + 1))
		done
		warn "镜像 $host 不可用，换下一个"
	done
	return 1
}

# 取最新 tag：先 API（最准），再跟随 releases/latest 的跳转（不需要 API 配额）
latest_tag() {
	_api="https://api.github.com/repos/$REPO/releases/latest"
	if have curl; then
		t=$(curl -fsSL -m 15 "$_api" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
		[ -n "$t" ] && { printf '%s' "$t"; return 0; }
	fi
	if [ -n "$DL_KIND" ]; then
		for prefix in "" https://ghfast.top/ https://gh-proxy.com/; do
			t=$(_fetch_once "${prefix}https://api.github.com/repos/$REPO/releases/latest" - 2>/dev/null)
			t=$(printf '%s' "$t" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
			[ -n "$t" ] && { printf '%s' "$t"; return 0; }
		done
	fi
	# 最后兜底：解析 releases/latest 的 302 Location
	if have curl; then
		loc=$(curl -fsSI -m 15 "https://github.com/$REPO/releases/latest" 2>/dev/null | tr -d '\r' | sed -n 's/^[Ll]ocation:[[:space:]]*//p' | head -1)
		case "$loc" in */tag/*) printf '%s' "${loc##*/}"; return 0 ;; esac
	fi
	return 1
}

# ------------------------------- 校验 ---------------------------------------
# verify_asset <file> <asset>  用 release 里的 SHA256SUMS 校验
verify_asset() {
	file=$1; asset=$2
	if [ -z "$SHA_TOOL" ]; then
		warn "系统没有 sha256 工具，跳过校验（建议安装 coreutils）"
		return 0
	fi
	[ -f "$SUMS_FILE" ] || { warn "没下到 SHA256SUMS，跳过校验"; return 0; }
	# 两种格式都认：`<hash>  name`（文本）与 `<hash> *name`（二进制）
	want=$(awk -v a="$asset" '{n=$0; sub(/^[0-9a-f]+[ \t]+[*]?/,"",n); if (n==a) {print $1; exit}}' "$SUMS_FILE" 2>/dev/null)
	if [ -z "$want" ]; then
		warn "SHA256SUMS 里没有 $asset 的记录，跳过校验"
		return 0
	fi
	got=$($SHA_TOOL "$file" 2>/dev/null | sed -n 's/^\([0-9a-f]\{64\}\).*/\1/p' | head -1)
	if [ "$want" != "$got" ]; then
		die "校验和不匹配: $asset" "可能下载被中间人篡改或文件不完整；重试一次仍有问题请提 issue"
	fi
	ok "校验和一致 ${want%"${want#????????}"}…"
}

# 真正能跑才算装成功：这一步同时能识别"架构选错 / mips 浮点不匹配"
verify_binary() {
	_bi=$1
	[ -x "$_bi" ] || { warn "二进制没有可执行权限"; return 1; }
	_out=$("$_bi" -v 2>&1) || return 1
	case "$_out" in
		*"$BIN_NAME"*) dbg "自检输出: $_out"; return 0 ;;
		*) warn "自检输出异常: $_out"; return 1 ;;
	esac
}

# ------------------------------- 服务 ---------------------------------------
write_systemd() {
	_unit=/etc/systemd/system/porch.service
	_exec="$INSTALL_DIR/$BIN_NAME -cd $CONF_DIR"
	[ -n "$PORT" ] && _exec="$_exec -p $PORT"
	[ "$DRY_RUN" = 1 ] && { info "[dry-run] 写入 $_unit"; return 0; }
	cat >"$_unit" <<EOF
[Unit]
Description=Porch - DDNS / HTTPS certificate / reverse proxy
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$_exec
Restart=always
RestartSec=3
LimitNOFILE=65535
# 监听 80（ACME HTTP-01）需要的唯一特权
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=$CONF_DIR

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload >/dev/null 2>&1 || true
	systemctl enable porch.service >/dev/null 2>&1 || warn "systemctl enable 失败，可手动执行: systemctl enable --now porch"
	systemctl restart porch.service >/dev/null 2>&1 || systemctl start porch.service >/dev/null 2>&1 || \
		warn "服务启动失败，执行 journalctl -u porch -n 50 查看原因"
	ok "systemd 服务已启用: porch.service"
}

write_procd() {
	_initd=/etc/init.d/porch
	_uci=/etc/config/porch
	[ "$DRY_RUN" = 1 ] && { info "[dry-run] 写入 $_initd 与 $_uci"; return 0; }
	if [ ! -f "$_uci" ]; then
		cat >"$_uci" <<EOF
config porch 'main'
	option enabled '1'
	option config_dir '$CONF_DIR'
	option port '$PORT'
	option allow_internet '0'
EOF
	fi
	cat >"$_initd" <<'EOF'
#!/bin/sh /etc/rc.common
# Porch 服务启动脚本（procd）
START=99
STOP=15
USE_PROCD=1

PROG=/usr/bin/porch

get_option() {
	value=$(uci -q get porch.@porch[0].$1)
	[ -z "$value" ] && value="$2"
	echo "$value"
}

start_service() {
	[ "$(get_option enabled 1)" = "0" ] && { echo "porch 在 UCI 中被禁用" >&2; return 0; }
	conf_dir=$(get_option config_dir /etc/porch/porchconf)
	port=$(get_option port '')
	mkdir -p "$conf_dir"

	procd_open_instance
	procd_set_param command "$PROG" -cd "$conf_dir"
	[ -n "$port" ] && procd_append_param command -p "$port"
	procd_set_param respawn
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_set_param pidfile /var/run/porch.pid
	procd_close_instance
	logger -t porch "已启动，配置目录: $conf_dir"
}

stop_service() { logger -t porch "已停止"; }
reload_service() { stop; start; }
service_triggers() { procd_add_reload_trigger porch; }
EOF
	chmod 755 "$_initd"
	"$_initd" enable >/dev/null 2>&1 || warn "init 脚本 enable 失败"
	"$_initd" restart >/dev/null 2>&1 || warn "服务启动失败，执行 logread | grep porch 查看原因"
	ok "procd 服务已启用: /etc/init.d/porch"
}

write_rcng() {
	_rc=/usr/local/etc/rc.d/porch
	[ "$DRY_RUN" = 1 ] && { info "[dry-run] 写入 $_rc"; return 0; }
	cat >"$_rc" <<EOF
#!/bin/sh
# PROVIDE: porch
# REQUIRE: NETWORKING
# KEYWORD: shutdown
. /etc/rc.subr
name=porch
rcvar=porch_enable
command=$INSTALL_DIR/$BIN_NAME
porch_flags="-cd $CONF_DIR${PORT:+ -p $PORT}"
load_rc_config \$name
: \${porch_enable:="YES"}
run_rc_command "\$1"
EOF
	chmod 755 "$_rc"
	sysrc porch_enable=YES >/dev/null 2>&1 || echo 'porch_enable="YES"' >>/etc/rc.conf
	service porch start >/dev/null 2>&1 || warn "服务启动失败，查看 /var/log/messages"
	ok "rc.d 服务已启用: $_rc"
}

write_launchd() {
	_plist=/Library/LaunchDaemons/cn.porch.daemon.plist
	[ "$DRY_RUN" = 1 ] && { info "[dry-run] 写入 $_plist"; return 0; }
	_args="<string>$INSTALL_DIR/$BIN_NAME</string><string>-cd</string><string>$CONF_DIR</string>"
	[ -n "$PORT" ] && _args="$_args<string>-p</string><string>$PORT</string>"
	cat >"$_plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>cn.porch.daemon</string>
  <key>ProgramArguments</key><array>$_args</array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/porch.log</string>
  <key>StandardErrorPath</key><string>/var/log/porch.err</string>
</dict></plist>
EOF
	chmod 644 "$_plist"
	launchctl unload "$_plist" >/dev/null 2>&1 || true
	launchctl load "$_plist" >/dev/null 2>&1 || warn "launchctl load 失败"
	ok "launchd 服务已加载: cn.porch.daemon"
}

install_service() {
	[ "$NO_SERVICE" = 1 ] && { info "按 --no-service 跳过服务安装"; return 0; }
	if [ "$USER_INSTALL" = 1 ] || [ "$IS_ROOT" = 0 ]; then
		info "非 root 安装，跳过开机自启（需要时手动运行 $INSTALL_DIR/${BIN_NAME}）"
		return 0
	fi
	case "$INIT_SYS" in
		systemd) write_systemd ;;
		procd)   write_procd ;;
		rcng)    write_rcng ;;
		launchd) write_launchd ;;
		*)       warn "未识别的 init 系统，已跳过开机自启；手动启动: $INSTALL_DIR/$BIN_NAME -cd $CONF_DIR" ;;
	esac
}

remove_service() {
	if [ -f /etc/systemd/system/porch.service ]; then
		[ "$DRY_RUN" = 1 ] || systemctl stop porch 2>/dev/null
		[ "$DRY_RUN" = 1 ] || systemctl disable porch 2>/dev/null
		[ "$DRY_RUN" = 1 ] || rm -f /etc/systemd/system/porch.service
		[ "$DRY_RUN" = 1 ] || systemctl daemon-reload 2>/dev/null
		ok "已移除 systemd 服务"
	fi
	if [ -f /etc/init.d/porch ]; then
		[ "$DRY_RUN" = 1 ] || /etc/init.d/porch stop 2>/dev/null
		[ "$DRY_RUN" = 1 ] || /etc/init.d/porch disable 2>/dev/null
		[ "$DRY_RUN" = 1 ] || rm -f /etc/init.d/porch
		ok "已移除 init.d 服务"
	fi
	if [ -f /usr/local/etc/rc.d/porch ]; then
		[ "$DRY_RUN" = 1 ] || service porch stop 2>/dev/null
		[ "$DRY_RUN" = 1 ] || rm -f /usr/local/etc/rc.d/porch
		ok "已移除 rc.d 服务"
	fi
	if [ -f /Library/LaunchDaemons/cn.porch.daemon.plist ]; then
		[ "$DRY_RUN" = 1 ] || launchctl unload /Library/LaunchDaemons/cn.porch.daemon.plist 2>/dev/null
		[ "$DRY_RUN" = 1 ] || rm -f /Library/LaunchDaemons/cn.porch.daemon.plist
		ok "已移除 launchd 服务"
	fi
	if [ "$PURGE" = 1 ]; then
		[ "$DRY_RUN" = 1 ] || rm -f /etc/config/porch
	fi
}

# ------------------------------- 检测 ---------------------------------------
print_env() {
	step "环境检测"
	_arch_disp=$ARCH
	[ "$_raw_arch" != "$ARCH" ] && _arch_disp="$_raw_arch → $ARCH"
	info "系统: ${_raw_os}${DISTRO:+ ($DISTRO $DISTRO_VER)}  架构: ${_arch_disp}"
	info "内核: $(uname -r 2>/dev/null)"
	info "当前用户: $(id -un 2>/dev/null) (uid=$(id -u 2>/dev/null))$([ "$IS_ROOT" = 1 ] && echo ' 可写系统目录' || echo ' 仅用户目录可写')"
	info "init 系统: ${INIT_SYS}$([ -n "$PKG_MGR" ] && echo "  包管理器: $PKG_MGR")"
	info "下载工具: ${DL_TOOL:-未找到}"
	info "校验工具: ${SHA_TOOL:-未找到（将跳过校验）}"
	info "日志文件: $LOG_FILE"
	if [ -n "$INSTALL_DIR" ]; then
		info "程序目录: $INSTALL_DIR"
	else
		info "程序目录: 待定（$([ "$IS_ROOT" = 1 ] && echo /usr/local/bin || echo "$HOME/.local/bin")）"
	fi
}

# ------------------------------- 主流程 -------------------------------------
detect_os_arch
detect_distro
detect_init
detect_pkgmgr
detect_downloader
detect_sha
build_mirrors

# 目录与权限：root 装系统目录，普通用户装 ~/.local
if [ -z "$INSTALL_DIR" ]; then
	if [ "$USER_INSTALL" = 1 ] || [ "$IS_ROOT" = 0 ]; then
		INSTALL_DIR="$HOME/.local/bin"
	else
		INSTALL_DIR=/usr/local/bin
	fi
fi
# OpenWrt 的惯例是 /usr/bin（/usr/local 可能不在 PATH 里）
if [ "$DISTRO" = openwrt ] && [ -z "${PORCH_DIR:-}" ] && [ "$INSTALL_DIR" = /usr/local/bin ]; then
	INSTALL_DIR=/usr/bin
fi
if [ -z "$CONF_DIR" ]; then
	if [ "$USER_INSTALL" = 1 ] || [ "$IS_ROOT" = 0 ]; then
		CONF_DIR="$HOME/.config/porch/porchconf"
	else
		CONF_DIR=/etc/porch/porchconf
	fi
fi
BIN_PATH="$INSTALL_DIR/$BIN_NAME"

if [ "$CHECK_ONLY" = 1 ]; then
	print_env
	step "可用版本与连通性"
	if [ -n "$VERSION_ARG" ] && [ "$VERSION_ARG" != latest ]; then
		info "目标版本: v${VERSION_ARG#v}（已用 -v 指定，不查询最新）"
	elif TAG_TMP=$(latest_tag); then
		ok "最新版本: $TAG_TMP"
	else
		warn "无法获取最新版本（网络受限？可用 -v 指定版本）"
	fi
	if [ -x "$BIN_PATH" ]; then ok "已安装: $("$BIN_PATH" -v 2>&1)"; else info "尚未安装（$BIN_PATH 不存在）"; fi
	printf '\n%s环境检测完成，未做任何修改。%s\n' "$C_DIM" "$C_RESET"
	exit 0
fi

# 已安装检测：读当前版本，判断是「安装 / 升级 / 重装 / 无事可做」
CUR_VER=""; CUR_BIN=""
for cand in "$BIN_PATH" /usr/local/bin/porch /usr/bin/porch; do
	if [ -x "$cand" ]; then
		CUR_BIN=$cand
		CUR_VER=$("$cand" -v 2>/dev/null | sed -n 's/^porch \([^ ]*\).*/\1/p' | head -1)
		break
	fi
done

do_uninstall() {
	step "卸载 $PROJECT"
	remove_service
	if [ -n "$CUR_BIN" ] && [ -f "$CUR_BIN" ]; then
		[ "$DRY_RUN" = 1 ] || rm -f "$CUR_BIN"
		ok "已删除 $CUR_BIN"
	else
		info "未发现已安装的可执行文件"
	fi
	if [ "$PURGE" = 1 ]; then
		# 先把本次实际用的配置目录删掉（用户可能用 -c 指到了别处）
		for d in "$CONF_DIR" /etc/porch "$HOME/.config/porch"; do
			[ -n "$d" ] || continue
			if [ -d "$d" ]; then
				[ "$DRY_RUN" = 1 ] || rm -rf "$d"
				ok "已删除配置目录 $d"
			fi
		done
	else
		info "配置目录保留（如需一并删除: --purge）"
		[ -d "$CONF_DIR" ] && info "  配置位于 $CONF_DIR"
	fi
	printf '\n%s卸载完成。%s\n' "$C_GRN" "$C_RESET"
	exit 0
}

if [ "$UNINSTALL" = 1 ]; then
	do_uninstall
fi

# 版本解析
TAG=""
LOCAL_PKG=0
if [ -n "$PKG_PATH" ]; then
	# --file：直接从本地包安装，完全不联网（离线机 / 内网 / 已下好再分发）
	[ -f "$PKG_PATH" ] || die "找不到本地安装包: $PKG_PATH" \
		"确认路径拼写；相对路径以当前目录为准"
	LOCAL_PKG=1
	PKG_PATH=$(cd "$(dirname "$PKG_PATH")" 2>/dev/null && pwd)/$(basename "$PKG_PATH")
	_pkg_name=$(basename "$PKG_PATH")
	_f_ver=$(printf '%s' "$_pkg_name" | sed -n 's/^porch_\([0-9][0-9A-Za-z.]*\)_.*\.tar\.gz$/\1/p')
	if [ -z "$_f_ver" ]; then
		[ -n "$VERSION_ARG" ] || die "无法从文件名识别版本: $_pkg_name" \
			"文件名应形如 porch_1.1.0_linux_x86_64.tar.gz；或用 -v 1.1.0 明确指定版本"
		_f_ver=${VERSION_ARG#v}
	fi
	VER=$_f_ver
	TAG="v$VER"
	# 文件名里的架构优先：离线包可能是事先在别的机器上下好的
	_f_arch=$(printf '%s' "$_pkg_name" | sed -n 's/^porch_[^_]*_[a-z]*_\(.*\)\.tar\.gz$/\1/p')
	if [ -n "$_f_arch" ] && [ "$_f_arch" != "$ARCH" ]; then
		warn "安装包架构是 ${_f_arch}，本机识别为 ${ARCH}"
		info "按安装包的架构继续（--file 以文件名为准）"
		ARCH=$_f_arch
	fi
	ASSET="${BIN_NAME}_${VER}_${OS}_${ARCH}.tar.gz"
	ok "离线安装: ${_pkg_name}（版本 ${VER}，架构 ${ARCH}）"
elif [ -n "$VERSION_ARG" ] && [ "$VERSION_ARG" != "latest" ]; then
	case "$VERSION_ARG" in
		v*) TAG="$VERSION_ARG" ;;
		*)  TAG="v$VERSION_ARG" ;;
	esac
	VER=${TAG#v}
else
	step "获取最新版本"
	if ! TAG=$(latest_tag); then
		# 已经装好了就别因为查不到版本号而中断——用户可能只是确认一下
		if [ -n "$CUR_VER" ]; then
			warn "查不到最新版本（网络受限）；当前已安装 ${CUR_VER}，未做任何修改"
			info "要装指定版本: sh install.sh -v 1.1.0"
			exit 0
		fi
		die "拿不到最新版本号" "网络可能被限制：用 -v 指定版本，或加 --mirror 指定镜像"
	fi
	VER=${TAG#v}
	ok "最新版本: $TAG"
fi

case "$VER" in
	''|*[!0-9.]*) die "版本号格式不对: ${VERSION_ARG:-$VER}" "示例: -v 1.1.0" ;;
esac

[ -n "$ASSET" ] || ASSET="${BIN_NAME}_${VER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download"

print_env
info "目标版本: ${C_BLD}v$VER${C_RESET}  目标包: $ASSET"

# 同版本已安装且没 -f：直接收工（幂等，脚本可以重复跑）
if [ -n "$CUR_VER" ] && [ "$CUR_VER" = "$VER" ] && [ "$FORCE" = 0 ] && [ "$DRY_RUN" = 0 ]; then
	ok "已是 ${VER}，无需重复安装（重装请加 -f）"
	if [ -x "$BIN_PATH" ]; then info "程序: $BIN_PATH"; fi
	info "管理界面: http://<本机IP>:${PORT:-$DEFAULT_PORT}"
	exit 0
fi

[ "$DRY_RUN" = 1 ] && step "演练模式（--dry-run）：只打印动作，不落盘"

# 磁盘空间（.tar.gz 约 3MB，解包后约 10MB，留 30MB 余量）
step "安装前检查"
if [ "$DRY_RUN" = 0 ]; then
	_avail=$(df -k "$(dirname "$INSTALL_DIR")" 2>/dev/null | awk 'NR==2{print $4}')
	if [ -n "${_avail:-}" ] && [ "$_avail" -lt 30720 ] 2>/dev/null; then
		die "磁盘空间不足（可用 $((_avail / 1024)) MB，至少需要 30 MB）" "清理空间或换 --dir 到别的分区"
	fi
	ok "磁盘空间充足"
fi

if [ "$DRY_RUN" = 0 ]; then
	if ! mkdir -p "$INSTALL_DIR" 2>/dev/null; then
		die "无法创建目录 $INSTALL_DIR" "当前用户无权限；用 sudo 运行，或加 --user 装到用户目录"
	fi
	[ -w "$INSTALL_DIR" ] || die "目录不可写: $INSTALL_DIR" "用 sudo 运行，或加 --user 装到用户目录"
	ok "目录可用: $INSTALL_DIR"
fi

# 工作目录放在安装目录下面：OpenWrt 常见 /tmp 是 noexec，装到那里的二进制跑不起来
WORK="$INSTALL_DIR/.porch-install.$$"
if [ "$DRY_RUN" = 0 ]; then
	mkdir -p "$WORK" || die "无法创建临时目录 $WORK"
	trap 'rm -rf "$WORK" 2>/dev/null || true' EXIT
fi

SUMS_FILE="$WORK/SHA256SUMS"
if [ "$LOCAL_PKG" = 1 ]; then
	step "读取本地安装包"
	if [ "$DRY_RUN" = 1 ]; then
		info "[dry-run] 使用本地包 $PKG_PATH"
	else
		cp -f "$PKG_PATH" "$WORK/$ASSET" || die "无法读取本地包: $PKG_PATH" \
			"确认文件权限与磁盘空间"
		ok "已读取 $(basename "$PKG_PATH")  $(du -h "$WORK/$ASSET" 2>/dev/null | cut -f1)"
	fi
	# 同目录下的 SHA256SUMS 顺手校验（发布页下载的一整套通常都在一起）
	if [ -f "$(dirname "$PKG_PATH")/SHA256SUMS" ]; then
		SUMS_FILE="$(dirname "$PKG_PATH")/SHA256SUMS"
		[ "$DRY_RUN" = 1 ] || verify_asset "$WORK/$ASSET" "$ASSET"
	else
		warn "本地包旁边没有 SHA256SUMS，跳过完整性校验"
	fi
else
	step "下载"
	info "镜像顺序: $(_mirror_display)"

	download_asset "SHA256SUMS" "$SUMS_FILE" || warn "SHA256SUMS 下载失败，将跳过校验"
	download_asset "$ASSET" "$WORK/$ASSET" || die "所有镜像都没能下载到 $ASSET" \
		"1) 检查网络/DNS  2) 换镜像: --mirror \",https://ghfast.top/\"  3) 用 -v 指定已发布版本"

	verify_asset "$WORK/$ASSET" "$ASSET"
fi

step "解包与自检"
[ "$DRY_RUN" = 0 ] && { tar xzf "$WORK/$ASSET" -C "$WORK" || die "解包失败：文件可能不完整" "删掉重试，或换镜像"; }
SRC_BIN="$WORK/${ASSET%.tar.gz}/$BIN_NAME"
if [ "$DRY_RUN" = 0 ] && [ ! -f "$SRC_BIN" ]; then
	# 兜底：包内目录层级跟标准不一致时，全树找一个叫 porch 的普通文件
	SRC_BIN=$(find "$WORK" -type f -name "$BIN_NAME" 2>/dev/null | head -1)
fi
[ "$DRY_RUN" = 1 ] || [ -f "$SRC_BIN" ] || die "包里没有 $BIN_NAME" "请提 issue 附上资产名 $ASSET"
[ "$DRY_RUN" = 0 ] && chmod 755 "$SRC_BIN"

if [ "$DRY_RUN" = 0 ]; then
	if ! verify_binary "$SRC_BIN"; then
		# mips 硬浮点选错时会 exec format error / illegal instruction，换软浮点再试
		case "$ARCH" in
			*_hardfloat)
				warn "该二进制在本机跑不起来（多半是浮点 ABI 不同），改用 softfloat 版本"
				ARCH="${ARCH%_hardfloat}_softfloat"
				ASSET="${BIN_NAME}_${VER}_${OS}_${ARCH}.tar.gz"
				download_asset "$ASSET" "$WORK/$ASSET" || die "软浮点版本也下载失败"
				verify_asset "$WORK/$ASSET" "$ASSET"
				tar xzf "$WORK/$ASSET" -C "$WORK"
				SRC_BIN="$WORK/${ASSET%.tar.gz}/$BIN_NAME"
				chmod 755 "$SRC_BIN"
				verify_binary "$SRC_BIN" || die "两种浮点版本都无法运行" \
					"请把 'uname -a' 与 '$SRC_BIN -v' 的输出提到 issue"
				;;
			*) die "下载的程序无法在本机运行（${ARCH}）" \
				"可能架构识别有误；请把 'uname -a' 输出与资产名 $ASSET 提到 issue" ;;
		esac
	fi
	ok "自检通过: $("$SRC_BIN" -v 2>&1)"
fi

step "安装"
if [ "$DRY_RUN" = 1 ]; then
	info "[dry-run] 复制 $ASSET → $BIN_PATH"
else
	# 原子替换：先落到同目录临时名再 mv，避免升级瞬间出现半个文件
	cp -f "$SRC_BIN" "$INSTALL_DIR/.$BIN_NAME.new" || die "写入失败: $INSTALL_DIR"
	chmod 755 "$INSTALL_DIR/.$BIN_NAME.new"
	if [ -n "$CUR_BIN" ] && [ "$CUR_BIN" != "$BIN_PATH" ] && [ -f "$CUR_BIN" ]; then
		rm -f "$CUR_BIN" 2>/dev/null || warn "旧版本 $CUR_BIN 未删除（可能正在运行）"
	fi
	mv -f "$INSTALL_DIR/.$BIN_NAME.new" "$BIN_PATH" || die "无法写入 $BIN_PATH"
	ok "$([ -n "$CUR_VER" ] && echo "已升级: ${CUR_VER} → ${VER}" || echo "已安装: $VER")  →  $BIN_PATH"
fi

# 配置目录
if [ "$DRY_RUN" = 0 ]; then
	mkdir -p "$CONF_DIR" 2>/dev/null || warn "创建配置目录失败: $CONF_DIR"
	[ -d "$CONF_DIR" ] && ok "配置目录: $CONF_DIR"
fi

install_service

# ------------------------------- 收尾 ---------------------------------------
MYIP=""
if have ip; then MYIP=$(ip route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -1); fi
[ -z "$MYIP" ] && MYIP=$(hostname -i 2>/dev/null | awk '{print $1}')
[ -z "$MYIP" ] && MYIP="<本机IP>"

step "完成"
SHOW_PORT=${PORT:-$DEFAULT_PORT}
cat <<EOF

  ${C_GRN}${C_BLD}Porch 已就绪${C_RESET}

  管理界面   http://${MYIP}:${SHOW_PORT}
  默认账号   666 / 666   ${C_YEL}（登录后第一件事：系统设置 → 改掉密码）${C_RESET}
  程序       $BIN_PATH
  配置目录   $CONF_DIR
  安装日志   $LOG_FILE

  常用命令
    $BIN_NAME -v                     查看版本
    $BIN_NAME -cd $CONF_DIR          前台运行（调试用）
    $BIN_NAME -cd $CONF_DIR -p 8080  换个端口跑

EOF

case "$INIT_SYS" in
	systemd) [ "$NO_SERVICE" = 0 ] && printf '    systemctl status porch           查看服务状态\n    journalctl -u porch -f           跟踪日志\n\n' ;;
	procd)   [ "$NO_SERVICE" = 0 ] && printf '    /etc/init.d/porch status         查看服务状态\n    logread -f | grep porch          跟踪日志\n\n' ;;
	rcng)    [ "$NO_SERVICE" = 0 ] && printf '    service porch status             查看服务状态\n\n' ;;
	launchd) [ "$NO_SERVICE" = 0 ] && printf '    launchctl list | grep porch      查看服务状态\n    tail -f /var/log/porch.log       跟踪日志\n\n' ;;
esac

# 用户目录安装时提醒 PATH——否则装完敲 porch 会 command not found
case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		printf '  %s注意%s %s 不在 PATH 里，把它加进去:\n' "$C_YEL" "$C_RESET" "$INSTALL_DIR"
		printf '        echo '\''export PATH="%s:$PATH"'\'' >> ~/.profile && . ~/.profile\n\n' "$INSTALL_DIR"
		;;
esac

cat <<EOF
  升级  重新执行本脚本即可（配置自动保留）
  卸载  sh install.sh --uninstall           （加 --purge 连配置一起删）

  下一步（不用看文档也能走完）
    1. 浏览器打开上面的地址，登录后到「动态域名」加一条任务
    2. 「SSL 证书」里申请一张证书（需要 80 端口能从公网访问）
    3. 「反向代理」把内网服务映射成一个能在外网打开的网址
    概览页顶部有同样的三步向导，点一下就能跳过去。

EOF

_log OK "安装完成: v$VER -> $BIN_PATH"
exit 0
