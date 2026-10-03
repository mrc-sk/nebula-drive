"""校验 release/ 下三平台发布包：二进制平台、zip CRC、权限位、行尾、内嵌前端是否为新版。"""
import os
import stat
import zipfile

REL = os.path.join(os.path.dirname(os.path.abspath(__file__)), "release")
VERSION = "V26-10.0-b"
PLATFORMS = ["windows-amd64", "linux-amd64", "darwin-amd64"]

MAGIC = {
    b"MZ": "PE/Windows",
    b"\x7fELF": "ELF/Linux",
    b"\xcf\xfa\xed\xfe": "Mach-O/macOS",
}
EXPECT_MAGIC = {
    "windows-amd64": b"MZ",
    "linux-amd64": b"\x7fELF",
    "darwin-amd64": b"\xcf\xfa\xed\xfe",
}

fail = 0


def check(cond, msg):
    global fail
    print(("  [OK]   " if cond else "  [FAIL] ") + msg)
    if not cond:
        fail += 1
    return cond


for plat in PLATFORMS:
    pkg = f"NebulaDrive-{VERSION}-{plat}"
    zpath = os.path.join(REL, pkg + ".zip")
    ddir = os.path.join(REL, pkg)
    print(f"\n=== {pkg} ===")

    if not check(os.path.isfile(zpath), "zip 存在"):
        continue
    with zipfile.ZipFile(zpath) as z:
        check(z.testzip() is None, "zip CRC 全部通过")
        names = z.namelist()
        print("         共 %d 项: %s" % (len(names), ", ".join(sorted(names))))

        prefix = pkg + "/"
        check(all(n.startswith(prefix) for n in names), "所有条目都在单一顶级目录下")

        # 1. 二进制平台
        want_name = "nebula.exe" if plat == "windows-amd64" else "nebula"
        exe = prefix + want_name
        if not check(exe in names, "含 " + want_name):
            continue
        head = z.read(exe)[:4]
        want = EXPECT_MAGIC[plat]
        check(head.startswith(want),
              "二进制平台正确: %s (期望 %s, 头 %s)" % (MAGIC.get(want), MAGIC.get(want), head.hex()))

        # 2. 内嵌前端必须是本轮重打包后的版本
        blob = z.read(exe)
        for chunk in ("ShareList-", "TasksOffline-", "Trash-"):
            check(chunk.encode() in blob, "内嵌前端含新chunk " + chunk + "*")
        check(b"AGPL-3.0" in blob, "内嵌前端已是 AGPL-3.0 文案")
        check(b"MIT License" not in blob, "内嵌前端无 MIT License 残留")

        # 3. 权限位：0755 给二进制与 .sh，其余 0644
        for info in z.infolist():
            mode = stat.S_IMODE(info.external_attr >> 16)
            base = info.filename.rsplit("/", 1)[-1]
            want_mode = 0o755 if (base == want_name or base.endswith(".sh")) else 0o644
            check(mode == want_mode, "权限 %s -> %s: %s" % (oct(mode), oct(want_mode), base))

        # 4. 行尾 / 文件类型
        for info in z.infolist():
            base = info.filename.rsplit("/", 1)[-1]
            data = z.read(info.filename)
            if base.endswith(".sh"):
                check(b"\r\n" not in data, base + " 为 LF（无 CRLF）")
                # shebang 是 b"#!/"（3 字节）。用 startswith 而不是 data[:2] == b"#!/"，
                # 后者拿 2 字节比 3 字节恒为 False。
                check(data.startswith(b"#!"), "%s shebang 正常 (%r)" % (base, data[:12]))
            elif base.endswith(".bat"):
                check(b"\r\n" in data, base + " 为 CRLF")
            elif base.endswith((".md", ".txt")) or base == "LICENSE":
                check(b"\r\n" not in data, base + " 为 LF（无 CRLF）")
            else:
                # 二进制不能查 CRLF：Go 产物里天然含 0d 0a 字节序列。
                # 只确认它确实是二进制。
                check(b"\x00" in data[:4096], base + " 确为二进制（含 NUL）")

        # 5. 必须带的文档
        for must in ("LICENSE", "README.md", "试用说明.md"):
            check(prefix + must in names, "含 " + must)

    # 6. 磁盘目录与 zip 一致
    check(os.path.isdir(ddir), "解压目录存在")
    with zipfile.ZipFile(zpath) as z:
        zipn = {n[len(prefix):] for n in z.namelist() if n != prefix}
    disk = set(os.listdir(ddir))
    check(disk == zipn, "目录内容与 zip 一致 (磁盘多: %s, zip 多: %s)" % (disk - zipn, zipn - disk))

print("\n" + "=" * 60)
print("全部通过" if fail == 0 else "有 %d 项未通过" % fail)
raise SystemExit(1 if fail else 0)
