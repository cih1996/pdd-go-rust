param(
    [string]$OutputDir = "",
    [switch]$SkipFrontendBuild,
    [switch]$SkipPythonBuild
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host "[OK] $Message" -ForegroundColor Green
}

function Ensure-Command {
    param([string]$Name)
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $command) {
        throw "Command not found: $Name"
    }
}

function Resolve-PythonForProject {
    param([string]$ProjectDir)
    $venvPython = Join-Path $ProjectDir ".venv\Scripts\python.exe"
    if (Test-Path -LiteralPath $venvPython) {
        return $venvPython
    }
    return "python"
}

function Ensure-PyInstallerModule {
    param([string]$PythonExe)
    & $PythonExe -c "import importlib.util, sys; sys.exit(0 if importlib.util.find_spec('PyInstaller') else 1)" 2>$null
    if ($LASTEXITCODE -ne 0) {
        & $PythonExe -m pip install pyinstaller
        if ($LASTEXITCODE -ne 0) {
            throw "failed to install PyInstaller with $PythonExe"
        }
    }
}

function Reset-Dir {
    param([string]$Path)
    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $Path | Out-Null
}

function Copy-IfExists {
    param(
        [string]$Source,
        [string]$Destination
    )
    if (Test-Path -LiteralPath $Source) {
        $parent = Split-Path -Parent $Destination
        if ($parent) {
            New-Item -ItemType Directory -Force -Path $parent | Out-Null
        }
        Copy-Item -LiteralPath $Source -Destination $Destination -Recurse -Force
    }
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $ProjectRoot "dist\windows-bundle"
}
$OutputDir = [System.IO.Path]::GetFullPath($OutputDir)
$BuildDir = Join-Path $ProjectRoot ".build\windows-package"
$PythonBuildRoot = Join-Path $BuildDir "python"

Write-Step "Preparing directories"
Reset-Dir -Path $OutputDir
Reset-Dir -Path $BuildDir
New-Item -ItemType Directory -Force -Path $PythonBuildRoot | Out-Null

Write-Step "Checking toolchain"
Ensure-Command -Name "go"
Ensure-Command -Name "cargo"
Ensure-Command -Name "python"
Ensure-Command -Name "npm"

$OpenCVPython = Resolve-PythonForProject -ProjectDir (Join-Path $ProjectRoot "opencv-server")
$OCRPython = Resolve-PythonForProject -ProjectDir (Join-Path $ProjectRoot "ocr-server")
Ensure-PyInstallerModule -PythonExe $OpenCVPython
Ensure-PyInstallerModule -PythonExe $OCRPython

