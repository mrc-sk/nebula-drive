"""校验 release/ 下三平台发布包：二进制平台、zip CRC、权限位、行尾、内嵌前端是否为新版。

用法：python packaging/verify_release.py（从任意工作目录运行都可以）
"""
import os
import stat
import zipfile

# 脚本在 packaging/ 下，release/ 是它的**上级**目录。
# 写成 os.path.join(dirname, "release") 会去找 packaging/release —— 不存在，
# 于是三个包全报"zip 存在"失败，看起来像打包丢了，实际只是路径错一层。
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REL = os.path.join(ROOT, "release")
VERSION = "V26-10.0-d"

# 与 build-release.sh --all 的矩阵保持一致。
# 顺序即报告顺序：先主流桌面平台，再 ARM。
PLATFORMS = [
    "windows-amd64",
    "linux-amd64",
    "linux-arm64",
    "darwin-arm64",
    "darwin-amd64",
]

# Mach-O 有多种魔数：arm64 是 64 位小端（cf fa ed fe），
# amd64 是 64 位大端（fe ed fa cf，别与前者弄反）。
MAGIC = {
    b"MZ": "PE/Windows",
    b"\x7fELF": "ELF/Linux",
    b"\xcf\xfa\xed\xfe": "Mach-O 64-bit LE (amd64 与 arm64 共用)",
    b"\xfe\xed\xfa\xcf": "Mach-O 64-bit BE（老 PowerPC，现代 Go 不产出）",
    b"\xce\xfa\xed\xfe": "Mach-O 32-bit LE",
}
# 注意：Mach-O 的 cffaedfe 是 MH_MAGIC_64，**amd64 与 arm64 共用**这个魔数，
# 两者都是小端。架构只能靠 cputype 区分（见下方 1b 的校验）。
# 我最初把 amd64 的期望值写成大端 feedfacf，那是老 PowerPC 的字节序，
# 结果把正确的包误判成错的 —— 校验脚本自己错了，包是对的。
EXPECT_MAGIC = {
    "windows-amd64": b"MZ",
    "linux-amd64": b"\x7fELF",
    "linux-arm64": b"\x7fELF",
    "darwin-arm64": b"\xcf\xfa\xed\xfe",
    "darwin-amd64": b"\xcf\xfa\xed\xfe",
}

fail = 0

# 路径层错必须在这里就炸掉。若放任它走到逐包检查，三个包会各报一次
# "zip 存在"失败 —— 看起来像"打包丢了三个包"，实际只是目录找错一层。
if not os.path.isdir(REL):
    raise SystemExit("找不到发布目录: %s\n（是否在错误的工作目录，或还没跑 packaging/repack.py？）" % REL)


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
        want_name = "nebula.exe" if plat.startswith("windows") else "nebula"
        exe = prefix + want_name
        if not check(exe in names, "含 " + want_name):
            continue
        blob = z.read(exe)
        head = blob[:4]
        want = EXPECT_MAGIC[plat]
        check(head.startswith(want),
              "二进制平台正确: %s (期望 %s, 头 %s)" % (MAGIC.get(want, want), plat, head.hex()))

        # 1b. 架构校验：光看魔数区分不出 amd64/arm64（同为 ELF 或同为 64 位 Mach-O）。
        # 这点很关键：拿错架构的包，用户看到的只是"二进制无法执行"这种
        # 毫无线索的报错。ELF 的 e_machine 在偏移 0x12（2 字节，小端）：
        #   0x3E = x86-64, 0xB7 = AArch64
        # Mach-O 的 cputype 在偏移 4（4 字节，小端）：
        #   0x01000007 = x86_64, 0x0100000C = arm64
        if want == b"\x7fELF":
            machine = int.from_bytes(blob[0x12:0x14], "little")
            want_machine = 0x3E if plat.endswith("amd64") else 0xB7
            check(machine == want_machine,
                  "ELF 架构正确: %s" % {0x3E: "x86-64", 0xB7: "AArch64"}.get(machine, hex(machine)))
        elif want == b"MZ":
            # PE 结构：MZ 头 → e_lfanew(0x3C) 指向 PE 签名 "PE\0\0"（4 字节）
            # → 紧接着是 COFF 头，machine 在其中偏移 0（2 字节，小端）。
            # 所以 machine 的位置是 e_lfanew + 4，不是 e_lfanew。
            #   0x8664 = x86-64, 0xAA64 = ARM64
            # 写成 e_lfanew 会读到 "PE" 的 0x4550，看起来像架构不对其实是偏移错。
            e_lfanew = int.from_bytes(blob[0x3C:0x40], "little")
            check(blob[e_lfanew:e_lfanew + 4] == b"PE\x00\x00",
                  "PE 签名正确 (e_lfanew=%d)" % e_lfanew)
            off = e_lfanew + 4
            machine = int.from_bytes(blob[off:off + 2], "little")
            want_machine = 0x8664 if plat.endswith("amd64") else 0xAA64
            check(machine == want_machine,
                  "PE 架构正确: %s" % {0x8664: "x86-64", 0xAA64: "ARM64"}.get(machine, hex(machine)))
        elif head in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf"):
            cputype = int.from_bytes(blob[4:8], "little")
            want_cpu = 0x01000007 if plat.endswith("amd64") else 0x0100000C
            check(cputype == want_cpu,
                  "Mach-O 架构正确: %s" % {0x01000007: "x86_64", 0x0100000C: "arm64"}.get(cputype, hex(cputype)))

        # 2. 内嵌前端必须是本轮重打包后的版本
        for chunk in ("Plugins-", "ShareList-", "TasksOffline-"):
            check(chunk.encode() in blob, "内嵌前端含新chunk " + chunk + "*")
        check(b"AGPL-3.0" in blob, "内嵌前端已是 AGPL-3.0 文案")
        check(b"MIT License" not in blob, "内嵌前端无 MIT License 残留")
        check(b"pluginAgreement" in blob, "内嵌前端含插件协议模块（热加载特性）")

        # 2b. 版本号注入：build-release.sh 用 -X main.buildVersion 注入，
        # 插件宿主会把它作为 HostVersion 传给插件。漏注入的话插件拿到的
        # 是 "dev"，无法做版本判断。
        check(VERSION.encode() in blob, "版本号已注入二进制 (%s)" % VERSION)

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

        # 5. 必须带的文件
        for must in ("LICENSE", "README.md", "试用说明.md"):
            check(prefix + must in names, "含 " + must)

        # Linux/macOS 包必须带 stop.sh：start.sh --daemon 会写 .nebula.pid 并
        # 提示「用 ./stop.sh 停止」，试用说明也教用户执行 ./stop.sh。
        # 只发 start.sh 的话，朋友按文档操作直接吃 No such file or directory。
        if not plat.startswith("windows"):
            check(prefix + "stop.sh" in names, "含 stop.sh（--daemon 停止用，说明文档引用了它）")
        if plat.startswith("linux"):
            check(prefix + "Linux上手说明.md" in names, "含 Linux上手说明.md（Linux 专属上手指引）")

    # 6. 磁盘目录与 zip 一致
    check(os.path.isdir(ddir), "解压目录存在")
    with zipfile.ZipFile(zpath) as z:
        zipn = {n[len(prefix):] for n in z.namelist() if n != prefix}
    disk = set(os.listdir(ddir))
    check(disk == zipn, "目录内容与 zip 一致 (磁盘多: %s, zip 多: %s)" % (disk - zipn, zipn - disk))

print("\n" + "=" * 60)
print("全部通过" if fail == 0 else "有 %d 项未通过" % fail)
raise SystemExit(1 if fail else 0)
