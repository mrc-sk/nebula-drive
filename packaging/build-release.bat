@echo off
chcp 65001 >nul 2>&1
setlocal
cd /d "%~dp0\.."
title 构建 NebulaDrive 试用包

echo.
echo   ============================================
echo     构建单二进制试用包
echo   ============================================
echo.

where go >nul 2>&1
if errorlevel 1 (
    echo   [错误] 找不到 go 命令
    echo.
    echo     请先安装 Go 1.25+ 并把 Go 安装目录加入 PATH。
    echo     下载：https://go.dev/dl/
    echo.
    pause
    exit /b 1
)

set "VER="
for /f "tokens=2 delims==" %%v in ('git describe --tags --abbrev=0 2^>nul') do set "VER=%%v"
if not defined VER set "VER=dev"

rem -trimpath 去掉本机路径（可复现构建）；-ldflags="-s -w" 剥离符号表与调试信息
rem 前端已通过 go:embed 内嵌进二进制，所以不需要 Node。
set "LDFLAGS=-s -w"

rem ---------- 产物 1：Windows ----------
set "OUT=release\NebulaDrive-%VER%-windows-amd64"
echo   [1/4] Windows x64  ->  %OUT%
if not exist "%OUT%" mkdir "%OUT%"
pushd backend
set CGO_ENABLED=0
go build -trimpath -ldflags="%LDFLAGS%" -o "..\%OUT%\nebula.exe" .
if errorlevel 1 (popd & echo   [错误] Windows 版编译失败 & pause & exit /b 1)
popd
copy /y "packaging\start.bat"    "%OUT%\" >nul
copy /y "packaging\试用说明.md"   "%OUT%\" >nul
copy /y "LICENSE"                "%OUT%\" >nul

rem ---------- 产物 2：Linux ----------
set "OUTL=release\NebulaDrive-%VER%-linux-amd64"
echo   [2/4] Linux x64    ->  %OUTL%
if not exist "%OUTL%" mkdir "%OUTL%"
pushd backend
set GOOS=linux
set GOARCH=amd64
go build -trimpath -ldflags="%LDFLAGS%" -o "..\%OUTL%\nebula" .
if errorlevel 1 (popd & echo   [错误] Linux 版编译失败 & pause & exit /b 1)
popd
set GOOS=
set GOARCH=
copy /y "packaging\start.sh"  "%OUTL%\" >nul
copy /y "packaging\stop.sh"   "%OUTL%\" >nul
copy /y "packaging\试用说明.md" "%OUTL%\" >nul
copy /y "LICENSE"            "%OUTL%\" >nul

rem ---------- 产物 3：macOS ----------
set "OUTM=release\NebulaDrive-%VER%-darwin-amd64"
echo   [3/4] macOSx64     ->  %OUTM%
if not exist "%OUTM%" mkdir "%OUTM%"
pushd backend
set GOOS=darwin
set GOARCH=amd64
go build -trimpath -ldflags="%LDFLAGS%" -o "..\%OUTM%\nebula" .
if errorlevel 1 (popd & echo   [错误] macOS 版编译失败 & pause & exit /b 1)
popd
set GOOS=
set GOARCH=
copy /y "packaging\start.sh"  "%OUTM%\" >nul
copy /y "packaging\stop.sh"   "%OUTM%\" >nul
copy /y "packaging\试用说明.md" "%OUTM%\" >nul
copy /y "LICENSE"            "%OUTM%\" >nul

echo   [4/4] 打包 zip...
rem 用 PowerShell 打 zip 并修正行尾：
rem   *.sh 必须是 LF —— CRLF 会让 Linux 报 "bad interpreter: /bin/sh^M"
rem   *.bat 最好是 CRLF —— cmd 的传统行尾
for %%D in ("%OUT%" "%OUTL%" "%OUTM%") do (
    powershell -NoProfile -Command ^
      "$$d='%%~fD';" ^
      "Get-ChildItem -Path $$d -Filter *.sh -ErrorAction SilentlyContinue | ForEach-Object {" ^
      "  $$c=[IO.File]::ReadAllText($$_.FullName);" ^
      "  [IO.File]::WriteAllText($$_.FullName, $$c.Replace(\"`r`n\",\"`n\"))" ^
      "};" ^
      "Compress-Archive -Path (Join-Path $$d '*') -DestinationPath ($$d + '.zip') -Force" 2>nul
)

echo.
echo   ============================================
echo     构建完成
echo   ============================================
echo.
dir /b release\*.zip
echo.
echo   每个目录/zip 都是自包含的：对方解压后运行对应脚本即可，
echo   不需要安装 Go / Node / 数据库。
echo.
echo     Windows : start.bat     （双击）
echo     Linux   : ./start.sh    （可能需 chmod +x）
echo     macOS   : ./start.sh
echo.
pause
endlocal
