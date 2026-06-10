@echo off
setlocal EnableExtensions

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

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
go run ./cmd/unified-server

echo.
echo [unified-server] process exited.
popd
pause
