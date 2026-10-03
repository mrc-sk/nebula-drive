# -*- coding: utf-8 -*-
"""
重新打包 V26-10.0-b 三平台发布物（内含 AGPL 前端）。

要点（历史踩坑，勿改）：
- *.sh 必须 LF（CRLF 会让 Linux 报 bad interpreter: /bin/sh^M）；
- *.bat 应 CRLF；
- zip 需给 nebula 与 *.sh 置 0755 权限位（external_attr）；
- packaging/ 源脚本本身是 CRLF，copy 出来要逐平台转换。

用法: python packaging/repack.py
"""
import os
import shutil
import subprocess
import sys
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

BUILD = os.path.join(ROOT, "release", "_build")
PKG = os.path.join(ROOT, "packaging")
RELEASE = os.path.join(ROOT, "release")

VERSION = "V26-10.0-b"

PLATFORMS = [
    # (目录后缀, 二进制名, 启动脚本名, 是否 windows)
    ("windows-amd64", "nebula.exe", "start.bat", True),
    ("linux-amd64", "nebula", "start.sh", False),
    ("darwin-amd64", "nebula", "start.sh", False),
]

# 每个发布目录应包含的文件
COMMON_FILES = ["nebula", "start", "试用说明.md", "README.md"]


def read_text(p):
    with open(p, "r", encoding="utf-8", newline="") as f:
        return f.read()


def write_text(p, s, eol):
    s = s.replace("\r\n", "\n").replace("\r", "\n")
    if eol == "crlf":
        s = s.replace("\n", "\r\n")
    with open(p, "wb") as f:
        f.write(s.encode("utf-8"))


def run_check(args, desc):
    p = subprocess.run(args, capture_output=True, text=True, encoding="utf-8", errors="replace")
    if p.returncode != 0:
        print(f"  [warn] {desc}: rc={p.returncode} {(p.stderr or '')[:300]}")
    return p.returncode == 0


def main():
    if not os.path.isdir(BUILD):
        raise SystemExit("缺少 _build 目录，请先交叉编译: " + BUILD)

    os.makedirs(RELEASE, exist_ok=True)
    made = []

    for suffix, binname, starter, is_win in PLATFORMS:
        src = os.path.join(BUILD, f"nebula-{suffix}" + (".exe" if is_win else ""))
        if not os.path.exists(src):
            print(f"[skip] 缺少 {src}")
            continue

        d = os.path.join(RELEASE, f"NebulaDrive-{VERSION}-{suffix}")
        if os.path.isdir(d):
            shutil.rmtree(d)
        os.makedirs(d)

        # 1) 二进制
        shutil.copy2(src, os.path.join(d, binname))
        os.chmod(os.path.join(d, binname), 0o755)

        # 2) 启动脚本（行尾按平台转换）
        sname = starter
        ssrc = os.path.join(PKG, sname)
        if os.path.exists(ssrc):
            eol = "crlf" if is_win else "lf"
            write_text(os.path.join(d, sname), read_text(ssrc), eol)
            os.chmod(os.path.join(d, sname), 0o755)
            print(f"  {suffix}: {sname} -> {eol}")

        # 3) 说明文档
        doc = os.path.join(PKG, "试用说明.md")
        if os.path.exists(doc):
            write_text(os.path.join(d, "试用说明.md"), read_text(doc), "lf")

        readme = os.path.join(ROOT, "README.md")
        if os.path.exists(readme):
            write_text(os.path.join(d, "README.md"), read_text(readme), "lf")

        # 4) 校验 shebang / 行尾
        if not is_win:
            sh = os.path.join(d, sname)
            if os.path.exists(sh):
                with open(sh, "rb") as f:
                    head = f.read(200)
                if b"\r" in head:
                    raise SystemExit(f"{suffix}: {sname} 仍含 CR，会导致 bad interpreter")

        # 5) zip（权限位）
        zp = os.path.join(RELEASE, f"NebulaDrive-{VERSION}-{suffix}.zip")
        if os.path.exists(zp):
            os.remove(zp)
        with zipfile.ZipFile(zp, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for root, _dirs, files in os.walk(d):
                for fn in sorted(files):
                    fp = os.path.join(root, fn)
                    arc = os.path.relpath(fp, d)
                    attr = 0o644 << 16
                    if fn == binname or fn.endswith(".sh"):
                        attr = 0o755 << 16
                    zi = zipfile.ZipInfo(arc, date_time=(2026, 10, 3, 17, 20, 0))
                    zi.external_attr = attr
                    zi.compress_type = zipfile.ZIP_DEFLATED
                    with open(fp, "rb") as f:
                        z.writestr(zi, f.read())

        print(f"[ok] {suffix}: 目录 {len(os.listdir(d))} 项, zip {os.path.getsize(zp):,} bytes")
        made.append(zp)

    print("\n=== 产出 ===")
    for p in made:
        print(" ", p)

    # 清理构建中间目录（保留发布目录与 zip）
    shutil.rmtree(BUILD, ignore_errors=True)
    print("\n[clean] 已删除中间目录 _build")


if __name__ == "__main__":
    main()
