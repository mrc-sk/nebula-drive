#!/bin/sh
# 构建 NebulaDrive 试用包（Linux / macOS）
#
# 用法：
#   ./build-release.sh              构建当前系统对应的包
#   ./build-release.sh --all        构建三个平台（在 Linux 上交叉编译 Windows 版）
#
# 产物：release/NebulaDrive-<版本>-<系统>-<架构>/
# 每个目录自包含：二进制 + 启动脚本 + 试用说明 + LICENSE。
#
# 前提：需要 Go 1.25+。前端资源 backend/frontend_dist/ 已随仓库提交，
#       所以不需要装 Node —— go:embed 会直接把它打进二进制。

cd "$(dirname "$0")/.." || exit 1

if [ -t 1 ]; then
    BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m')
    RED=$(printf '\033[31m'); GRN=$(printf '\033[32m'); RST=$(printf '\033[0m')
else
    BOLD=''; DIM=''; RED=''; GRN=''; RST=''
fi

echo
echo "  ============================================"
echo "    构建单二进制试用包"
echo "  ============================================"
echo

# ---------- 1. 检查 Go ----------
if ! command -v go >/dev/null 2>&1; then
    echo "  ${RED}[错误]${RST} 找不到 go 命令"
    echo
    echo "        请先安装 Go 1.25 或更高版本："
    echo "          https://go.dev/dl/"
    echo
    echo "        Debian/Ubuntu: sudo apt install golang-go"
    echo "        Fedora/RHEL  : sudo dnf install golang"
    echo "        macOS        : brew install go"
    echo
    exit 1
fi

GOVER=$(go version | awk '{print $3}' | sed 's/go//')
echo "  [信息] Go 版本：$GOVER"
case "$GOVER" in
    1.*|2.*|3.*|4.*)
        echo "  ${RED}[错误]${RST} Go 版本过低（需要 1.25+），go.mod 声明了 go 1.25.0"
        exit 1
        ;;
esac

# ---------- 2. 版本号 ----------
if [ -d .git ] && command -v git >/dev/null 2>&1; then
    VER=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")
else
    VER="dev"
fi
[ -z "$VER" ] && VER="dev"

# ---------- 3. 前端资源（go:embed 需要）----------
if [ ! -d backend/frontend_dist ]; then
    echo "  ${RED}[错误]${RST} 找不到 backend/frontend_dist/"
    echo
    echo "        前端资源是通过 go:embed 打进二进制的，必须先构建前端："
    echo "          cd frontend && npm install && npm run build"
    echo
    echo "        正常情况下 clone 仓库就会自带它。如果确实没有，"
    echo "        说明clone 的版本较旧 —— 拉取最新 main 后再试。"
    echo
    exit 1
fi

# -trimpath 去掉本机路径（可复现构建）
# -ldflags="-s -w" 剥离符号表与调试信息，体积 65 MB -> 47 MB
LDFLAGS="-s -w"
export CGO_ENABLED=0
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

build_one() {
    _os="$1"; _arch="$2"; _ext="$3"; _script="$4"
    _out="release/NebulaDrive-${VER}-${_os}-${_arch}"

    echo
    echo "  [构建] ${_os}/${_arch}  ->  ${_out}"

    mkdir -p "$_out" || return 1

    if ! (cd backend && GOOS="$_os" GOARCH="$_arch" go build \
            -trimpath -ldflags="$LDFLAGS" -o "../${_out}/nebula$_ext" .); then
        echo "  ${RED}[错误]${RST} ${_os} 版编译失败"
        return 1
    fi

    # 复制运行文件
    if [ "$_script" = "bat" ]; then
        cp packaging/start.bat packaging/试用说明.md "$_out/" 2>/dev/null
    else
        cp packaging/start.sh packaging/stop.sh packaging/试用说明.md "$_out/" 2>/dev/null
    fi
    cp LICENSE "$_out/" 2>/dev/null

    # *.sh 必须是 LF —— CRLF 会导致 Linux 报 bad interpreter: /bin/sh^M
    for f in "$_out"/*.sh; do
        [ -f "$f" ] || continue
        if grep -qU "$(printf '\r')" "$f" 2>/dev/null; then
            echo "  ${DIM}[修正]${RST} $(basename "$f") 行尾 CRLF -> LF"
            tr -d '\r' < "$f" > "$f.tmp" && mv "$f.tmp" "$f"
        fi
    done
    chmod +x "$_out"/nebula$_ext "$_out"/*.sh 2>/dev/null

    # 打 zip（没有 zip 命令时跳过，不影响使用）
    if command -v zip >/dev/null 2>&1; then
        (cd "$_out" && zip -qr "../$(basename "$_out").zip" . 2>/dev/null) \
            && echo "  ${GRN}[完成]${RST} $(basename "$_out").zip"
    else
        SIZE=$(du -sh "$_out" 2>/dev/null | awk '{print $1}')
        echo "  ${GRN}[完成]${RST} ${_out} (${SIZE})"
        echo "  ${DIM}[提示]${RST} 未安装 zip，跳过打包；直接把目录压缩发出去即可"
    fi
}

# ---------- 4. 构建 ----------
HOST_OS=$(uname -s | tr 'A-Z' 'a-z')
case "$HOST_OS" in
    linux)  HOST_OS=linux ;;
    darwin) HOST_OS=darwin ;;
esac
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    x86_64|amd64) HOST_ARCH=amd64 ;;
    aarch64|arm64) HOST_ARCH=arm64 ;;
esac

if [ "$1" = "--all" ]; then
    build_one linux  amd64  ""   sh || exit 1
    build_one darwin amd64  ""   sh || exit 1
    build_one windows amd64 ".exe" bat || exit 1
else
    case "$HOST_OS" in
        linux|darwin) build_one "$HOST_OS" "$HOST_ARCH" "" sh || exit 1 ;;
        *)
            echo "  ${RED}[错误]${RST} 不支持的系统：$HOST_OS"
            echo "        Windows 请用 packaging\\build-release.bat"
            exit 1
            ;;
    esac
fi

echo
echo "  ============================================"
echo "    ${GRN}构建完成${RST}"
echo "  ============================================"
echo
echo "  产物目录："
ls -d release/*/ 2>/dev/null | sed 's/^/    /'
echo
echo "  对方解压后运行："
echo "    Windows : start.bat      （双击）"
echo "    Linux   : ./start.sh     （若提示权限不足：chmod +x start.sh nebula）"
echo "    macOS   : ./start.sh"
echo
echo "  不需要安装 Go / Node / 数据库。"
echo
