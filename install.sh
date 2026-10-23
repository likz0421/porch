#!/bin/sh
# =============================================================================
#  Porch 门廊 · 一键在线安装脚本
#
#  支持: Linux（含 OpenWrt / 各发行版）/ macOS / FreeBSD
#        CPU: x86_64 i386 arm64 armv7 armv6 armv5 mips mipsle riscv64
#  用法: sh install.sh                 安装最新版
#        sh install.sh -v 1.1.2        安装指定版本
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
DEFAULT_PORT="5200"

# 安装位置（Linux/FreeBSD 用 /usr/local，OpenWrt 用 /usr，符合各自习惯）
INSTALL_DIR="${PORCH_DIR:-}"
CONF_DIR="${PORCH_CONF_DIR:-}"
PORT="${PORCH_PORT:-}"
PORT_ARG=""          # 只有配置写不进去时才退回用服务参数 -p 兜底
VERSION_ARG="${PORCH_VERSION:-}"
MIRROR_ARG="${PORCH_MIRROR:-}"
PROBE="${PORCH_PROBE:-0}"
BASE_URL_ARG="${PORCH_BASE_URL:-}"
LUCI_MODE="auto"
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

用法: sh install.sh [选项] [下载源根地址]

最常用
  sh install.sh                    安装 / 升级到最新版（已有配置保留）
                                   已装过会先问：升级 / 重装 / 改端口 / 卸载
  sh install.sh -p 8080            安装时指定管理端口（不给就交互问一次）
  sh install.sh --uninstall        卸载程序与服务，配置保留
  sh install.sh --uninstall --purge   卸载并连配置一起删（不可恢复）

选项
  -v, --version VER    安装指定版本（如 1.1.2 或 v1.1.2；默认最新）
  -d, --dir DIR        程序安装目录（默认 /usr/local/bin）
  -c, --conf DIR       配置目录（默认 /etc/porch/porchconf）
  -p, --port PORT      管理端口（默认 5200；安装时不给会交互问一次，回车即默认）
                       已装过的情况下单独给 -p 只改端口，不重新下载二进制
      --file PATH      从本地 tar.gz 安装（离线 / 内网 / 已下载好的包）
      --mirror LIST    下载镜像，逗号分隔；空串表示只走直连
                       例: --mirror ",https://ghfast.top/"  (逗号前为空=先试直连)
      --probe          先给每个镜像测速，按实际耗时排序再下载（大陆网络推荐）
      --luci           在 OpenWrt 上强制安装 LuCI 界面包（默认有 LuCI 就自动装）
      --no-luci        不装 LuCI 界面，只要后端
      --user           安装到当前用户目录（~/.local/bin、~/.config/porch），不装服务
      --no-service     只放二进制，不创建开机自启/服务
      --insecure       跳过 TLS 证书校验（旧设备 CA 过期时用，不安全）
      --check          只输出环境检测结果，不做任何修改
      --dry-run        打印将执行的动作，不落盘
      --uninstall      卸载（服务 + 二进制 + LuCI 界面包，配置保留）
      --purge          卸载时连配置目录一起删
  -f, --force          已安装同版本时也重装
  -y, --yes            全部确认（非交互）
      --debug          输出调试信息（等同 set -x）
  -h, --help           显示本帮助

环境变量: PORCH_VERSION PORCH_DIR PORCH_CONF_DIR PORCH_PORT PORCH_MIRROR
          PORCH_REPO（仓库，默认 likz0421/porch）PORCH_INSTALL_LOG PORCH_PROBE
          PORCH_BASE_URL（自建下载源，等同末尾的位置参数）

OpenWrt 说明: 检测到系统里有 LuCI 时，会顺带把「服务 → Porch 门廊」界面装上；
              纯后端环境（无 LuCI）默认跳过，要强制装加 --luci。
              装完菜单没出现就 Ctrl+F5 强刷浏览器（LuCI 会缓存菜单树）。

示例:
  curl -fsSL https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
  sh install.sh --probe            # 大陆网络：先测速再下载
  sh install.sh -v 1.1.2 --mirror ",https://ghfast.top/"
  sh install.sh http://your-mirror.host   # 自建源（兼容 Lucky 脚本的传参风格）
  sh install.sh --check
  sh install.sh --file ./porch_1.1.2_linux_x86_64.tar.gz
  sh install.sh --uninstall --purge

大陆网络拉不下这个脚本本身时，先把 GitHub 地址套一层加速：
  curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh -s -- --probe
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
		--probe)      PROBE=1; shift ;;
		--luci)       LUCI_MODE=yes; shift ;;
		--no-luci)    LUCI_MODE=no; shift ;;
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
		*://*)
			# 兼容 Lucky 官方脚本的用法：sh install.sh http://your-mirror.host
			# 位置参数直接当下载源根地址
			BASE_URL_ARG=$1; shift ;;
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

# OpenWrt 有两个时代：24.10 及更早用 opkg（.ipk），25.12 起换成 apk（.apk）。
# 本脚本两种都不依赖——直接把静态二进制放进去、自己写 procd 脚本，
# 所以 apk 设备照样能装。这里只是认出来，好在结尾告诉用户"你还有另一条路"。
PKG_HINT=""
detect_openwrt_pkg() {
	[ "$DISTRO" = openwrt ] || return 0
	if have apk; then
		PKG_HINT="apk（OpenWrt 25.12+）"
	elif have opkg; then
		PKG_HINT="opkg（OpenWrt 24.10 及更早）"
	fi
}

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
# 默认候选源 = 直连 + 实测可用的公共加速站。
#
# 这份清单是 2026-10 逐个实测出来的：用 curl 对 release 资产发 Range 请求，
# 能拿到 206/200 且内容正确的才写进来。被排除的典型：
#   gh.llkk.cc / github.moeyy.xyz / hub.gitmirror.com / gh.xxooo.cf —— 连接超时
#   gh-proxy.net —— 返回一段 JS 跳转页（563 字节），脚本无法直接下载
# 这类站点寿命普遍不长，所以清单必须"少而准"，并且随时能用 --mirror 覆盖。
DEFAULT_MIRRORS=",https://ghfast.top/,https://gh-proxy.com/,https://ghproxy.net/"

