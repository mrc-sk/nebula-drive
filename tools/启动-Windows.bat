@echo off
chcp 65001 >nul 2>nul
title NebulaDrive 插件制作器 (Python 桌面版)
cd /d "%~dp0"

where python >nul 2>nul
if %errorlevel%==0 goto :run_python
where py >nul 2>nul
if %errorlevel%==0 goto :run_py

echo.
echo   没有找到 Python。请安装 Python 3.8 或更高版本：
echo   https://www.python.org/downloads/
echo   安装时请勾选 "Add Python to PATH" 和 "tcl/tk and IDLE" 组件。
echo.
pause
exit /b 1

:run_python
python -c "import tkinter" >nul 2>nul
if errorlevel 1 (
  echo   当前 Python 缺少 tkinter（图形界面组件）。
  echo   请重新运行安装包，选择 Repair 并勾选 "tcl/tk and IDLE"。
  pause
  exit /b 1
)
python "插件制作器.py" %*
exit /b %errorlevel%

:run_py
py -3 -c "import tkinter" >nul 2>nul
if errorlevel 1 (
  echo   当前 Python 缺少 tkinter，请修复安装并勾选 tcl/tk 组件。
  pause
  exit /b 1
)
py -3 "插件制作器.py" %*
exit /b %errorlevel%
