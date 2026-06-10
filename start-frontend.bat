@echo off
setlocal EnableExtensions

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

pushd "%ROOT%\frontend" || (
    echo [frontend] failed to enter frontend directory
    pause
    exit /b 1
)

echo [frontend] working dir: %cd%

where npm >nul 2>nul
if errorlevel 1 (
    echo.
    echo [frontend] npm was not found.
    echo [frontend] install Node.js from:
    echo https://nodejs.org/
    echo.
    popd
    pause
    exit /b 1
)

if not exist "node_modules" (
    echo.
    echo [frontend] node_modules not found, installing...
    call npm install
    if errorlevel 1 (
        echo.
        echo [frontend] npm install failed.
        popd
        pause
        exit /b 1
    )
)

echo [frontend] starting on http://127.0.0.1:5173
echo.
call npm run dev -- --host 127.0.0.1 --port 5173

echo.
echo [frontend] process exited.
popd
pause