MIRRORS=""
build_mirrors() {
	if [ -n "$MIRROR_ARG" ]; then
		MIRRORS="$MIRROR_ARG"
	elif [ -n "${PORCH_BASE_URL:-}" ]; then
		MIRRORS=""
	else
		MIRRORS="$DEFAULT_MIRRORS"
	fi
	dbg "镜像列表: [$MIRRORS]"
}

# _fetch_once <url> <dest> [连接超时秒] [整体超时秒]
# 超时可调是为了「快速失败」：先给死源很短的时间，别在一个连不上的站上白等。
_fetch_once() {
	_f_url=$1; _f_dest=$2; _f_ct=${3:-15}; _f_mt=${4:-300}
	case "$DL_KIND" in
		curl)
			if [ "$INSECURE" = 1 ]; then
				curl -fsSL -k -m "$_f_mt" --connect-timeout "$_f_ct" -o "$_f_dest" "$_f_url"
			else
				curl -fsSL -m "$_f_mt" --connect-timeout "$_f_ct" -o "$_f_dest" "$_f_url"
			fi ;;
		wget)
			# GNU wget 没有「总超时」，-T/--timeout 管的是连接与每次读的超时。
			# 这里就用连接超时值：死源快速失败，大文件只要不断流就不会被掐。
			if [ "$DL_TOOL" = uclient-fetch ]; then
				uclient-fetch -q -T 60 -O "$_f_dest" "$_f_url" 2>/dev/null
			elif [ "$INSECURE" = 1 ]; then
				wget --no-check-certificate -q --timeout="$_f_ct" -O "$_f_dest" "$_f_url"
			else
				wget -q --timeout="$_f_ct" -O "$_f_dest" "$_f_url"
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

