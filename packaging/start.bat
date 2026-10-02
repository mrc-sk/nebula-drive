@echo off
chcp 65001 >nul 2>&1
setlocal
cd /d "%~dp0"
title NebulaDrive

echo.
echo   ============================================
echo     NebulaDrive  -  自托管云存储（试用版）
echo   ============================================
echo.

if not exist "nebula.exe" (
    echo   [错误] 找不到 nebula.exe
    echo.
    echo     当前目录：%cd%
    echo     请确认完整解压了压缩包 —— nebula.exe 必须和本文件在同一层。
    echo.
    pause
    exit /b 1
)

if exist "data\conf.env" (
    echo   [信息] 已有配置，将直接启动
) else (
    echo   [信息] 首次运行：浏览器会自动打开安装向导，按提示创建管理员账号
)
echo   [信息] 端口冲突会自动避开（5212 起）
echo.

rem -autoport 自动挑空闲端口；-open 启动后自动开浏览器。
rem 端口选择放在 Go 里做（net.Listen 实际试探），比在 bat 里解析 netstat 可靠。
rem 前台运行，日志直接显示在这个窗口里。
nebula.exe -autoport -open

echo.
echo   ------------------------------------------------------------
echo   [已停止] 服务已退出
echo.
pause
endlocal
