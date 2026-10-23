#!/bin/sh
# 把预编译二进制打成 OpenWrt 25.12+ 用的 .apk（apk-tools v3 / ADB 格式），
# 不需要 OpenWrt SDK —— 直接调 apk-tools 的 `apk mkpkg`，这也是 OpenWrt 自己
# include/package-pack.mk 走的同一条路。
#
# 为什么不能像 ipk 那样用 Python 手搓：ipk 是「gz 过的 tar + 三段固定成员」，
# 用 stdlib 就能写；apk v3 是 Alpine 自研的 ADB 二进制容器（文件头 magic "ADB"），
# 字段布局只由 apk-tools 的 C 实现规定，手写等于复刻它的序列化逻辑。
#
# 前置：apk-tools >= 3.0（sh scripts/install-apk-tools.sh <dir> 可拿到静态版）
#       并且要以 root 运行（mkpkg 会记录磁盘上的属主，fakeroot 对它无效）
#
# 用法:
#   sh scripts/make_apk.sh porch --binary <porch 二进制> --version 1.1.0 \
#       [--init openwrt/porch/files/porch.init] [--uci openwrt/porch/files/porch.uci] \
#       [--arch noarch] --out dist/porch_1.1.0_openwrt_x86_64.apk
#
#   sh scripts/make_apk.sh luci --version 1.1.0 [--src openwrt/luci-app-porch] \
#       --out dist/luci-app-porch_1.1.0_all.apk
#
# 环境变量:
#   APK_BIN      apk 可执行文件路径（默认 PATH 里的 apk）
#   STAGE_ONLY=1 只准备目录树并打印清单，不调 mkpkg（非 Linux 上也能跑，用来核对内容）
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
APK=${APK_BIN:-apk}
STAGE_ONLY=${STAGE_ONLY:-0}
MAINTAINER="Porch contributors <porch@users.noreply.github.com>"
LICENSE="MIT"
URL="https://github.com/likz0421/porch"

usage() {
	cat >&2 <<'U'
用法:
  sh scripts/make_apk.sh porch --binary <二进制> --version 1.1.0 [--arch noarch] --out <输出>
  sh scripts/make_apk.sh luci  --version 1.1.0 [--src <LuCI 源码目录>] --out <输出>

环境变量: APK_BIN（apk 可执行文件）STAGE_ONLY=1（只准备文件树，不调 mkpkg）
U
	exit 2
}

have() { command -v "$1" >/dev/null 2>&1; }

sha256_of() {
	# GNU coreutils 在文件名需要转义时会输出 "\<hash>  <name>"，先去掉前导反斜杠
	if have sha256sum; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi | sed 's/^\\//'
}

# apk 的版本语法是 X.Y.Z-rN（注意不是 opkg 的 X.Y.Z-N）
apk_version() {
	case "$1" in
		*-r*) printf '%s' "$1" ;;   # 已经是 X.Y.Z-rN
		*)    printf '%s-r0' "$1" ;; # 1.1.0 → 1.1.0-r0
	esac
}