# probe_sources
# 给每个候选源测一次速，按实际耗时重排 MIRRORS。
#
# 为什么需要：大陆网络下各加速站差异极大（实测同一台机器上 ghfast.top 29 KB/s、
# ghproxy.net 只有 6.8 KB/s），固定顺序会让"能通但很慢"的源排前面，白白拖长下载。
# 探测只取 SHA256SUMS 的前 1KB（几毫秒就结束），代价很小。
#
# 只用 curl 能拿到亚秒耗时；wget/uclient-fetch 拿不到精确时间，就降级成
# "通/不通"分区（能通的保持原顺序在前），不做速度排序。
probe_sources() {
	[ "$PROBE" = 1 ] || return 0
	[ "$DRY_RUN" = 0 ] || { info "演练模式，跳过镜像测速"; return 0; }
	[ -n "$MIRRORS" ] || { info "只走直连，无需测速"; return 0; }
	[ -n "$DL_KIND" ] || return 0

	_pf="$WORK/.mirror-probe"
	: >"$_pf" 2>/dev/null || return 0

	_ifs=$IFS; IFS=,
	# shellcheck disable=SC2086
	set -- $MIRRORS
	IFS=$_ifs

	info "镜像测速中（各取 SHA256SUMS 前 1KB）…"
	_n=0
	for _p in "$@"; do
		_n=$((_n + 1))
		if [ -z "$_p" ]; then _label="(直连)"; else _label=$(_short_host "$_p"); fi
		_probe_url="${_p}${BASE_URL}/${TAG}/SHA256SUMS"

		_t=""
		if [ "$DL_KIND" = curl ]; then
			_t=$(curl -fsSL -m 12 --connect-timeout 6 -r 0-1023 -o /dev/null \
				-w '%{time_total}' "$_probe_url" 2>/dev/null)
		fi
		if [ -n "$_t" ]; then
			printf '%08.3f|%s\n' "$_t" "$_p" >>"$_pf"
			printf '   %-22s %6.2fs\n' "$_label" "$_t"
		else
			# 拿不到耗时（wget 系）或请求失败：用 999 排到最后
			printf '%08.3f|%s\n' 999 "$_p" >>"$_pf"
			printf '   %-22s %s\n' "$_label" "不可用/未知"
		fi
	done

	# 定长耗时做字典序排序 = 数值排序，不需要 sort -n 的兼容性顾虑
	_new=""
	while IFS= read -r _line; do
		_p=${_line#*|}
		if [ -z "$_new" ]; then _new=$_p; else _new="$_new,$_p"; fi
	done <<EOF
$(sort "$_pf")
EOF
	[ -n "$_new" ] && MIRRORS=$_new
	rm -f "$_pf" 2>/dev/null || true
	dbg "测速后镜像列表: [$MIRRORS]"
}

# download_asset <asset> <dest>
# 两轮策略，目的是"死源不要白等"：
#   第 1 轮 每个源只试 1 次，连接超时 10s —— 连不上就立刻换下一个
#   第 2 轮 对第 1 轮失败的源再试 2 次，连接超时 15s/整体 300s —— 网络抖动的情况
# 老实现是"每个源重试 3 次再换"，一个死源要耗掉 3×(15s+退避) ≈ 50s，
# 大陆网络下两三个站挂掉就是好几分钟，这正是要修的问题。
#
# 候选源 = 镜像前缀 × GitHub 基础 URL；PORCH_BASE_URL 可用逗号分隔给多个自建源，
# 自建源会额外尝试"站点根直接放资产"的扁平布局（Lucky 官方源就是这种）。
# MIRRORS 为空表示"只走直连"——用 set -- 展开，避免空列表导致循环体一次都不执行。
#
# GOOD_BASE 记住上一次成功的源：SHA256SUMS 下完后，主包直接用同一个源，
# 不用再把整个候选列表重走一遍。
GOOD_BASE=""
download_asset() {
	asset=$1; dest=$2
	[ -n "$DL_KIND" ] || die "没有可用的下载工具（curl / wget / uclient-fetch 都没有）" \
	                        "OpenWrt 可试: opkg update && opkg install uclient-fetch ca-bundle"
	[ -n "$BASE_URL" ] || die "内部错误: BASE_URL 未设置"

	# 候选列表，每项是 "URL|源标识"。源标识必须是「目录」而不是完整 URL，
	# 否则换文件时会把上一个文件的 URL 当成源，下回来的是错内容。
	if [ -n "${PORCH_BASE_URL:-}" ]; then
		_ifs=$IFS; IFS=,
		# shellcheck disable=SC2086
		set -- $PORCH_BASE_URL
		IFS=$_ifs
		_cands=""
		for _b in "$@"; do
			_b=${_b%/}
			_cands="${_cands}${_cands:+;}${_b}/$TAG/$asset|${_b}"
			# 站点根直接放资产的自建源（不强制 GitHub 的 releases/download 布局）
			case "$_b" in
				*/releases/download) ;;
				*) _cands="${_cands};${_b}/$asset|${_b}" ;;
			esac
		done
	else
		if [ -z "$MIRRORS" ]; then
			set -- ""
		else
			_ifs=$IFS; IFS=,
			# shellcheck disable=SC2086
			set -- $MIRRORS
			IFS=$_ifs
		fi
		_cands=""
		for _prefix in "$@"; do
			# 镜像站在前缀后面看到的必须是完整的原始 URL
			_b="${_prefix}${BASE_URL}"
			_cands="${_cands}${_cands:+;}${_b}/$TAG/$asset|${_b}"
		done
	fi

	_ifs=$IFS; IFS=';'
	# shellcheck disable=SC2086
	set -- $_cands
	IFS=$_ifs

	# 上次成功过的源提到最前（一次安装要下两三个文件，后面的不用再试错）
	if [ -n "$GOOD_BASE" ]; then
		_ordered=""
		for _c in "$@"; do
			[ "${_c#*|}" = "$GOOD_BASE" ] && _ordered="${_ordered}${_ordered:+;}$_c"
		done
		for _c in "$@"; do
			[ "${_c#*|}" = "$GOOD_BASE" ] || _ordered="${_ordered}${_ordered:+;}$_c"
		done
		_ifs=$IFS; IFS=';'
		# shellcheck disable=SC2086
		set -- $_ordered
		IFS=$_ifs
	fi

	if [ "$DRY_RUN" = 1 ]; then
		for _c in "$@"; do info "[dry-run] 下载 ${_c%%|*}"; done
		return 0
	fi

	# ---- 第 1 轮：每个源 1 次，只等连接握手，快速失败 ----
	# 整体超时给到 900s：慢速线路下 2.8MB 可能要几分钟，靠 connect-timeout
	# 快速踢掉死源就够了，别用整体超时把正常的大文件下载掐断。
	_failed=""
	for _c in "$@"; do
		_u=${_c%%|*}; _b=${_c#*|}
		host=$(_short_host "$_u"); [ -z "$host" ] && host=$_b
		info "下载 $asset  ← ${host}"
		if _fetch_once "$_u" "$dest.part" 10 900; then
			mv -f "$dest.part" "$dest" 2>/dev/null || { rm -f "$dest.part"; continue; }
			ok "下载完成 $(du -h "$dest" 2>/dev/null | cut -f1)"
			GOOD_BASE=$_b
			return 0
		fi
		rm -f "$dest.part"
		warn "$host 不通/超时，换下一个"
		_failed="${_failed:+$_failed;}${_c}"
	done

	# ---- 第 2 轮：对失败过的源再给两次机会（网络抖动）----
	[ -n "$_failed" ] || return 1
	warn "所有源第一轮都没成功，开始第二轮重试（更长的连接超时）"
	_ifs=$IFS; IFS=';'
	# shellcheck disable=SC2086
	set -- $_failed
	IFS=$_ifs
	for _c in "$@"; do
		_u=${_c%%|*}; _b=${_c#*|}
		host=$(_short_host "$_u"); [ -z "$host" ] && host=$_b
		attempt=1
		while [ "$attempt" -le 2 ]; do
			info "重试 $asset  ← ${host}（第 $attempt 次）"
			if _fetch_once "$_u" "$dest.part" 15 900; then
				mv -f "$dest.part" "$dest" 2>/dev/null || { rm -f "$dest.part"; continue; }
				ok "下载完成 $(du -h "$dest" 2>/dev/null | cut -f1)"
				GOOD_BASE=$_b
				return 0
			fi
			rm -f "$dest.part"
			warn "$host 第 $attempt 次失败"
			case $attempt in 1) sleep 2 ;; *) sleep 4 ;; esac
			attempt=$((attempt + 1))
		done
	done
	return 1
}

