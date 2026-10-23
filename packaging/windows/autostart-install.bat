@echo off
chcp 65001 >nul
setlocal
set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
set "VBS=%STARTUP%\porch-autostart.vbs"

> "%VBS%" echo Set ws = CreateObject^("WScript.Shell"^)
>> "%VBS%" echo ws.CurrentDirectory = "%~dp0"
>> "%VBS%" echo ws.Run "porch.exe -cd ""%~dp0porchconf""", 0, False

echo 已创建开机自启: %VBS%
echo 移除请运行 autostart-remove.bat
pause
