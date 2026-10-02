#!/bin/sh
# 停止后台运行的 NebulaDrive（配合 ./start.sh --daemon 使用）

cd "$(dirname "$0")" || exit 1

if [ ! -f .nebula.pid ]; then
    echo "  没有找到 .nebula.pid —— 服务可能不是用 --daemon 方式启动的。"
    echo "  如果它在另一个终端前台运行，直接在那个窗口按 Ctrl+C 即可。"
    exit 0
fi

PID=$(cat .nebula.pid)

if ! kill -0 "$PID" 2>/dev/null; then
    echo "  进程 $PID 已不存在，清理 pid 文件"
    rm -f .nebula.pid
    exit 0
fi

echo "  正在停止 NebulaDrive（PID=$PID）..."
kill "$PID"

# 给它一点时间优雅退出
for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ! kill -0 "$PID" 2>/dev/null; then
        rm -f .nebula.pid
        echo "  已停止。"
        exit 0
    fi
    sleep 1
done

echo "  未响应，强制结束"
kill -9 "$PID" 2>/dev/null
rm -f .nebula.pid
echo "  已停止。"