# 取最新 tag：先 API（最准），再跟随 releases/latest 的跳转（不需要 API 配额）
# 大陆网络下 api.github.com 经常被墙，所以镜像前缀也要挨个试一遍。
latest_tag() {
	_api="https://api.github.com/repos/$REPO/releases/latest"
	if have curl; then
		t=$(curl -fsSL -m 15 "$_api" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
		[ -n "$t" ] && { printf '%s' "$t"; return 0; }
	fi
	if [ -n "$DL_KIND" ]; then
		_ifs=$IFS; IFS=,
		# shellcheck disable=SC2086
		if [ -z "$MIRRORS" ]; then set -- ""; else set -- $MIRRORS; fi
		IFS=$_ifs
		for _prefix in "$@"; do
			t=$(_fetch_once "${_prefix}${_api}" - 10 20 2>/dev/null)
			t=$(printf '%s' "$t" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
			[ -n "$t" ] && { printf '%s' "$t"; return 0; }
		done
	fi
	# 再兜底：解析 releases/latest 的 302 Location
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

# ------------------------------- 端口 ---------------------------------------
# 端口不写死：默认 5200，但 -p / 交互输入 / UCI / LuCI 设置页都能改。
# 真正落到两个地方：① 服务启动参数 -p（systemd/procd/rc.d/launchd）
#                   ② OpenWrt 的 /etc/config/porch 里的 option port
# 程序配置文件里的 AdminWebListenPort 不用动——命令行 -p 优先级高于它。

# 从已装好的地方读回当前实际端口（升级/改端口时用来对比与展示）
# 优先级与运行时一致：OpenWrt 的 UCI > 程序配置 base.json > 服务参数 -p（旧版本写法）
service_port() {
	_sp=""
	if [ -f /etc/config/porch ] && have uci; then
		_sp=$(uci -q get porch.@porch[0].port 2>/dev/null || true)
	fi
	if [ -z "$_sp" ] && [ -f "$CONF_DIR/base.json" ]; then
		_sp=$(sed -n 's/.*"adminWebListenPort"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' \
			"$CONF_DIR/base.json" 2>/dev/null | head -1)
	fi
	if [ -z "$_sp" ] && [ -f /etc/systemd/system/porch.service ]; then
		_sp=$(sed -n 's/^ExecStart=.*[[:space:]]-p[[:space:]]\{1,\}\([0-9][0-9]*\).*/\1/p' \
			/etc/systemd/system/porch.service 2>/dev/null | head -1)
	fi
	if [ -z "$_sp" ] && [ -f /usr/local/etc/rc.d/porch ]; then
		_sp=$(sed -n 's/.*-p[[:space:]]\{1,\}\([0-9][0-9]*\).*/\1/p' \
			/usr/local/etc/rc.d/porch 2>/dev/null | head -1)
	fi
	if [ -z "$_sp" ] && [ -f /Library/LaunchDaemons/cn.porch.daemon.plist ]; then
		_sp=$(sed -n 's/.*<string>-p<\/string><string>\([0-9][0-9]*\)<\/string>.*/\1/p' \
			/Library/LaunchDaemons/cn.porch.daemon.plist 2>/dev/null | head -1)
	fi
	printf '%s' "${_sp:-}"
}

# 把端口落进程序配置 base.json —— 这是「不写死」的关键：
# 服务启动参数里不带 -p，所以以后在网页「系统设置 → 管理端口」里改的端口
# 重启后能真正生效，不会被安装时的一个参数永久压住。
# 规则：-p 给了用它；配置里已有明确端口就保持不动；两者都没有就写默认值，
#       避免「脚本提示 5200、程序实际听 16601」这种两边对不上的情况。
apply_port() {
	[ "$DRY_RUN" = 0 ] || { info "[dry-run] 管理端口 $([ -n "$PORT" ] && echo "$PORT" || echo "默认 $DEFAULT_PORT") 写入 $CONF_DIR/base.json"; return 0; }
	mkdir -p "$CONF_DIR" 2>/dev/null || { [ -z "$PORT" ] || warn "无法创建配置目录 ${CONF_DIR}，端口未写入配置"; return 0; }
	_bj="$CONF_DIR/base.json"
	_up="$PORT"

	if [ ! -f "$_bj" ]; then
		[ -n "$_up" ] || _up=$DEFAULT_PORT
		# 只写端口一个键就够：程序加载时缺的字段自动用默认值补齐
		printf '{\n  "adminWebListenPort": %s\n}\n' "$_up" >"$_bj" || {
			warn "写入 $_bj 失败，改用服务参数 -p $_up 兜底"; [ -n "$PORT" ] && PORT_ARG="-p $PORT"; return 0; }
		chmod 600 "$_bj" 2>/dev/null || true
		ok "管理端口已写入 $CONF_DIR/base.json: $_up"
		return 0
	fi

	if [ -z "$PORT" ] && grep -q '"adminWebListenPort"' "$_bj" 2>/dev/null; then
		return 0    # 配置里已有明确端口，用户没要求改，别动
	fi
	[ -n "$_up" ] || _up=$DEFAULT_PORT

	if grep -q '"adminWebListenPort"' "$_bj" 2>/dev/null; then
		# 改已有键。正则用 [0-9][0-9]*（POSIX），GNU 的 \+ 在 macOS/BSD sed 上
		# 不报错但静默不匹配——所以替换完必须 grep 复核，不能只看退出码。
		sed -i.bak -e "s/\"adminWebListenPort\"[[:space:]]*:[[:space:]]*[0-9][0-9]*/\"adminWebListenPort\": $_up/" "$_bj" 2>/dev/null
		rm -f "$_bj.bak" 2>/dev/null || true
		if grep -q "\"adminWebListenPort\"[[:space:]]*:[[:space:]]*$_up" "$_bj" 2>/dev/null; then
			ok "管理端口已更新为 ${_up}（$CONF_DIR/base.json）"
		else
			warn "改不了 $_bj 里的端口，改用服务参数 -p $_up 兜底"; [ -n "$PORT" ] && PORT_ARG="-p $PORT"
		fi
	else
		# base.json 里还没有这个键：用 awk 在第一个 { 后面插一行。
		# 不用 sed 的 \n——那是各实现差异最大的地方，awk 是 POSIX 全平台一致。
		awk -v v="$_up" '
			BEGIN { done = 0 }
			{
				if (!done && $0 ~ /\{/) {
					print
					print "  \"adminWebListenPort\": " v ","
					done = 1
					next
				}
				print
			}' "$_bj" >"$_bj.new" && [ -s "$_bj.new" ] && mv "$_bj.new" "$_bj"
		if [ -s "$_bj.new" ] || grep -q '"adminWebListenPort"' "$_bj" 2>/dev/null; then
			rm -f "$_bj.new" 2>/dev/null || true
			ok "管理端口已写入 $CONF_DIR/base.json: $_up"
		else
			rm -f "$_bj.new" 2>/dev/null || true
			warn "改不了 ${_bj}，改用服务参数 -p $_up 兜底"; [ -n "$PORT" ] && PORT_ARG="-p $PORT"
		fi
	fi
	return 0
}

# 交互式问一次端口。只在这三种情况之外才问：
#   已经给了 -p / 加了 -y（无人值守）/ 不是真终端
# 注意 curl|sh 时 stdin 是脚本管道，必须从 /dev/tty 读，否则读不到用户输入。
ask_port() {
	[ -n "$PORT" ] && return 0
	[ -z "$CUR_VER" ] || return 0    # 已装过：升级沿用现有端口，别每次升级都问
	[ "$ASSUME_YES" = 0 ] || return 0
	[ "$DRY_RUN" = 0 ] || return 0
	[ "$CHECK_ONLY" = 0 ] || return 0
	[ "$UNINSTALL" = 0 ] || return 0
	[ -r /dev/tty ] && [ -w /dev/tty ] || return 0

	_ap=""
	_curap=$(service_port)
	_defap=${_curap:-$DEFAULT_PORT}
	printf '\n  管理端口（回车用 %s）: ' "$_defap" >/dev/tty
	IFS= read -r _ap </dev/tty || _ap=""
	[ -z "$_ap" ] && { info "管理端口: 用 $_defap"; return 0; }
	case "$_ap" in
		*[!0-9]*) warn "端口只能是数字，已改用 $_defap"; return 0 ;;
	esac
	if [ "$_ap" -lt 1 ] 2>/dev/null || [ "$_ap" -gt 65535 ] 2>/dev/null; then
		warn "端口超出 1-65535，已改用 $_defap"; return 0
	fi
	PORT="$_ap"
	ok "管理端口: $PORT"
}

# 已装过再跑脚本：别猜用户想干嘛，当面问（参考 Lucky 官方脚本的交互菜单）。
# 无人值守场景不受影响：-y / --file / -f / -v 都是明确意图，直接跳过菜单。
main_menu() {
	[ "$ASSUME_YES" = 0 ] || return 0
	[ -n "$CUR_VER" ] || return 0
	[ "$DRY_RUN" = 0 ] || return 0
	[ "$CHECK_ONLY" = 0 ] || return 0
	[ "$UNINSTALL" = 0 ] || return 0
	[ "$FORCE" = 0 ] || return 0
	[ -z "$PKG_PATH" ] || return 0
	[ -n "$VERSION_ARG" ] && [ "$VERSION_ARG" != latest ] && return 0
	[ -r /dev/tty ] && [ -w /dev/tty ] || return 0

	printf '\n  检测到已安装 Porch %s  (%s)\n' "$CUR_VER" "$BIN_PATH"
	_cur_port=$(service_port)
	info "当前管理端口: ${_cur_port:-$DEFAULT_PORT}   最新版本: v$VER"
	printf '\n'
	printf '  1) 升级到最新版 v%s\n' "$VER"
	printf '  2) 重装当前版本 %s（配置保留）\n' "$CUR_VER"
	printf '  3) 只改管理端口\n'
	printf '  4) 卸载（保留配置）\n'
	printf '  5) 卸载并删除配置\n'
	printf '  0) 什么都不做，退出\n'
	printf '  选择 [回车=1]: '
	_m=""
	IFS= read -r _m </dev/tty || _m=""
	case "$_m" in
		''|1) return 0 ;;
		2) FORCE=1; ok "将重装当前版本"; return 0 ;;
		3)
			_ap=""
			printf '  新管理端口（1-65535，回车取消）: ' >/dev/tty
			IFS= read -r _ap </dev/tty || _ap=""
			case "$_ap" in
				'') info "未改端口"; exit 0 ;;
				*[!0-9]*) die "端口只能是数字: $_ap" ;;
			esac
			[ "$_ap" -ge 1 ] 2>/dev/null && [ "$_ap" -le 65535 ] 2>/dev/null || die "端口超出 1-65535: $_ap"
			PORT="$_ap"
			step "只改管理端口（${_cur_port:-$DEFAULT_PORT} → ${PORT}）"
			install_service
			ok "端口已改为 $PORT"
			exit 0 ;;
		4) UNINSTALL=1; do_uninstall ;;
		5) PURGE=1; UNINSTALL=1; do_uninstall ;;
		0|q|Q) info "已退出，未做任何修改"; exit 0 ;;
		*) warn "无效选择「${_m}」，按升级继续"; return 0 ;;
	esac
}

