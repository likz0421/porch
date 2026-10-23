#!/bin/sh
# 取一个静态链接的 apk-tools 3 可执行文件（只为了用它的 `apk mkpkg`）。
#
# 为什么要它：OpenWrt 25.12 起包管理器换成 apk，包格式也换成了 apk-tools v3 的
# ADB 二进制容器（magic "ADB"），不再是 ipk 那种 gz 过的 tar。这种格式没法用
# Python 手搓（字段全靠 apk-tools 的实现细节），官方做法就是调 `apk mkpkg`
# ——OpenWrt 自己的 include/package-pack.mk 也是这么做的。
#
# 为什么不从源码编：apk-tools 的源码在 gitlab.alpinelinux.org，有反爬，
# GitHub Actions 里 clone 不稳。Alpine 官方提供了 apk-tools-static（静态编译版），
# 直接下、校验 sha256 就用。
#
# 用法:
#   sh scripts/install-apk-tools.sh <目标目录>        # 装到 <目标目录>/apk
#
# 环境变量:
#   APK_TOOLS_MIRROR   覆盖下载源（默认 dl-cdn，失败自动换国内镜像）
set -eu

# Alpine CDN 只保留每个包的当前构建；这个 pin 过期会明确报错，不会静默装错版本。
# 升级方式：去 https://dl-cdn.alpinelinux.org/alpine/<branch>/main/<arch>/ 找新版，
# 改下面 4 个变量，再用 sha256sum 刷新两个校验值。
APK_TOOLS_VERSION="3.0.8-r0"
ALPINE_BRANCH="v3.24"
SHA256_X86_64="c8e2c88c13ba12a12269b79a3543e1190ff8c0ab0beb32b58cadfd5881c619e3"
SHA256_AARCH64="7e86f8258f3a97ea1c279105950ebf6c9af960891eb499f731b7e5f0af26ff63"

DEST=${1:-}
[ -n "$DEST" ] || { echo "用法: sh $0 <目标目录>" >&2; exit 2; }

case "$(uname -m)" in
	x86_64|amd64)   ARCH=x86_64;  WANT=$SHA256_X86_64 ;;
	aarch64|arm64)  ARCH=aarch64; WANT=$SHA256_AARCH64 ;;
	*) echo "不支持的宿主机架构: $(uname -m)（只能编 apk 包用的静态工具在 x86_64/aarch64 上跑）" >&2; exit 1 ;;
esac

# 已经有正确版本就跳过（CI 里可以把目录缓存起来）
if [ -x "$DEST/apk" ] && "$DEST/apk" --version 2>/dev/null | grep -q "^apk-tools ${APK_TOOLS_VERSION},"; then
	echo "apk-tools ${APK_TOOLS_VERSION} 已就绪: $DEST/apk"
	exit 0
fi

PKG="$DEST/apk-tools-static-${APK_TOOLS_VERSION}.${ARCH}.apk"
NAME="apk-tools-static-${APK_TOOLS_VERSION}.apk"
mkdir -p "$DEST"

# 默认官方 CDN，失败按顺序换镜像（国内网络/CI 都可能只通其中一个）
if [ -n "${APK_TOOLS_MIRROR:-}" ]; then
	BASES=$APK_TOOLS_MIRROR
else
	BASES="https://dl-cdn.alpinelinux.org/alpine,https://mirrors.tuna.tsinghua.edu.cn/alpine,https://mirrors.aliyun.com/alpine"
fi

have() { command -v "$1" >/dev/null 2>&1; }

_dl() { # url dest
	if have curl; then
		curl -fsSL --retry 2 --connect-timeout 15 -m 300 -o "$2" "$1"
	elif have wget; then
		wget -q -T 60 -O "$2" "$1"
	else
		return 1
	fi
}

ok=0
_ifs=$IFS; IFS=,
for b in $BASES; do
	IFS=$_ifs
	url="${b%/}/${ALPINE_BRANCH}/main/${ARCH}/${NAME}"
	echo "下载 $NAME  ← $b"
	if _dl "$url" "$PKG"; then ok=1; break; fi
	echo "  ! $(echo "$b" | sed -e 's|^[a-z]*://||' -e 's|/.*||') 失败"
	IFS=,
done
IFS=$_ifs
[ "$ok" = 1 ] || { echo "所有镜像都没下到 $NAME" >&2; exit 1; }

if have sha256sum; then got=$(sha256sum "$PKG" | cut -d' ' -f1)
else got=$(shasum -a 256 "$PKG" | cut -d' ' -f1); fi
if [ "$got" != "$WANT" ]; then
	echo "sha256 不匹配，删掉重来：" >&2
	echo "  期望 $WANT" >&2
	echo "  实际 $got" >&2
	rm -f "$PKG"
	exit 1
fi
echo "校验通过 ${WANT%"${WANT#????????}"}…"

# apk-tools-static 本身还是 apk v2 格式（gz 过的 tar，第一段是签名，tar 会跳过）。
# GNU tar 会对 apk 的扩展头告警，无害；这里用 2>/dev/null 静音。
tar -xzf "$PKG" -C "$DEST" sbin/apk.static 2>/dev/null || tar -xzf "$PKG" -C "$DEST"
if [ -f "$DEST/sbin/apk.static" ]; then
	mv -f "$DEST/sbin/apk.static" "$DEST/apk"
	rmdir "$DEST/sbin" 2>/dev/null || true
fi
rm -f "$PKG"
chmod 755 "$DEST/apk" 2>/dev/null || true

"$DEST/apk" --version || { echo "解出来的不是可执行的 apk" >&2; exit 1; }
echo "ok: $DEST/apk"
