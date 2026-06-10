@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
set "TARGET_PORT=8080"

pushd "%ROOT%" || (
    echo [unified-server] failed to enter project directory
    pause
    exit /b 1
)

echo [unified-server] working dir: %cd%

where go >nul 2>nul
if errorlevel 1 (
    echo.
    echo [unified-server] go was not found.
    echo [unified-server] install Go from:
    echo https://go.dev/dl/
    echo.
    popd
    pause
    exit /b 1
)

if not defined ADAPTER_BASE_URL set "ADAPTER_BASE_URL=http://127.0.0.1:8091"
if not defined ENABLE_VISION_MOCK set "ENABLE_VISION_MOCK=true"

echo [unified-server] ADAPTER_BASE_URL=%ADAPTER_BASE_URL%
echo [unified-server] ENABLE_VISION_MOCK=%ENABLE_VISION_MOCK%
echo [unified-server] starting on http://127.0.0.1:8080
echo.

set "PORT_PID="
set "PORT_NAME="
for /f "usebackq delims=" %%P in (`powershell -NoProfile -Command "(Get-NetTCPConnection -LocalPort %TARGET_PORT% -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty OwningProcess)"`) do set "PORT_PID=%%P"
if defined PORT_PID (
    for /f "usebackq delims=" %%N in (`powershell -NoProfile -Command "(Get-Process -Id %PORT_PID% -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ProcessName)"`) do set "PORT_NAME=%%N"
    if /I "!PORT_NAME!"=="unified-server" (
        echo [unified-server] found previous unified-server on port %TARGET_PORT%.
        echo [unified-server] stopping pid !PORT_PID! and restarting...
        powershell -NoProfile -Command "Stop-Process -Id !PORT_PID! -Force"
        timeout /t 1 /nobreak >nul
    ) else (
        echo [unified-server] port %TARGET_PORT% is already in use.
        if defined PORT_NAME (
            echo [unified-server] process: !PORT_NAME!  pid: !PORT_PID!
        ) else (
            echo [unified-server] pid: !PORT_PID!
        )
        echo [unified-server] stop the process using %TARGET_PORT%, then run again.
        popd
        pause
        exit /b 1
    )
)

go run ./cmd/unified-server

echo.
echo [unified-server] process exited.
popd
pause
