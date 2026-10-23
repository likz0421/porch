#!/usr/bin/env python3
"""把预编译二进制打成一个标准 OpenWrt .ipk（不依赖 OpenWrt SDK）。

ipk 的结构就是一个 gzip 的 tar，里面装三段：

    ./debian-binary    文本 "2.0\\n"
    ./control.tar.gz   ./control、./postinst、./prerm、./conffiles
    ./data.tar.gz      真正的文件树（./usr/bin/porch、./etc/init.d/porch ...）

用 stdlib 生成而不走 `ar`，是为了在两个平台上都能跑（Windows 的 Git Bash 没有 ar），
并且能固定 mtime/uid/gid，同一个输入永远得到同一个 ipk（可复现构建）。

用法:
  python3 make_ipk.py porch --version 1.1.0 --binary <porch 二进制> \\
      --init openwrt/porch/files/porch.init --uci openwrt/porch/files/porch.uci \\
      --out dist/porch_1.1.0_openwrt_x86_64.ipk

  python3 make_ipk.py luci --version 1.1.0 --src openwrt/luci-app-porch \\
      --out dist/luci-app-porch_1.1.0_all.ipk
"""

import argparse
import gzip
import io
import os
import sys
import tarfile

EPOCH = 0  # 固定时间戳，保证可复现


# --------------------------------------------------------------------------- #
# 基础工具
# --------------------------------------------------------------------------- #
def _tar_add(tf, name, data, mode):
    """往 tar 里加一个普通文件（名字统一以 ./ 开头，和 OpenWrt 打包结果一致）"""
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = EPOCH
    info.uid = info.gid = 0
    info.uname = info.gname = "root"
    tf.addfile(info, io.BytesIO(data))


def _tar_add_dir(tf, name, mode=0o755):
    info = tarfile.TarInfo(name)
    info.type = tarfile.DIRTYPE
    info.mode = mode
    info.mtime = EPOCH
    info.uid = info.gid = 0
    info.uname = info.gname = "root"
    tf.addfile(info)


def _tgz(entries, dirs=()):
    """entries: [(name, data, mode)] -> gzip 压缩的 tar 字节串"""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.GNU_FORMAT) as tf:
        _tar_add_dir(tf, "./")
        for d in dirs:
            _tar_add_dir(tf, d)
        for name, data, mode in entries:
            _tar_add(tf, name, data, mode)
    raw = buf.getvalue()
    out = io.BytesIO()
    with gzip.GzipFile(fileobj=out, mode="wb", mtime=EPOCH, compresslevel=9) as gz:
        gz.write(raw)
    return out.getvalue()


def _read(path):
    with open(path, "rb") as f:
        return f.read()


