#!/bin/sh
# 用 dist/ 里已编好的 tar.gz，批量打出 OpenWrt 25.12+ 的 .apk 包。
#
# 为什么单独一个脚本：apk 的 mkpkg 要求以 root 运行（它记录磁盘上的属主），
# 而交叉编译和 ipk 打包都不需要 root。把需要提权的部分收在这里，CI 里就是
# 一句 `sudo -E sh scripts/build-apks.sh <ver>`。
#
# 前置：sh scripts/install-apk-tools.sh <dir>（拿静态 apk-tools 3），
#       然后 APK_BIN=<dir>/apk sudo -E sh scripts/build-apks.sh 1.1.0
#
# 用法: sh scripts/build-apks.sh [版本] [dist 目录]
set -eu

VERSION=${1:-1.1.0}
OUT=${2:-dist}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

# 与 ipk 保持同一份架构清单：OpenWrt 上跑的仍是 Linux 二进制，Go 的 GOARCH 不变
ARCHS="x86_64 i386 arm64 armv7 armv6 armv5 mips_hardfloat mips_softfloat mipsle_hardfloat mipsle_softfloat riscv64"

APK=${APK_BIN:-apk}
if ! command -v "$APK" >/dev/null 2>&1; then
	echo "build-apks.sh: 找不到 apk（先跑 sh scripts/install-apk-tools.sh <dir> 并导出 APK_BIN）" >&2
	exit 1
fi
case "$("$APK" --version 2>/dev/null)" in
	"apk-tools "[3-9].*) ;;
	*) echo "build-apks.sh: $APK 不是 apk-tools >= 3.0（mkpkg 是新加的 applet）" >&2; exit 1 ;;
esac

TMP=$(mktemp -d 2>/dev/null || echo "/tmp/porch-apks.$$")
mkdir -p "$TMP"
trap 'rm -rf "$TMP"' EXIT

n=0
failed=""
for arch in $ARCHS; do
	tgz="$OUT/porch_${VERSION}_linux_${arch}.tar.gz"
	if [ ! -f "$tgz" ]; then
		echo "  跳过 $arch（缺 $(basename "$tgz")）"
		failed="$failed $arch"
		continue
	fi
	tar xzf "$tgz" -C "$TMP"
	bin="$TMP/porch_${VERSION}_linux_${arch}/porch"
	if [ ! -f "$bin" ]; then
		echo "  ! $arch 的 tar.gz 里没有 porch 二进制" >&2
		failed="$failed $arch"
		continue
	fi
	chmod 755 "$bin"
	if APK_BIN="$APK" sh scripts/make_apk.sh porch \
		--binary "$bin" \
		--version "$VERSION" \
		--out "$OUT/porch_${VERSION}_openwrt_${arch}.apk"; then
		n=$((n + 1))
	else
		echo "  ! $arch 打包失败" >&2
		failed="$failed $arch"
	fi
done

if APK_BIN="$APK" sh scripts/make_apk.sh luci \
	--version "$VERSION" \
	--out "$OUT/luci-app-porch_${VERSION}_all.apk"; then
	n=$((n + 1))
else
	failed="$failed luci"
fi

echo
echo "共生成 ${n} 个 .apk"
[ -z "$failed" ] || { echo "有问题:${failed}" >&2; exit 1; }
