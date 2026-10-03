# -*- coding: utf-8 -*-
"""
重新打包 V26-10.0-b 三平台发布物（内嵌前端由 go:embed 打进二进制）。

要点（历史踩坑，勿改）：
- *.sh 必须 LF（CRLF 会让 Linux 报 bad interpreter: /bin/sh^M）；
- *.bat 应 CRLF；
- zip 需给二进制与 *.sh 置 0755 权限位（external_attr），否则 Linux/macOS
  用户解压后还得手动 chmod +x；
- **必须包含 LICENSE**：AGPL-3.0 要求分发二进制时随附许可证声明。
  漏了它合规上就站不住脚，不要因为"文件多了不好看"而省掉；
- zip 内条目统一带顶级目录前缀（NebulaDrive-<版本>-<平台>/）。
  不带前缀的话解压出来是一堆散落在当前目录的文件，容易覆盖用户自己的同名文件；
- 仓库工作区的 LICENSE / README.md 是 CRLF（git 索引里是 LF，但检出后
  带 CRLF），所以文档类文件一律走 write_text 转换，不能 copy2。

用法:
    # 1) 交叉编译（输出必须用 Windows 可见路径，Git Bash 的 /tmp 对 go 不可见）
    cd backend
    GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o .ndbuild/nebula-windows-amd64.exe .
    GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o .ndbuild/nebula-linux-amd64 .
    GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o .ndbuild/nebula-darwin-amd64 .
    cd ..
    # 2) 组包
    python packaging/repack.py

注意：后端 frontend_dist 必须与 frontend/src 同步（CI frontend-ci.yml 有守卫），
否则打出来的是旧前端。
"""
import os
import shutil
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

BUILD = os.path.join(ROOT, "backend", ".ndbuild")
PKG = os.path.join(ROOT, "packaging")
RELEASE = os.path.join(ROOT, "release")

VERSION = "V26-10.0-b"

PLATFORMS = [
    # (目录后缀, 编译产物名, 包内二进制名, 启动脚本名, 是否 windows)
    ("windows-amd64", "nebula-windows.exe", "nebula.exe", "start.bat", True),
    ("linux-amd64", "nebula-linux", "nebula", "start.sh", False),
    ("darwin-amd64", "nebula-darwin", "nebula", "start.sh", False),
]

# 文档类文件：统一转 LF 后写入。LICENSE 是 AGPL 义务，不能少。
DOCS = [
    ("LICENSE", "LICENSE", "lf"),
    ("README.md", "README.md", "lf"),
    ("试用说明.md", "试用说明.md", "lf"),
]


def read_text(p):
    """newline="" 保留原始行尾信息，交给 write_text 统一处理。"""
    with open(p, "r", encoding="utf-8", newline="") as f:
        return f.read()


def write_text(p, s, eol):
    s = s.replace("\r\n", "\n").replace("\r", "\n")
    if eol == "crlf":
        s = s.replace("\n", "\r\n")
    with open(p, "wb") as f:
        f.write(s.encode("utf-8"))


def main():
    if not os.path.isdir(BUILD):
        raise SystemExit("缺少编译产物目录: " + BUILD)

    os.makedirs(RELEASE, exist_ok=True)
    made = []

    for suffix, buildname, binname, starter, is_win in PLATFORMS:
        src = os.path.join(BUILD, buildname)
        if not os.path.exists(src):
            print(f"[skip] 缺少编译产物 {src}")
            continue

        pkgname = f"NebulaDrive-{VERSION}-{suffix}"
        d = os.path.join(RELEASE, pkgname)
        if os.path.isdir(d):
            shutil.rmtree(d)
        os.makedirs(d)

        # 1) 二进制
        shutil.copy2(src, os.path.join(d, binname))

        # 2) 启动脚本（行尾按平台转换）
        ssrc = os.path.join(PKG, starter)
        if os.path.exists(ssrc):
            write_text(os.path.join(d, starter), read_text(ssrc), "crlf" if is_win else "lf")

        # 3) 文档（含 LICENSE —— AGPL 义务）
        for src_name, dst_name, eol in DOCS:
            base = os.path.join(PKG, src_name) if src_name == "试用说明.md" else os.path.join(ROOT, src_name)
            if os.path.exists(base):
                write_text(os.path.join(d, dst_name), read_text(base), eol)
            else:
                raise SystemExit(f"缺少必需文件: {base}")

        # 4) 自检：非 Windows 的 .sh 里出现 CR 就是不可用包
        if not is_win:
            sh = os.path.join(d, starter)
            with open(sh, "rb") as f:
                head = f.read(200)
            if b"\r" in head:
                raise SystemExit(f"{suffix}: {starter} 仍含 CR，会导致 bad interpreter")
            if not head.startswith(b"#!"):
                raise SystemExit(f"{suffix}: {starter} 首行不是 shebang: {head[:20]!r}")

        # 5) zip（权限位 + 顶级目录前缀）
        zp = os.path.join(RELEASE, pkgname + ".zip")
        if os.path.exists(zp):
            os.remove(zp)
        with zipfile.ZipFile(zp, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for fn in sorted(os.listdir(d)):
                fp = os.path.join(d, fn)
                if not os.path.isfile(fp):
                    continue
                zi = zipfile.ZipInfo(pkgname + "/" + fn, date_time=(2026, 10, 3, 12, 0, 0))
                zi.external_attr = (0o755 if (fn == binname or fn.endswith(".sh")) else 0o644) << 16
                zi.compress_type = zipfile.ZIP_DEFLATED
                with open(fp, "rb") as f:
                    z.writestr(zi, f.read())

        print(f"[ok] {suffix}: 二进制 {os.path.getsize(os.path.join(d, binname)):,} B, "
              f"zip {os.path.getsize(zp):,} B, {len(os.listdir(d))} 项")
        made.append(zp)

    print("\n=== 产出 ===")
    for p in made:
        print(" ", p)


if __name__ == "__main__":
    main()
