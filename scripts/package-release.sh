#!/bin/sh
# 生成一整套发布资产（CI 与本地都用这一个脚本）
#
# 用法:
#   sh scripts/package-release.sh                # 版本 1.1.0
#   sh scripts/package-release.sh 1.2.0          # 指定版本
#   SKIP_BUILD=1 sh scripts/package-release.sh   # 复用 dist/ 里已有的 tar.gz，只重新打包 ipk/zip
#
# 产物（全部落在 dist/）:
#   porch_<ver>_<os>_<arch>.tar.gz   19 个平台
#   porch_<ver>_windows_<arch>.zip   2 个免安装包
#   porch_<ver>_openwrt_<arch>.ipk   11 个 OpenWrt 包
#   luci-app-porch_<ver>_all.ipk     LuCI 界面
#   SHA256SUMS                       全部资产的校验和
set -eu

VERSION=${1:-1.1.0}
OUT=${2:-dist}
SKIP_BUILD=${SKIP_BUILD:-0}
ROOT=$(cd "$(dirname "$0")/.." && pwd)

cd "$ROOT"

# Python：make_assets / make_ipk 需要 3.7+
PY=""
for c in python3 python; do
	if command -v "$c" >/dev/null 2>&1; then
		if "$c" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 7) else 1)' 2>/dev/null; then
			PY=$c
			break
		fi
	fi
done
[ -n "$PY" ] || { echo "需要 Python 3.7+（用于组装 zip/ipk 与校验和）" >&2; exit 1; }

echo "==> 版本 ${VERSION}，输出目录 ${OUT}"
if [ "$SKIP_BUILD" = 0 ]; then
	sh build.sh "$VERSION" "$OUT"
else
	echo "    (SKIP_BUILD=1，跳过交叉编译)"
fi

echo
"$PY" scripts/make_assets.py --version "$VERSION" --dist "$OUT" --root .

echo
echo "==> 资产清单"
ls -1sh "$OUT"/*.tar.gz "$OUT"/*.zip "$OUT"/*.ipk "$OUT"/SHA256SUMS 2>/dev/null | sed 's|^|    |'
echo
echo "共 $(ls -1 "$OUT"/*.tar.gz "$OUT"/*.zip "$OUT"/*.ipk 2>/dev/null | wc -l | tr -d ' ') 个可下载资产 + SHA256SUMS"
echo "发布: gh release create v$VERSION $OUT/*.tar.gz $OUT/*.zip $OUT/*.ipk $OUT/SHA256SUMS --title \"Porch v$VERSION\""
