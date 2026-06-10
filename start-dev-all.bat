@echo off
setlocal EnableExtensions

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

pushd "%ROOT%" || (
    echo [dev-all] failed to enter project directory
    pause
    exit /b 1
)

echo Starting development services...
echo.
echo - Rust adapter:  http://127.0.0.1:8091
echo - Go server:     http://127.0.0.1:8080
echo - Vue frontend:  http://127.0.0.1:5173
echo.

start "adapter-rs" cmd /k ""%ROOT%\start-adapter.bat""
start "unified-server" cmd /k ""%ROOT%\restart-unified-server.bat""
start "frontend" cmd /k ""%ROOT%\start-frontend.bat""

echo Opened 3 windows.
echo Close each window to stop the related service.
echo.
popd
pause
