@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "TARGET_PORT=8080"
set "PORT_PID="
set "PORT_NAME="

for /f "usebackq delims=" %%P in (`powershell -NoProfile -Command "(Get-NetTCPConnection -LocalPort %TARGET_PORT% -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty OwningProcess)"`) do set "PORT_PID=%%P"

if not defined PORT_PID (
    echo [unified-server] nothing is listening on port %TARGET_PORT%.
    pause
    exit /b 0
)

for /f "usebackq delims=" %%N in (`powershell -NoProfile -Command "(Get-Process -Id %PORT_PID% -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ProcessName)"`) do set "PORT_NAME=%%N"

if /I not "!PORT_NAME!"=="unified-server" (
    echo [unified-server] port %TARGET_PORT% is used by another process.
    if defined PORT_NAME (
        echo [unified-server] process: !PORT_NAME!  pid: !PORT_PID!
    ) else (
        echo [unified-server] pid: !PORT_PID!
    )
    echo [unified-server] stop it manually if needed.
    pause
    exit /b 1
)

echo [unified-server] stopping pid !PORT_PID! ...
powershell -NoProfile -Command "Stop-Process -Id !PORT_PID! -Force"
echo [unified-server] stopped.
pause