# ------------------------------- 服务 ---------------------------------------
write_systemd() {
	_unit=/etc/systemd/system/porch.service
	# 端口不写进 ExecStart（除非配置写不进去）：让它以 base.json 为准，
	# 这样网页里改端口重启才生效
	_exec="$INSTALL_DIR/$BIN_NAME -cd $CONF_DIR${PORT_ARG:+ $PORT_ARG}"
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
EOF
	elif [ -n "$PORT" ] && have uci; then
		# 已有配置（重装 / 只改端口）：只动 port 一项，别覆盖用户在 LuCI 里改过的其它设置
		uci -q set porch.@porch[0].port="$PORT" && uci -q commit porch 2>/dev/null && \
			dbg "已更新 UCI 端口: $PORT"
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
porch_flags="-cd $CONF_DIR${PORT_ARG:+ $PORT_ARG}"
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
	[ -n "$PORT_ARG" ] && _args="$_args<string>-p</string><string>$PORT</string>"
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
	# 端口先落配置（base.json），服务参数只在配置写不进去时兜底
	apply_port
	[ "$NO_SERVICE" = 1 ] && { info "按 --no-service 跳过服务安装"; return 0; }
	if [ "$USER_INSTALL" = 1 ] || [ "$IS_ROOT" = 0 ]; then
		info "非 root 安装，跳过开机自启（需要时手动运行 $INSTALL_DIR/${BIN_NAME}$([ -n "$PORT" ] && echo " -p $PORT")）"
		return 0
	fi
	case "$INIT_SYS" in
		systemd) write_systemd ;;
		procd)   write_procd ;;
		rcng)    write_rcng ;;
		launchd) write_launchd ;;
		*)       warn "未识别的 init 系统，已跳过开机自启；手动启动: $INSTALL_DIR/$BIN_NAME -cd $CONF_DIR${PORT_ARG:+ $PORT_ARG}" ;;
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

