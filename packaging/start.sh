#!/bin/sh
# NebulaDrive 一键启动（Linux / macOS）
#
# 用法：
#   ./start.sh              前台运行，Ctrl+C 停止（推荐）
#   ./start.sh --no-open    同上，但不自动打开浏览器
#   ./start.sh --daemon     后台运行，日志写nebula.log，用 ./stop.sh 停止

cd "$(dirname "$0")" || exit 1

# ---------- 颜色（非 TTY 时不输出，避免污染日志）----------
if [ -t 1 ]; then
    BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m')
    RED=$(printf '\033[31m'); RST=$(printf '\033[0m')
else
    BOLD=''; DIM=''; RED=''; RST=''
fi

echo
echo "  ============================================"
echo "    NebulaDrive  -  自托管云存储（试用版）"
echo "  ============================================"
echo

# ---------- 1. 检查二进制 ----------
if [ ! -f ./nebula ]; then
    if [ -f ./nebula.exe ]; then
        echo "  ${RED}[错误]${RST} 这是 Windows 版本，Linux/macOS 上跑不了。"
        echo
        echo "        请下载对应系统的压缩包（文件名里会标 windows / linux / darwin）。"
        echo
        exit 1
    fi
    echo "  ${RED}[错误]${RST} 找不到 ./nebula"
    echo
    echo "        当前目录：$(pwd)"
    echo "        请确认完整解压了压缩包。"
    echo
    exit 1
fi

# zip 传输会丢失可执行权限，这里补上
if [ ! -x ./nebula ]; then
    echo "  ${DIM}[提示]${RST} 正在补加可执行权限..."
    chmod +x ./nebula || {
        echo "  ${RED}[错误]${RST} 无权修改权限，请手动执行： chmod +x ./nebula"
        exit 1
    }
fi

# ---------- 1.5 验证二进制格式与本机匹配 ----------
# 只看「文件存在」不够：下错版本（拿了 Windows 的）时文件同样存在但根本跑不了，
# 用户只会看到难以理解的 "Exec format error"。
# 这里读文件头判断格式，把错误翻译成人话。
# 依赖退出码不可靠 —— 部分 shell（含 Git Bash）会转换退出码，实测拿到 0。
NEEDLE=nebula
MAGIC=$(head -c 4 ./"$NEEDLE" 2>/dev/null | od -An -tx1 2>/dev/null | tr -d ' \n')
case "$MAGIC" in
    7f454c46) ;;                       # 7f 45 4c 46 = ELF，Linux 可执行
    cffaedfe|cefaedfe)                 # Mach-O 64/32，macOS 可执行
        ;;
    4d5a9000|4d5a)                     # 4d 5a = "MZ"，Windows PE
        echo "  ${RED}[错误]${RST} 这是 Windows 版本，Linux/macOS 上跑不了。"
        echo
        echo "        请下载对应系统的压缩包"
        echo "        （压缩包名里会标 windows-amd64 / linux-amd64 / darwin-amd64）。"
        echo
        exit 1
        ;;
    "")
        echo "  ${RED}[错误]${RST} ./nebula 是空文件或无法读取。"
        echo "        请重新解压一次压缩包。"
        echo
        exit 1
        ;;
    *)
        echo "  ${RED}[错误]${RST} ./nebula 的文件格式无法识别（magic: $MAGIC）"
        echo
        echo "        当前系统：$(uname -s) $(uname -m)"
        echo "        正常情况下 Linux 版应以 ELF 头开头、macOS 版以 Mach-O 头开头。"
        echo "        建议重新下载对应系统的压缩包。"
        echo
        exit 1
        ;;
esac

# 架构不匹配：ELF 头里第 5 字节是 1（32 位）或 2（64 位），第 19 字节是架构
case "$MAGIC" in
    7f454c46)
        EI_CLASS=$(head -c 5 ./nebula | od -An -tu1 | tr -d ' \n')
        HOST_BITS=$(getconf LONG_BIT 2>/dev/null || echo 64)
        if [ "$HOST_BITS" = "64" ] && [ "$EI_CLASS" = "1" ]; then
            echo "  ${RED}[错误]${RST} 这是 32 位版本，但当前系统是 64 位。"
            echo "        请下载 amd64 版本。"
            echo
            exit 1
        fi
        ;;
esac

# ---------- 2. 参数解析 ----------
OPEN_FLAG="-open"
DAEMON=0
case "$1" in
    --no-open) OPEN_FLAG="" ;;
    --daemon)  DAEMON=1; OPEN_FLAG="" ;;
esac

# ---------- 3. 运行前提示 ----------
if [ -f data/conf.env ]; then
    echo "  [信息] 已有配置，将直接启动"
else
    echo "  [信息] 首次运行：启动后请按页面指引创建管理员账号"
fi
echo "  [信息] 端口冲突会自动避开（5212 起）"
echo

# ---------- 4. 启动 ----------
if [ "$DAEMON" = "1" ]; then
    echo "  [信息] 后台运行，日志：nebula.log"
    ./nebula -autoport $OPEN_FLAG > nebula.log 2>&1 &
    PID=$!
    echo "$PID" > .nebula.pid
    sleep 2
    if ! kill -0 "$PID" 2>/dev/null; then
        echo
        echo "  ${RED}[错误]${RST} 启动失败，日志末尾："
        echo "  ----------------------------------------"
        tail -20 nebula.log
        echo "  ----------------------------------------"
        exit 1
    fi
    echo "  [信息] PID=$PID ，停止服务请执行 ./stop.sh"
    echo
    sed -n 's/.*访问地址：//p' nebula.log | head -1
    echo
else
    # 前台运行，日志直接输出到终端
    exec ./nebula -autoport $OPEN_FLAG
fi
