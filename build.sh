#!/bin/sh
# Porch 交叉编译脚本：按发布矩阵一次产出全部平台
#
# 用法:
#   sh build.sh                 # 版本取 1.1.2，产物写入 dist/
#   sh build.sh 1.2.0           # 指定版本
#   sh build.sh 1.2.0 out       # 指定版本与输出目录
#   ONLY=linux sh build.sh      # 只编某个 OS（调试用）
#
# 矩阵与上游 Lucky 的发布范围对齐（18 个平台），另加 mips64le：
#   Linux   : x86_64 i386 arm64 armv7 armv6 armv5 mips/mipsle ×(hard|soft)float riscv64
#   Darwin  : arm64 x86_64
#   FreeBSD : x86_64 i386 arm64
#   Windows : x86_64 i386
# 产物命名: porch_<版本>_<os>_<后缀>.tar.gz，包内是 porch（Windows 为 porch.exe）+ README.md
set -eu

VERSION=${1:-1.1.2}
OUT=${2:-dist}
ONLY=${ONLY:-}
LDFLAGS="-s -w -X main.version=$VERSION -X main.build=release"

# os      arch      后缀                 额外环境变量
MATRIX="
linux   amd64     x86_64
linux   386       i386
linux   arm64     arm64
linux   arm       armv7                GOARM=7
linux   arm       armv6                GOARM=6
linux   arm       armv5                GOARM=5
linux   mips      mips_hardfloat       GOMIPS=hardfloat
linux   mips      mips_softfloat       GOMIPS=softfloat
linux   mipsle    mipsle_hardfloat     GOMIPS=hardfloat
linux   mipsle    mipsle_softfloat     GOMIPS=softfloat
linux   mips64le  mips64le_softfloat   GOMIPS64=softfloat
linux   riscv64   riscv64
darwin  arm64     arm64
darwin  amd64     x86_64
freebsd amd64     x86_64
freebsd 386       i386
freebsd arm64     arm64
windows amd64     x86_64
windows 386       i386
"

mkdir -p "$OUT"

build_one() {
	goos=$1
	goarch=$2
	suffix=$3
	shift 3

	name="porch_${VERSION}_${goos}_${suffix}"
	bin=porch
	[ "$goos" = windows ] && bin=porch.exe   # Go 的 -o 不会自动补 .exe

	printf '==> %-42s' "$name"
	# 额外变量（GOARM / GOMIPS / GOMIPS64）必须经 env 传入；
	# 直接写成 "GOARM=7" 会被 shell 当成命令执行。
	GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 env "$@" \
		go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name/$bin" . || {
		echo "失败"
		return 1
	}
	cp -f README.md "$OUT/$name/" 2>/dev/null || true
	# -n: 不写入本机 uid/gid，保证在别的机器上解包不会出现奇怪属主
	tar czf "$OUT/${name}.tar.gz" -C "$OUT" "$name"
	rm -rf "$OUT/$name"
	echo "ok  $(du -h "$OUT/${name}.tar.gz" | cut -f1)"
}

count=0
failed=""
# 用 here-doc 而不是 echo | while：管道会把循环放进子 shell，失败计数就丢了
while read -r goos goarch suffix rest; do
	[ -z "${goos:-}" ] && continue
	[ -n "$ONLY" ] && [ "$ONLY" != "$goos" ] && continue
	# shellcheck disable=SC2086
	if build_one "$goos" "$goarch" "$suffix" $rest; then
		count=$((count + 1))
	else
		failed="$failed $goos/$suffix"
	fi
done <<EOF
$MATRIX
EOF

echo
echo "==> 成功 $count 个目标，产物位于 $OUT"
ls -1 "$OUT"/*.tar.gz 2>/dev/null | sed 's|^|    |'
if [ -n "$failed" ]; then
	echo "!! 失败目标:$failed"
	exit 1
fi
exit 0