# ------------------------------ LuCI 界面 ------------------------------------
# OpenWrt 上「后端程序」和「LuCI 界面」是两个独立的包。
# 本脚本主流程故意不依赖包管理器（直接放静态二进制 + 写 procd 脚本），
# 所以光跑完主流程只有后端，界面得自己再 apk/opkg 装一次 —— 这就是
# 「装完没有 LuCI」的原因。下面这段把界面补上：
#   有 LuCI 的系统 → 自动装；纯后端系统 → 跳过（可 --luci 强制）；
#   --no-luci → 一律不装。
HAS_LUCI=0
LUCI_PKG_MGR=""
LUCI_INSTALLED=0

detect_luci() {
	HAS_LUCI=0
	LUCI_PKG_MGR=""
	[ "$DISTRO" = openwrt ] || return 0
	# /www/luci-static 是 LuCI 的静态资源目录，比 /etc/config/luci 更可靠
	if [ -d /www/luci-static ] || [ -f /etc/config/luci ]; then HAS_LUCI=1; fi
	if have apk; then LUCI_PKG_MGR=apk
	elif have opkg; then LUCI_PKG_MGR=opkg
	fi
}

# luci-base 是界面包的硬依赖，缺了装不上
luci_base_installed() {
	case "$LUCI_PKG_MGR" in
		apk)  apk list --installed luci-base 2>/dev/null | grep -q '^luci-base[- ]' ;;
		opkg) opkg list-installed 2>/dev/null | grep -q '^luci-base ' ;;
		*) return 1 ;;
	esac
}

install_luci() {
	[ "$LUCI_MODE" = no ] && { info "按 --no-luci 跳过 LuCI 界面"; return 0; }
	if [ "$DISTRO" != openwrt ]; then
		[ "$LUCI_MODE" = yes ] && warn "LuCI 界面只适用于 OpenWrt，已跳过"
		return 0
	fi
	if [ "$USER_INSTALL" = 1 ] || [ "$IS_ROOT" = 0 ]; then
		info "非 root 安装，跳过 LuCI 界面（装包需要 root）"
		return 0
	fi
	if [ -z "$LUCI_PKG_MGR" ]; then
		warn "系统里既没有 apk 也没有 opkg，无法安装 LuCI 界面"
		info "界面包在发布页: luci-app-porch_${VER}_all.apk / .ipk，可手动解压到 /"
		return 0
	fi
	if [ "$HAS_LUCI" = 0 ] && [ "$LUCI_MODE" != yes ]; then
		info "系统里没检测到 LuCI，跳过界面安装（要装请加 --luci）"
		return 0
	fi

	case "$LUCI_PKG_MGR" in
		apk)  _ext=apk ;;
		opkg) _ext=ipk ;;
	esac
	_lasset="luci-app-porch_${VER}_all.${_ext}"
	_lfile="$WORK/$_lasset"

	step "安装 LuCI 界面（服务 → Porch 门廊）"

	# 取包：离线模式找同目录，在线模式走同一套镜像
	if [ "$LOCAL_PKG" = 1 ]; then
		_lsrc="$(dirname "$PKG_PATH")/$_lasset"
		if [ ! -f "$_lsrc" ]; then
			info "本地包旁边没有 ${_lasset}，跳过 LuCI 界面"
			info "  单独下载: ${TAG}/${_lasset}"
			return 0
		fi
		cp -f "$_lsrc" "$_lfile" 2>/dev/null || { warn "复制 $_lasset 失败"; return 0; }
		ok "已读取 $_lasset"
	else
		if ! download_asset "$_lasset" "$_lfile"; then
			warn "没下载到 ${_lasset}，跳过 LuCI 界面（后端不受影响）"
			return 0
		fi
	fi

	if ! luci_base_installed; then
		warn "系统里没有 luci-base，界面包装不上（缺硬依赖）"
		case "$LUCI_PKG_MGR" in
			apk)  info "先装基础包: apk add luci-base" ;;
			opkg) info "先装基础包: opkg update && opkg install luci-base" ;;
		esac
		info "装好后重跑本脚本即可（配置不会丢）"
		return 0
	fi

	if [ "$DRY_RUN" = 1 ]; then
		info "[dry-run] 会用 $LUCI_PKG_MGR 安装 $_lasset"
		return 0
	fi

	# 依赖已在本地，别让 apk 再去联网同步官方源（大陆网络会卡很久）。
	# 第一遍正常装；失败退到「忽略缺失依赖」——一键脚本装的 porch 不在
	# 包管理器数据库里，老版本界面包把 porch 写进依赖时会在这里被卡住。
	case "$LUCI_PKG_MGR" in
		apk)
			apk add --repositories-file /dev/null --allow-untrusted "$_lfile" 2>/dev/null || \
			apk add --repositories-file /dev/null --allow-untrusted --force-broken-world "$_lfile"
			_rc=$? ;;
		opkg)
			opkg install "$_lfile" 2>/dev/null || opkg install --force-depends "$_lfile"
			_rc=$? ;;
	esac
	if [ "$_rc" != 0 ]; then
		warn "安装 $_lasset 失败（退出码 ${_rc}）"
		info "常见原因: ① luci-base 版本不匹配 ② 包管理器正忙（等几秒重试） ③ 空间不足"
		info "手动重试: $LUCI_PKG_MGR install $_lfile"
		return 0
	fi

	# 让新菜单/ACL 立刻生效
	rm -f /tmp/luci-indexcache* 2>/dev/null || true
	/etc/init.d/rpcd reload 2>/dev/null || true
	ok "LuCI 界面已安装"
	LUCI_INSTALLED=1
}

