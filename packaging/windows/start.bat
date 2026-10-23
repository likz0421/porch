@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"
title Porch 门廊

echo.
echo   Porch 门廊   ^(动态域名 / HTTPS 证书 / 反向代理^)
echo   配置目录: %~dp0porchconf
echo.

if not exist "%~dp0porchconf" mkdir "%~dp0porchconf"

set PORT=5200

echo   管理页面 http://127.0.0.1:%PORT%/  默认账号 666 密码 666
echo   关闭本窗口即停止服务
echo.

start "" http://127.0.0.1:%PORT%/
porch.exe -cd "%~dp0porchconf"

echo.
echo 服务已停止。
pause