# 写 OpenWrt apk 的记账文件，等价于 include/package-pack.mk 做的事：
#   /lib/apk/packages/<pkg>.list             本包安装的所有文件（绝对路径）
#   /lib/apk/packages/<pkg>.conffiles        配置文件清单
#   /lib/apk/packages/<pkg>.conffiles_static "<路径> <sha256>"
# 有了这些，apk 才知道哪些文件归它管、升级时哪些配置不能被覆盖。
write_pkg_meta() { # <staging> <pkgname> [conffile...]
	stg=$1; pkg=$2; shift 2
	root="$stg/files"
	meta="$root/lib/apk/packages"
	mkdir -p "$meta"
	(cd "$root" && find . \( -type f -o -type l \) | sed 's|^\./|/|' | LC_ALL=C sort) >"$stg/pkg.list"
	mv "$stg/pkg.list" "$meta/$pkg.list"
	if [ $# -gt 0 ]; then
		: >"$meta/$pkg.conffiles"
		: >"$meta/$pkg.conffiles_static"
		for f in "$@"; do
			echo "$f" >>"$meta/$pkg.conffiles"
			echo "$f $(sha256_of "$root$f")" >>"$meta/$pkg.conffiles_static"
		done
	fi
	find "$root/lib" -type d -exec chmod 755 {} + 2>/dev/null || true
	find "$meta" -type f -exec chmod 644 {} + 2>/dev/null || true
}

# pack <staging> <name> <apkver> <arch> <depends> <desc> <out> [TYPE:FILE...]
pack() {
	stg=$1; name=$2; ver=$3; arch=$4; deps=$5; desc=$6; out=$7; shift 7

	if [ "$STAGE_ONLY" = 1 ]; then
		echo "==> [stage-only] $name $ver ($arch)"
		echo "    文件树:"
		(cd "$stg/files" && find . -type f | LC_ALL=C sort | sed 's|^\./|      /|')
		echo "    记账文件:"
		find "$stg/files/lib" -type f 2>/dev/null | sed 's|^.*/files|      |'
		echo "    脚本: $(ls "$stg/scripts" 2>/dev/null | tr '\n' ' ')"
		echo "    （未调用 apk mkpkg，所以没有产物）"
		return 0
	fi

	scripts=""
	for s in "$@"; do scripts="$scripts --script $s"; done

	# shellcheck disable=SC2086  # 故意不引号：$scripts 要展开成多个参数
	SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0} "$APK" mkpkg \
		--info "name:$name" \
		--info "version:$ver" \
		--info "description:$desc" \
		--info "arch:$arch" \
		--info "license:$LICENSE" \
		--info "origin:$name" \
		--info "url:$URL" \
		--info "maintainer:$MAINTAINER" \
		--info "depends:$deps" \
		$scripts \
		--files "$stg/files" \
		--output "$out"
	_kb=$(( ($(wc -c <"$out") + 1023) / 1024 ))
	echo "apk: $name $ver ($arch) → $out (${_kb} KB)"
}

# 检查环境：真打包时才需要 root + apk>=3
require_tools() {
	[ "$STAGE_ONLY" = 1 ] && return 0
	[ "$(id -u 2>/dev/null || echo 1)" = 0 ] || {
		echo "make_apk.sh: 必须以 root 运行（mkpkg 记录磁盘属主，fakeroot 无效）；CI 里用 sudo -E" >&2
		exit 1
	}
	command -v "$APK" >/dev/null 2>&1 || {
		echo "make_apk.sh: 找不到 apk（先跑 sh scripts/install-apk-tools.sh <dir>，再设 APK_BIN）" >&2
		exit 1
	}
	case "$("$APK" --version 2>/dev/null)" in
		"apk-tools "[3-9].*) ;;
		*) echo "make_apk.sh: $APK 版本太旧，需要 apk-tools >= 3.0（才有 mkpkg）" >&2; exit 1 ;;
	esac
}