# 卸载时把界面包一起摘掉，否则菜单会指向一个不存在的后端
remove_luci() {
	_luci_gone=0
	if [ "$LUCI_INSTALLED" != 1 ]; then
		# 上次装的、这次没装：只要包在就一并删
		case "${LUCI_PKG_MGR:-}" in
			apk)  apk list --installed luci-app-porch 2>/dev/null | grep -q '^luci-app-porch' || _luci_gone=1 ;;
			opkg) opkg list-installed 2>/dev/null | grep -q '^luci-app-porch ' || _luci_gone=1 ;;
			*)    _luci_gone=1 ;;
		esac
	fi

	if [ "$_luci_gone" != 1 ]; then
		case "${LUCI_PKG_MGR:-}" in
			apk)  apk del luci-app-porch 2>/dev/null && ok "已移除 LuCI 界面包" ;;
			opkg) opkg remove luci-app-porch 2>/dev/null && ok "已移除 LuCI 界面包" ;;
		esac
	fi

	# 硬删兜底：包管理器删失败 / 记账不一致 / 手动解压装的情况，
	# 把界面文件按清单直接摘掉，保证「卸载完菜单里真的没有」
	if [ -e /usr/share/luci/menu.d/luci-app-porch.json ] || \
	   [ -e /www/luci-static/resources/view/porch ]; then
		rm -f /usr/share/luci/menu.d/luci-app-porch.json \
		      /usr/share/rpcd/acl.d/luci-app-porch.json \
		      /usr/libexec/rpcd/porch \
		      /www/luci-static/resources/view/porch/status.js \
		      /www/luci-static/resources/view/porch/settings.js 2>/dev/null || true
		rmdir /www/luci-static/resources/view/porch \
		      /usr/share/luci/menu.d /usr/share/rpcd/acl.d 2>/dev/null || true
		ok "已清除 LuCI 界面残留文件"
	fi

	rm -f /tmp/luci-indexcache* 2>/dev/null || true
	/etc/init.d/rpcd reload 2>/dev/null || true
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
	if [ -n "$LUCI_PKG_MGR" ]; then
		if [ "$HAS_LUCI" = 1 ]; then
			info "LuCI: 已检测到（装完会一并安装界面包，包管理器 ${LUCI_PKG_MGR}）"
		elif [ "$LUCI_MODE" = yes ]; then
			info "LuCI: 未检测到，但指定了 --luci，仍会尝试安装"
		else
			info "LuCI: 未检测到（纯后端环境，加 --luci 可强制装界面）"
		fi
	fi
	info "日志文件: $LOG_FILE"
	if [ -n "$PORT" ]; then
		info "管理端口: $PORT"
	elif [ "$UNINSTALL" = 0 ] && [ "$CHECK_ONLY" = 0 ]; then
		_pp=$(service_port)
		if [ -n "$_pp" ]; then
			info "管理端口: ${_pp}（沿用现有配置）"
		else
			info "管理端口: 待定（默认 ${DEFAULT_PORT}，稍后可改）"
		fi
	fi
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
detect_openwrt_pkg

# 位置参数（兼容 Lucky 官方脚本的传参风格 sh install.sh <URL>）当作自建下载源。
# 兜底顺序：--mirror > 位置参数/ PORCH_BASE_URL > 默认加速池
if [ -n "$BASE_URL_ARG" ]; then
	case "$BASE_URL_ARG" in
		*://*) PORCH_BASE_URL=$BASE_URL_ARG ;;
		*) die "下载源地址格式不对: $BASE_URL_ARG" "应以 http:// 或 https:// 开头" ;;
	esac
fi

build_mirrors
detect_luci

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

# 如果 porch 本身是包管理器装的（OpenWrt 上从 ipk/apk 源装的），
# 用包管理器删更干净：它会顺带清掉 /etc/rc.d/S99porch 软链、conffiles 记录。
# 本脚本主流程是自己放二进制，不产生包记录，这里查不到就走后面的 rm。
remove_pkg_porch() {
	[ "$DRY_RUN" = 1 ] && return 1
	case "${LUCI_PKG_MGR:-}" in
		apk)
			apk list --installed porch 2>/dev/null | grep -q '^porch[- ]' || return 1
			apk del porch >/dev/null 2>&1 || return 1
			ok "已用 apk 移除 porch 包"
			return 0 ;;
		opkg)
			opkg list-installed 2>/dev/null | grep -q '^porch ' || return 1
			opkg remove porch >/dev/null 2>&1 || return 1
			ok "已用 opkg 移除 porch 包"
			return 0 ;;
		*) return 1 ;;
	esac
}

