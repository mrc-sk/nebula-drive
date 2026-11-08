#!/usr/bin/env bash
# NebulaDrive 插件制作器 · Linux / macOS 启动脚本
set -e
cd "$(dirname "$0")"

PY=""
if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "未找到 Python 3。Debian/Ubuntu: sudo apt install python3 python3-tk"
  exit 1
fi

if ! "$PY" -c "import tkinter" >/dev/null 2>&1; then
  echo "Python 缺少 tkinter（图形界面组件）。"
  echo "  Debian/Ubuntu : sudo apt install python3-tk"
  echo "  Fedora/RHEL   : sudo dnf install python3-tkinter"
  echo "  Arch          : sudo pacman -S tk"
  exit 1
fi

exec "$PY" "插件制作器.py" "$@"