# --------------------------------------------------------------------------- #
# porch 主包
# --------------------------------------------------------------------------- #
build_porch() {
	binary=""; version=""; arch=noarch; out=""; init=""; uci=""
	while [ $# -gt 0 ]; do
		case "$1" in
			--binary)  binary=$2; shift 2 ;;
			--version) version=$2; shift 2 ;;
			--arch)    arch=$2; shift 2 ;;
			--out)     out=$2; shift 2 ;;
			--init)    init=$2; shift 2 ;;
			--uci)     uci=$2; shift 2 ;;
			*) echo "未知参数: $1" >&2; usage ;;
		esac
	done
	[ -n "$binary" ] && [ -n "$version" ] && [ -n "$out" ] || usage
	[ -f "$binary" ] || { echo "找不到二进制: $binary" >&2; exit 1; }
	[ -n "$init" ] || init="$ROOT/openwrt/porch/files/porch.init"
	[ -n "$uci" ]  || uci="$ROOT/openwrt/porch/files/porch.uci"
	[ -f "$init" ] || { echo "找不到 init 脚本: $init" >&2; exit 1; }
	[ -f "$uci" ]  || { echo "找不到 uci 配置: $uci" >&2; exit 1; }

	require_tools
	ver=$(apk_version "$version")

	stg=$(mktemp -d 2>/dev/null || echo "/tmp/porch-apk.$$")
	mkdir -p "$stg/scripts" "$stg/files/usr/bin" "$stg/files/etc/init.d" "$stg/files/etc/config"
	d="$stg/files"
	install -m 755 "$binary" "$d/usr/bin/porch" 2>/dev/null || { cp "$binary" "$d/usr/bin/porch"; chmod 755 "$d/usr/bin/porch"; }
	install -m 755 "$init" "$d/etc/init.d/porch" 2>/dev/null || { cp "$init" "$d/etc/init.d/porch"; chmod 755 "$d/etc/init.d/porch"; }
	install -m 644 "$uci"  "$d/etc/config/porch" 2>/dev/null || { cp "$uci" "$d/etc/config/porch"; chmod 644 "$d/etc/config/porch"; }
	chmod 755 "$d/usr/bin/porch" "$d/etc/init.d/porch"
	chmod 644 "$d/etc/config/porch"
	find "$d" -type d -exec chmod 755 {} + 2>/dev/null || true
	# 真打包时必须带可执行位；Windows 上 MSYS 是按 shebang 判断执行权的，
	# 对无后缀的 ELF 永远显示 644，chmod 也不生效，所以只在类 Unix 上强校验。
	if [ ! -x "$d/usr/bin/porch" ]; then
		case "$(uname -s 2>/dev/null)" in
			MINGW*|MSYS*|CYGWIN*) : ;;  # Windows 本地预览，跳过
			*) echo "porch 二进制没拿到可执行位，abort" >&2; exit 1 ;;
		esac
	fi

	write_pkg_meta "$stg" porch /etc/config/porch

	# 安装后自检：装完立刻试跑一次。apk 的 arch 我们写 noarch（见文档说明：
	# OpenWrt 各子目标 arch 名不统一，写死会把多数机型挡在门外），代价是
	# 装错架构 apk 不拦，所以这里补一道运行期校验，跑不通就明确说出来。
	cat >"$stg/scripts/post-install" <<'EOS'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0

# 1) 运行期架构自检：apk 的 arch 写 noarch，装错架构它不会拦，这里补一道
if /usr/bin/porch -v >/dev/null 2>&1; then
	echo "porch: $(/usr/bin/porch -v 2>/dev/null | head -n1)"
else
	echo "porch: [警告] /usr/bin/porch 无法在本机执行，很可能装错了架构包。" >&2
	echo "porch: 本机 uname -m = $(uname -m)；到 Releases 按架构名重新下载：" >&2
	echo "porch:   https://github.com/likz0421/porch/releases/latest" >&2
	echo "porch: MIPS 机型若报 Illegal instruction，请换 softfloat 版本。" >&2
fi

# 2) 开机自启。先走 rc.common 的标准路径，再无条件兜底建 rc.d 链接
#    —— 不同 OpenWrt 版本 rc.common 的 enable 行为不完全一致，不能只依赖它。
chmod 755 /etc/init.d/porch 2>/dev/null || true
/etc/init.d/porch enable >/dev/null 2>&1 || true
if [ ! -L /etc/rc.d/S99porch ]; then
	mkdir -p /etc/rc.d 2>/dev/null || true
	ln -sf ../init.d/porch /etc/rc.d/S99porch 2>/dev/null || true
	ln -sf ../init.d/porch /etc/rc.d/K15porch 2>/dev/null || true
fi

exit 0
EOS
	cp "$stg/scripts/post-install" "$stg/scripts/post-upgrade"

	# 只在真正删除时跑（apk 升级不会跑旧的 pre-deinstall），避免升级把服务停掉
	cat >"$stg/scripts/pre-deinstall" <<'EOS'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/porch stop >/dev/null 2>&1 || true
