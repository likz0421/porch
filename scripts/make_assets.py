#!/usr/bin/env python3
"""把 build.sh 产出的 tar.gz 组装成完整发布资产。

输入：dist/porch_<ver>_*.tar.gz（由 build.sh 生成）
输出：
  · porch_<ver>_windows_<arch>.zip   — Windows 免安装包（exe + 启动脚本 + 说明）
  · porch_<ver>_openwrt_<arch>.ipk   — OpenWrt 包（共 11 个架构）
  · luci-app-porch_<ver>_all.ipk     — LuCI 界面包
  · SHA256SUMS                       — 全部资产的校验和

只依赖 stdlib，Windows/Linux/macOS 都能跑（CI 里也是这一步）。
"""

import argparse
import hashlib
import io
import os
import sys
import tarfile
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import make_ipk  # noqa: E402

# 需要打 ipk 的架构（与 Lucky 的 OpenWrt 包范围一致，另加 riscv64）
IPK_ARCHS = [
    "x86_64",
    "i386",
    "arm64",
    "armv7",
    "armv6",
    "armv5",
    "mips_hardfloat",
    "mips_softfloat",
    "mipsle_hardfloat",
    "mipsle_softfloat",
    "riscv64",
]

WIN_ARCHS = ["x86_64", "i386"]
WIN_EXTRA = ["start.bat", "autostart-install.bat", "autostart-remove.bat", "使用说明.txt"]


def read_member(tgz_path, member_hint=None, member_endswith=None):
    """从 tar.gz 里取一个文件的内容（按完整名或后缀匹配）"""
    with tarfile.open(tgz_path, "r:gz") as tf:
        for m in tf.getmembers():
            if not m.isfile():
                continue
            if member_endswith and m.name.endswith(member_endswith):
                return tf.extractfile(m).read()
            if m.name == member_hint:
                return tf.extractfile(m).read()
    raise SystemExit("在 %s 里没找到 %s" % (tgz_path, member_hint or member_endswith))


def build_windows_zip(version, dist, root, arch):
    tgz = os.path.join(dist, "porch_%s_windows_%s.tar.gz" % (version, arch))
    if not os.path.exists(tgz):
        print("  跳过 windows/%s（缺 %s）" % (arch, os.path.basename(tgz)))
        return None
    exe = read_member(tgz, member_endswith="/porch.exe")

    folder = "porch_windows_%s" % arch
    out = os.path.join(dist, "porch_%s_windows_%s.zip" % (version, arch))
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        info = zipfile.ZipInfo(folder + "/porch.exe", date_time=(2020, 1, 1, 0, 0, 0))
        info.external_attr = 0o755 << 16
        info.compress_type = zipfile.ZIP_DEFLATED   # ZipInfo 默认是 STORED，不设就会白给 6MB
        z.writestr(info, exe)
        for name in WIN_EXTRA:
            src = os.path.join(root, "packaging", "windows", name)
            if not os.path.exists(src):
                print("  ! 缺少模板 %s" % src)
                continue
            zi = zipfile.ZipInfo(folder + "/" + name, date_time=(2020, 1, 1, 0, 0, 0))
            zi.external_attr = 0o644 << 16
            zi.compress_type = zipfile.ZIP_DEFLATED
            with open(src, "rb") as f:
                z.writestr(zi, f.read())
    return out


def build_openwrt_ipk(version, dist, root, arch):
    tgz = os.path.join(dist, "porch_%s_linux_%s.tar.gz" % (version, arch))
    if not os.path.exists(tgz):
        print("  跳过 openwrt/%s（缺 %s）" % (arch, os.path.basename(tgz)))
        return None
    binary = read_member(tgz, member_endswith="/porch")

    class A:
        pass

    a = A()
    a.version = version
    a.binary = os.path.join(dist, ".tmp-porch-%s" % arch)
    with open(a.binary, "wb") as f:
        f.write(binary)
    a.init = os.path.join(root, "openwrt", "porch", "files", "porch.init")
    a.uci = os.path.join(root, "openwrt", "porch", "files", "porch.uci")
    a.out = os.path.join(dist, "porch_%s_openwrt_%s.ipk" % (version, arch))
    a.maintainer = "porch <porch@users.noreply.github.com>"
    try:
        make_ipk.build_porch(a)
    finally:
        os.remove(a.binary)
    return a.out


def build_luci_ipk(version, dist, root):
    class A:
        pass

    a = A()
    a.version = version
    a.src = os.path.join(root, "openwrt", "luci-app-porch")
    a.out = os.path.join(dist, "luci-app-porch_%s_all.ipk" % version)
    a.maintainer = "porch <porch@users.noreply.github.com>"
    make_ipk.build_luci(a)
    return a.out


def write_sums(dist):
    names = sorted(
        n
        for n in os.listdir(dist)
        if n.endswith((".tar.gz", ".zip", ".ipk"))
    )
    lines = []
    for n in names:
        h = hashlib.sha256()
        with open(os.path.join(dist, n), "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
        lines.append("%s  %s" % (h.hexdigest(), n))
    out = os.path.join(dist, "SHA256SUMS")
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines) + "\n")
    return out, len(lines)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True)
    ap.add_argument("--dist", default="dist")
    ap.add_argument("--root", default=".")
    args = ap.parse_args()

    root = os.path.abspath(args.root)
    dist = os.path.abspath(args.dist)
    os.makedirs(dist, exist_ok=True)

    print("==> Windows 免安装包")
    for arch in WIN_ARCHS:
        build_windows_zip(args.version, dist, root, arch)

    print("==> OpenWrt ipk")
    for arch in IPK_ARCHS:
        build_openwrt_ipk(args.version, dist, root, arch)
    build_luci_ipk(args.version, dist, root)

    print("==> SHA256SUMS")
    path, n = write_sums(dist)
    print("   %s（%d 个资产）" % (path, n))


if __name__ == "__main__":
    main()
