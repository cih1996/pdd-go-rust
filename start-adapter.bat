@echo off
setlocal EnableExtensions

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"

pushd "%ROOT%\adapter-rs" || (
    echo [adapter-rs] failed to enter adapter-rs directory
    pause
    exit /b 1
)

echo [adapter-rs] working dir: %cd%

where cargo >nul 2>nul
if errorlevel 1 (
    echo.
    echo [adapter-rs] cargo was not found.
    echo [adapter-rs] install Rust from:
    echo https://www.rust-lang.org/tools/install
    echo.
    popd
    pause
    exit /b 1
)

where link >nul 2>nul
if errorlevel 1 (
    echo.
    echo [adapter-rs] MSVC linker link.exe was not found.
    echo [adapter-rs] install Visual Studio Build Tools with Desktop development with C++.
    echo [adapter-rs] or run this script from x64 Native Tools Command Prompt for VS.
    echo [adapter-rs] download:
    echo https://visualstudio.microsoft.com/visual-cpp-build-tools/
    echo.
    popd
    pause
    exit /b 1
)

echo [adapter-rs] starting on http://127.0.0.1:8091
echo.
cargo run

echo.
echo [adapter-rs] process exited.
popd
pause