/etc/init.d/porch disable >/dev/null 2>&1 || true
# rc.d 链接是 post-install 建的、不在包文件清单里，卸载时得自己收干净
rm -f /etc/rc.d/S99porch /etc/rc.d/K15porch 2>/dev/null || true
exit 0
EOS
	chmod 755 "$stg"/scripts/*

	mkdir -p "$(dirname "$out")"
	pack "$stg" porch "$ver" "$arch" "" \
		"Porch - DDNS / ACME certificate / reverse proxy. 动态域名解析、ACME 证书自动签发续期、按域名与路径的 HTTP 反向代理，自带浅色 Web 管理界面（16601 端口）。" \
		"$out" \
		"post-install:$stg/scripts/post-install" \
		"post-upgrade:$stg/scripts/post-upgrade" \
		"pre-deinstall:$stg/scripts/pre-deinstall"

	[ "$STAGE_ONLY" = 1 ] || rm -rf "$stg"
}

# --------------------------------------------------------------------------- #
# LuCI 界面包（纯脚本，noarch）
# --------------------------------------------------------------------------- #
build_luci() {
	version=""; out=""; src=""
	while [ $# -gt 0 ]; do
		case "$1" in
			--version) version=$2; shift 2 ;;
			--out)     out=$2; shift 2 ;;
			--src)     src=$2; shift 2 ;;
			*) echo "未知参数: $1" >&2; usage ;;
		esac
	done
	[ -n "$version" ] && [ -n "$out" ] || usage
	[ -n "$src" ] || src="$ROOT/openwrt/luci-app-porch"
	[ -d "$src" ] || { echo "找不到 LuCI 源码目录: $src" >&2; exit 1; }

	require_tools
	ver=$(apk_version "$version")

	stg=$(mktemp -d 2>/dev/null || echo "/tmp/porch-apk-luci.$$")
	mkdir -p "$stg/scripts" "$stg/files"
	d="$stg/files"
	# htdocs/ → /www，root/ → /（与 OpenWrt 的安装根约定一致）
	for rel in \
		"htdocs/luci-static/resources/view/porch/settings.js:www/luci-static/resources/view/porch/settings.js:644" \
		"htdocs/luci-static/resources/view/porch/status.js:www/luci-static/resources/view/porch/status.js:644" \
		"root/usr/share/luci/menu.d/luci-app-porch.json:usr/share/luci/menu.d/luci-app-porch.json:644" \
		"root/usr/share/rpcd/acl.d/luci-app-porch.json:usr/share/rpcd/acl.d/luci-app-porch.json:644" \
		"root/usr/libexec/rpcd/porch:usr/libexec/rpcd/porch:755" \
	; do
		s=${rel%%:*}; rest=${rel#*:}; t=${rest%%:*}; mode=${rest##*:}
		[ -f "$src/$s" ] || { echo "缺少文件: $src/$s" >&2; exit 1; }
		mkdir -p "$d/$(dirname "$t")"
		cp "$src/$s" "$d/$t"
		chmod "$mode" "$d/$t"
	done
	find "$d" -type d -exec chmod 755 {} + 2>/dev/null || true

	write_pkg_meta "$stg" luci-app-porch

	cat >"$stg/scripts/post-install" <<'EOS'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
rm -f /tmp/luci-indexcache* 2>/dev/null || true
/etc/init.d/rpcd reload 2>/dev/null || true
exit 0
EOS
	cp "$stg/scripts/post-install" "$stg/scripts/post-upgrade"
	cat >"$stg/scripts/post-deinstall" <<'EOS'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
rm -f /tmp/luci-indexcache* 2>/dev/null || true
exit 0
EOS
	chmod 755 "$stg"/scripts/*

	mkdir -p "$(dirname "$out")"
	pack "$stg" luci-app-porch "$ver" noarch "luci-base porch rpcd" \
		"LuCI 界面 - Porch（动态域名 / SSL 证书 / 反向代理）：状态查看、启停服务、修改配置目录与端口。" \
		"$out" \
		"post-install:$stg/scripts/post-install" \
		"post-upgrade:$stg/scripts/post-upgrade" \
		"post-deinstall:$stg/scripts/post-deinstall"

	[ "$STAGE_ONLY" = 1 ] || rm -rf "$stg"
}

case "${1:-}" in
	porch) shift; build_porch "$@" ;;
	luci)  shift; build_luci "$@" ;;
	*) usage ;;
esac
