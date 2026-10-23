@echo off
chcp 65001 >nul
set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
if exist "%STARTUP%\porch-autostart.vbs" (
  del "%STARTUP%\porch-autostart.vbs"
  echo 已移除开机自启
) else (
  echo 未找到开机自启项
)
pause