def _installed_kb(entries):
    total = sum(len(d) for _, d, _ in entries)
    return max(1, (total + 1023) // 1024)


def write_ipk(out_path, control_text, control_scripts, data_entries, data_dirs):
    """control_scripts: {"postinst": b"...", "prerm": b"..."}"""
    ctl_entries = [("./control", control_text.encode("utf-8"), 0o644)]
    for name, body in control_scripts.items():
        ctl_entries.append(("./" + name, body, 0o755))

    control_tgz = _tgz(ctl_entries)
    data_tgz = _tgz(data_entries, data_dirs)

    os.makedirs(os.path.dirname(os.path.abspath(out_path)) or ".", exist_ok=True)
    with open(out_path, "wb") as f:
        with tarfile.open(fileobj=f, mode="w", format=tarfile.GNU_FORMAT) as tf:
            _tar_add(tf, "./debian-binary", b"2.0\n", 0o644)
            _tar_add(tf, "./control.tar.gz", control_tgz, 0o644)
            _tar_add(tf, "./data.tar.gz", data_tgz, 0o644)
    return os.path.getsize(out_path)


# --------------------------------------------------------------------------- #
# postinst / prerm：沿用 OpenWrt 的 default_* 钩子，让 init 脚本自动 enable
# --------------------------------------------------------------------------- #
POSTINST = """#!/bin/sh
[ "${IPKG_NO_SCRIPT}" = "1" ] && exit 0
[ -s ${IPKG_INSTROOT}/lib/functions.sh ] || exit 0
. ${IPKG_INSTROOT}/lib/functions.sh

# 架构自检：control 里写 Architecture: all 是为了兼容 OpenWrt 各子目标
# （aarch64_cortex-a53 / arm_cortex-a7_neon-vfpv4 这类 arch 名各不相同，
#  冒然写死会让 opkg 在多数机型上拒装，上游 Lucky 也是 all 的写法）。
# 代价是装错架构时 opkg 不会拦，用户只会看到 "Exec format error" 或
# "Illegal instruction"（mips 硬浮点/软浮点装反的典型症状）。
# 这里补一道运行期校验：装完立刻试跑一次，装错就大声说出来。
if [ -z "${IPKG_INSTROOT}" ] && [ -x /usr/bin/porch ]; then
	if /usr/bin/porch -v >/dev/null 2>&1; then
		echo "porch: $(/usr/bin/porch -v 2>/dev/null | head -n1)"
	else
		echo "porch: [警告] /usr/bin/porch 无法在本机执行，很可能装错了架构包。" >&2
		echo "porch: 本机 uname -m = $(uname -m)；请到 Releases 页按架构名重新下载：" >&2
		echo "porch:   https://github.com/likz0421/porch/releases/latest" >&2
		echo "porch: MIPS 机型若报 Illegal instruction，请换 softfloat 版本。" >&2
	fi
fi

default_postinst $0 $@

# 兜底开机自启：不同 OpenWrt 版本 default_postinst / rc.common 建 rc.d 链接的
# 行为不完全一致，这里无条件确认一次（等价于 /etc/init.d/porch enable）。
if [ -z "${IPKG_INSTROOT}" ] && [ -f /etc/init.d/porch ] && [ ! -L /etc/rc.d/S99porch ]; then
	mkdir -p /etc/rc.d 2>/dev/null || true
	ln -sf ../init.d/porch /etc/rc.d/S99porch 2>/dev/null || true
	ln -sf ../init.d/porch /etc/rc.d/K15porch 2>/dev/null || true
fi
exit 0
"""

PRERM = """#!/bin/sh
[ -s ${IPKG_INSTROOT}/lib/functions.sh ] || exit 0
. ${IPKG_INSTROOT}/lib/functions.sh
default_prerm $0 $@
"""


# --------------------------------------------------------------------------- #
# porch 主包
# --------------------------------------------------------------------------- #
def build_porch(args):
    binary = _read(args.binary)
    init = _read(args.init)
    uci = _read(args.uci)

    entries = [
        ("./usr/bin/porch", binary, 0o755),
        ("./etc/init.d/porch", init, 0o755),
        ("./etc/config/porch", uci, 0o644),
    ]
    dirs = [
        "./usr/",
        "./usr/bin/",
        "./etc/",
        "./etc/init.d/",
        "./etc/config/",
        "./etc/porch/",
    ]

    control = "\n".join(
        [
            "Package: porch",
            "Version: %s-1" % args.version,
            # 不声明 libc：二进制是纯静态链接（CGO_ENABLED=0），不依赖目标系统任何库
            "Depends: ",
            "Section: net",
            "Priority: optional",
            "Maintainer: %s" % args.maintainer,
            "Architecture: all",
            "Installed-Size: %d" % _installed_kb(entries),
            "License: MIT",
            "Description: Porch - DDNS / HTTPS certificate / reverse proxy",
            " 动态域名解析、ACME 证书自动签发续期、按域名与路径的 HTTP 反向代理，",
            " 自带浅色毛玻璃 Web 管理界面（16601 端口）。",
            "",
        ]
    )
    size = write_ipk(
        args.out,
        control,
        {"postinst": POSTINST.encode("utf-8"), "prerm": PRERM.encode("utf-8")},
        entries,
        dirs,
    )
    print("ipk: %s (%d KB)" % (args.out, size // 1024))


# --------------------------------------------------------------------------- #
# LuCI 界面包（纯脚本，Architecture: all）
# --------------------------------------------------------------------------- #
def build_luci(args):
    src = args.src
    entries = []
    for rel, mode in [
        ("htdocs/luci-static/resources/view/porch/settings.js", 0o644),
        ("htdocs/luci-static/resources/view/porch/status.js", 0o644),
        ("root/usr/share/luci/menu.d/luci-app-porch.json", 0o644),
        ("root/usr/share/rpcd/acl.d/luci-app-porch.json", 0o644),
        ("root/usr/libexec/rpcd/porch", 0o755),
    ]:
        p = os.path.join(src, rel)
        if not os.path.exists(p):
            sys.exit("缺少文件: %s" % p)
        # htdocs/ 与 root/ 是 OpenWrt 的"安装根"，打进包里要去掉这一层
        name = "./" + rel.replace("htdocs/", "www/", 1) if rel.startswith("htdocs/") else "./" + rel[len("root/"):]
        entries.append((name, _read(p), mode))

    dirs = [
        "./www/",
        "./www/luci-static/",
        "./www/luci-static/resources/",
        "./www/luci-static/resources/view/",
        "./www/luci-static/resources/view/porch/",
        "./usr/",
        "./usr/share/",
        "./usr/share/luci/",
        "./usr/share/luci/menu.d/",
        "./usr/share/rpcd/",
        "./usr/share/rpcd/acl.d/",
        "./usr/libexec/",
        "./usr/libexec/rpcd/",
    ]

    control = "\n".join(
        [
            "Package: luci-app-porch",
            "Version: %s-1" % args.version,
            "Depends: luci-base, porch, rpcd",
            "Section: luci",
            "Priority: optional",
            "Maintainer: %s" % args.maintainer,
            "Architecture: all",
            "Installed-Size: %d" % _installed_kb(entries),
            "License: MIT",
            "Description: LuCI 界面 - Porch（动态域名 / SSL 证书 / 反向代理）",
            " 在 LuCI 的「服务」菜单下提供状态查看、启停服务、修改配置目录与端口。",
            "",
        ]
    )
    size = write_ipk(
        args.out,
        control,
        {"postinst": POSTINST.encode("utf-8"), "prerm": PRERM.encode("utf-8")},
        entries,
        dirs,
    )
    print("ipk: %s (%d KB)" % (args.out, size // 1024))


def main():
    ap = argparse.ArgumentParser(description="把预编译二进制打成 OpenWrt ipk")
    sub = ap.add_subparsers(dest="kind", required=True)

    p1 = sub.add_parser("porch", help="核心服务包")
    p1.add_argument("--version", required=True)
    p1.add_argument("--binary", required=True)
    p1.add_argument("--init", required=True)
    p1.add_argument("--uci", required=True)
    p1.add_argument("--out", required=True)
    p1.add_argument("--maintainer", default="porch <porch@users.noreply.github.com>")
    p1.set_defaults(func=build_porch)

    p2 = sub.add_parser("luci", help="LuCI 界面包")
    p2.add_argument("--version", required=True)
    p2.add_argument("--src", required=True)
    p2.add_argument("--out", required=True)
    p2.add_argument("--maintainer", default="porch <porch@users.noreply.github.com>")
    p2.set_defaults(func=build_luci)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