do_uninstall() {
	step "卸载 $PROJECT"
	detect_luci
	printf '  将移除: 服务自启项 / 程序二进制 / LuCI 界面包'
	[ "$PURGE" = 1 ] && printf ' / 配置目录'
	printf '\n'
	[ "$PURGE" = 0 ] && info "配置目录会保留；要一起删加 --purge"

	remove_service

	# 先摘 LuCI 界面包再删后端：老版本界面包依赖 porch，
	# 顺序反了会被包管理器以 "not removed due to: luci-app-porch" 拒绝
	remove_luci

	if ! remove_pkg_porch && [ -n "$CUR_BIN" ] && [ -f "$CUR_BIN" ]; then
		[ "$DRY_RUN" = 1 ] || rm -f "$CUR_BIN"
		ok "已删除 $CUR_BIN"
	elif [ -z "$CUR_BIN" ]; then
		info "未发现已安装的可执行文件"
	fi

	# 残留的 pid / launchd 日志
	[ "$DRY_RUN" = 1 ] || rm -f /var/run/porch.pid 2>/dev/null || true
	[ "$DRY_RUN" = 1 ] || rm -f /var/log/porch.log /var/log/porch.err 2>/dev/null || true

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
	[ "$PURGE" = 0 ] && printf '  配置仍在 %s，重新装回来会沿用；要彻底清掉: sh install.sh --uninstall --purge\n' "$CONF_DIR"
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
			"文件名应形如 porch_1.1.2_linux_x86_64.tar.gz；或用 -v 1.1.2 明确指定版本"
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
			info "要装指定版本: sh install.sh -v 1.1.2"
			exit 0
		fi
		die "拿不到最新版本号" "网络可能被限制：用 -v 指定版本，或加 --mirror 指定镜像"
	fi
	VER=${TAG#v}
	ok "最新版本: $TAG"
fi

case "$VER" in
	''|*[!0-9.]*) die "版本号格式不对: ${VERSION_ARG:-$VER}" "示例: -v 1.1.2" ;;
esac

[ -n "$ASSET" ] || ASSET="${BIN_NAME}_${VER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download"

# 已装过的机器：先问清楚要升级、重装、改端口还是卸载，别自作主张
main_menu

# 端口不写死：没给 -p 也没 -y 时，在环境检测前问一次，让下面的输出显示最终值
ask_port

print_env
info "目标版本: ${C_BLD}v$VER${C_RESET}  目标包: $ASSET"

# 同版本已安装且没 -f：直接收工（幂等，脚本可以重复跑）
if [ -n "$CUR_VER" ] && [ "$CUR_VER" = "$VER" ] && [ "$FORCE" = 0 ] && [ "$DRY_RUN" = 0 ]; then
	_cur_port=$(service_port)
	if [ -z "$PORT" ] || [ "$PORT" = "$_cur_port" ]; then
		ok "已是 ${VER}，无需重复安装（重装请加 -f）"
		if [ -x "$BIN_PATH" ]; then info "程序: $BIN_PATH"; fi
		info "管理界面: http://<本机IP>:${_cur_port:-$DEFAULT_PORT}"
		info "想换端口: sh install.sh -p 8080   （只改端口，不重新下载）"
		info "想卸载:   sh install.sh --uninstall"
		exit 0
	fi
	# 版本没变、只是端口变了：只重写服务配置，不重新下载 2.8MB 二进制
	step "只改管理端口（${_cur_port:-$DEFAULT_PORT} → ${PORT}）"
	install_service
	ok "端口已改为 $PORT"
	info "管理界面: http://<本机IP>:$PORT"
	info "已生效的话无需重启；没生效就重启一次服务"
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
	probe_sources
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
install_luci

# ------------------------------- 收尾 ---------------------------------------
MYIP=""
if have ip; then MYIP=$(ip route get 1.1.2.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -1); fi
[ -z "$MYIP" ] && MYIP=$(hostname -i 2>/dev/null | awk '{print $1}')
[ -z "$MYIP" ] && MYIP="<本机IP>"

step "完成"
SHOW_PORT=${PORT:-$(service_port)}
[ -n "$SHOW_PORT" ] || SHOW_PORT=$DEFAULT_PORT
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

_extra_done=0
if [ -n "$PKG_HINT" ]; then
	printf '  包管理器   %s\n' "$PKG_HINT"
	_extra_done=1
fi
if [ "$LUCI_INSTALLED" = 1 ]; then
	printf '  LuCI 界面  已安装 → 路由器后台「服务 → Porch 门廊」\n'
	_extra_done=1
elif [ -n "$LUCI_PKG_MGR" ] && [ "$LUCI_MODE" != no ]; then
	if [ "$HAS_LUCI" = 1 ]; then _why="下载或依赖失败，重跑本脚本即可"; else _why="系统里没有 LuCI，加 --luci 可强制装"; fi
	printf '  LuCI 界面  %s未安装%s（%s）\n' "$C_YEL" "$C_RESET" "$_why"
	printf '             手动装: %s install <发布页>/luci-app-porch_%s_all.%s\n' \
		"$LUCI_PKG_MGR" "$VER" "$([ "$LUCI_PKG_MGR" = apk ] && echo apk || echo ipk)"
	_extra_done=1
fi
[ "$_extra_done" = 1 ] && printf '\n'

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
  改端口  sh install.sh -p 8080              只改端口，不重新下载二进制
  卸载  sh install.sh --uninstall          服务 + 程序 + LuCI 界面包，配置保留
        sh install.sh --uninstall --purge  连配置目录一起删（不可恢复）

  下一步（不用看文档也能走完）
    1. 浏览器打开上面的地址，登录后到「动态域名」加一条任务
    2. 「SSL 证书」里申请一张证书（需要 80 端口能从公网访问）
    3. 「反向代理」把内网服务映射成一个能在外网打开的网址
    概览页顶部有同样的三步向导，点一下就能跳过去。

EOF

_log OK "安装完成: v$VER -> $BIN_PATH"
exit 0
