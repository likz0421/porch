#!/usr/bin/env python3
"""校验 ipk 结构：外层裸 tar（ipk 规范），内层两段 gzip tar。

ipk 格式（Debian 系 ar 的 tar 变体）：
    ./debian-binary   文本 "2.0\n"
    ./control.tar.gz  控制脚本 + control 元数据
    ./data.tar.gz     实际文件树

用法：
    python scripts/verify_ipk.py dist/porch_1.1.1_openwrt_x86_64.ipk [...]
"""

import io
import sys
import tarfile

REQUIRED_MEMBERS = ("./debian-binary", "./control.tar.gz", "./data.tar.gz")


def _open_outer(path):
    """ipk 外层是未压缩 tar；兼容误用 gz 的情况。"""
    try:
        return tarfile.open(path, "r:")
    except tarfile.ReadError:
        return tarfile.open(path, "r:gz")


def _list(tf):
    return [(m.name, m.mode, m.size) for m in tf.getmembers() if m.isfile()]


def _member(tf, *candidates):
    """按候选名依次找第一个存在的成员，返回 (name, fileobj) 或 (None, None)。"""
    for name in candidates:
        try:
            return name, tf.extractfile(name)
        except KeyError:
            continue
    return None, None


def verify(path, expect_luci=False):
    print("=" * 74)
    print("FILE: %s" % path)
    ok = True

    with _open_outer(path) as outer:
        names = outer.getnames()
        print("外层成员: %s" % ", ".join(names))
        for req in REQUIRED_MEMBERS:
            if req not in names:
                # 兼容不带 ./ 前缀的写法
                alt = req.lstrip("./")
                if alt not in names:
                    print("  [FAIL] 缺少 %s" % req)
                    ok = False

        # ---- debian-binary ----
        _, m = _member(outer, "./debian-binary", "debian-binary")
        if m:
            body = m.read().decode("utf-8", "replace")
            print("debian-binary = %r" % body)
            if body.strip() != "2.0":
                print("  [FAIL] 应为 '2.0'")
                ok = False

        # ---- control.tar.gz ----
        _, cf = _member(outer, "./control.tar.gz", "control.tar.gz")
        if cf:
            with tarfile.open(fileobj=io.BytesIO(cf.read()), mode="r:gz") as ctf:
                members = _list(ctf)
                print("control 段: %s" % ", ".join(n for n, _, _ in members))
                _, control = _member(ctf, "./control", "control")
                if control:
                    text = control.read().decode("utf-8", "replace")
                    print("-" * 74)
                    print(text.rstrip())
                    print("-" * 74)
                    fields = dict(
                        ln.split(": ", 1) for ln in text.splitlines() if ": " in ln
                    )
                    for need in ("Package", "Version", "Architecture", "Description"):
                        if need not in fields:
                            print("  [FAIL] control 缺字段 %s" % need)
                            ok = False
                    if expect_luci:
                        deps = fields.get("Depends", "")
                        for d in ("luci-base", "porch", "rpcd"):
                            if d not in deps:
                                print("  [FAIL] luci 包 Depends 缺 %s" % d)
                                ok = False
                    if "Installed-Size" not in fields:
                        print("  [WARN] 无 Installed-Size（非致命）")
                # 脚本必须可执行
                for name, mode, _ in members:
                    if name.endswith(("postinst", "prerm", "postrm")):
                        perm = mode & 0o777
                        print("  %-28s mode=0o%03o" % (name, perm))
                        if not perm & 0o100:
                            print("  [FAIL] %s 缺少可执行位" % name)
                            ok = False
                if not any(n.endswith("postinst") for n, _, _ in members):
                    print("  [WARN] 无 postinst 脚本")

        # ---- data.tar.gz ----
        _, df = _member(outer, "./data.tar.gz", "data.tar.gz")
        if df:
            with tarfile.open(fileobj=io.BytesIO(df.read()), mode="r:gz") as dtf:
                members = _list(dtf)
                print("data 段文件树:")
                for name, mode, size in members:
                    print("  %-42s mode=0o%03o size=%d" % (name, mode & 0o777, size))
                bins = [n for n, _, _ in members if n.endswith("/bin/porch") or n == "./usr/bin/porch"]
                if not expect_luci:
                    if not bins:
                        print("  [FAIL] data 段没有 /usr/bin/porch")
                        ok = False
                    else:
                        for n, mode, size in members:
                            if n in bins:
                                if not (mode & 0o777) & 0o100:
                                    print("  [FAIL] %s 不可执行" % n)
                                    ok = False
                                if size < 1_000_000:
                                    print("  [FAIL] %s 体积异常 (%d)" % (n, size))
                                    ok = False
                    inits = [n for n, _, _ in members if "/init.d/" in n]
                    if not inits:
                        print("  [WARN] 无 init.d 脚本")
                else:
                    if not any(n.startswith(("./www/", "www/")) for n, _, _ in members):
                        print("  [FAIL] luci 包缺 www/ 资源")
                        ok = False

    print("RESULT: %s" % ("PASS" if ok else "FAIL"))
    return ok


def main(argv):
    targets = argv[1:]
    if not targets:
        print("用法: verify_ipk.py <a.ipk> [...]", file=sys.stderr)
        return 2
    results = [verify(t, expect_luci="luci-app-" in t) for t in targets]
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