if (-not $SkipFrontendBuild) {
    Write-Step "Building frontend dist"
    Push-Location (Join-Path $ProjectRoot "frontend")
    try {
        npm run build
        if ($LASTEXITCODE -ne 0) {
            throw "frontend build failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
} else {
    Write-Step "Skipping frontend build"
}

Write-Step "Building adapter exe"
    Push-Location (Join-Path $ProjectRoot "adapter-rs")
    try {
        cargo build --release
        if ($LASTEXITCODE -ne 0) {
            throw "adapter build failed with exit code $LASTEXITCODE"
        }
        Copy-Item -LiteralPath ".\target\release\adapter-rs.exe" -Destination (Join-Path $OutputDir "adapter-rs.exe") -Force
    }
    finally {
        Pop-Location
    }

Write-Step "Building main business exe"
    Push-Location $ProjectRoot
    try {
        go build -trimpath -ldflags "-s -w" -o (Join-Path $OutputDir "unified-server.exe") .\cmd\unified-server
        if ($LASTEXITCODE -ne 0) {
            throw "main business build failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

if (-not $SkipPythonBuild) {
    Write-Step "Packaging opencv-server.exe"
    Push-Location (Join-Path $ProjectRoot "opencv-server")
    try {
        & $OpenCVPython -m PyInstaller --noconfirm --clean --onefile --name opencv-server `
            --paths . `
            --add-data "static;static" `
            run_opencv.py
        if ($LASTEXITCODE -ne 0) {
            throw "opencv-server pyinstaller failed with exit code $LASTEXITCODE"
        }
        Copy-Item -LiteralPath ".\dist\opencv-server.exe" -Destination (Join-Path $OutputDir "opencv-server.exe") -Force
    }
    finally {
        Pop-Location
    }

    Write-Step "Packaging ocr-server.exe"
    Push-Location (Join-Path $ProjectRoot "ocr-server")
    try {
        & $OCRPython -m PyInstaller --noconfirm --clean --onefile --name ocr-server `
            --paths . `
            --collect-submodules onnxocr `
            --collect-data onnxocr `
            --collect-all flask `
            --collect-all werkzeug `
            --collect-all jinja2 `
            --collect-all click `
            --collect-all itsdangerous `
            --collect-all blinker `
            --add-data "app-service.py;." `
            --add-data "templates;templates" `
            --add-data "static;static" `
            --add-data "onnxocr;onnxocr" `
            run_ocr.py
        if ($LASTEXITCODE -ne 0) {
            throw "ocr-server pyinstaller failed with exit code $LASTEXITCODE"
        }
        Copy-Item -LiteralPath ".\dist\ocr-server.exe" -Destination (Join-Path $OutputDir "ocr-server.exe") -Force
    }
    finally {
        Pop-Location
    }
} else {
    Write-Step "Skipping python exe packaging"
}

Write-Step "Copying runtime assets"
Copy-IfExists -Source (Join-Path $ProjectRoot "frontend\dist") -Destination (Join-Path $OutputDir "frontend\dist")
Copy-IfExists -Source (Join-Path $ProjectRoot "adb") -Destination (Join-Path $OutputDir "adb")
New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir ".runtime") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir "logs") | Out-Null

$batPath = Join-Path $OutputDir "start-all.bat"
@'
@echo off
setlocal
set "ROOT=%~dp0"
cd /d "%ROOT%"

powershell -NoProfile -ExecutionPolicy Bypass -Command "$ports = 18080,8091,7771,5005; Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $ports -contains $_.LocalPort } | Select-Object -ExpandProperty OwningProcess -Unique | ForEach-Object { try { Stop-Process -Id $_ -Force -ErrorAction Stop } catch {} }" >nul 2>nul

if not exist "%ROOT%.runtime" mkdir "%ROOT%.runtime"
if not exist "%ROOT%logs" mkdir "%ROOT%logs"

set "OPENCV_PORT=7771"
start "opencv-server" /d "%ROOT%" "%ROOT%opencv-server.exe"

set "ONNXOCR_PORT=5005"
start "ocr-server" /d "%ROOT%" "%ROOT%ocr-server.exe"

start "adapter-rs" /d "%ROOT%" "%ROOT%adapter-rs.exe"

set "ADB_PATH=%ROOT%adb\adb.exe"
set "FRONTEND_DIST_DIR=%ROOT%frontend\dist"
set "ADAPTER_BASE_URL=http://127.0.0.1:8091"
set "OPENCV_BASE_URL=http://127.0.0.1:7771"
set "OCR_BASE_URL=http://127.0.0.1:5005"
start "unified-server" /d "%ROOT%" "%ROOT%unified-server.exe"
start "" "http://127.0.0.1:18080"

echo.
echo All services are starting...
echo UI: http://127.0.0.1:18080
echo Adapter: http://127.0.0.1:8091
echo OpenCV: http://127.0.0.1:7771
echo OCR: http://127.0.0.1:5005
echo.
pause
'@ | Set-Content -LiteralPath $batPath -Encoding ASCII

Write-Step "Package summary"
Get-ChildItem -LiteralPath $OutputDir | Select-Object Name, Length

Write-Ok "Windows bundle is ready: $OutputDir"
