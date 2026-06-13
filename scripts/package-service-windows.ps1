param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('adapter', 'main', 'opencv', 'ocr')]
    [string]$Service,
    [string]$OutputDir = '',
    [switch]$SkipFrontendBuild,
    [switch]$SkipBundleSync
)

$ErrorActionPreference = 'Stop'

function Write-Step {
    param([string]$Message)
    Write-Host ''
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host "[OK] $Message" -ForegroundColor Green
}

function Write-Warn {
    param([string]$Message)
    Write-Host "[WARN] $Message" -ForegroundColor Yellow
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
    $venvPython = Join-Path $ProjectDir '.venv\Scripts\python.exe'
    if (Test-Path -LiteralPath $venvPython) {
        return $venvPython
    }
    return 'python'
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
        if ([System.IO.Path]::GetFullPath($Source) -eq [System.IO.Path]::GetFullPath($Destination)) {
            return
        }
        $parent = Split-Path -Parent $Destination
        if ($parent) {
            New-Item -ItemType Directory -Force -Path $parent | Out-Null
        }
        Copy-Item -LiteralPath $Source -Destination $Destination -Recurse -Force
    }
}

function Invoke-AdapterBuild {
    param([string]$OutputDir)

    $releaseExe = '.\target\release\adapter-rs.exe'
    $debugExe = '.\target\debug\adapter-rs.exe'

    Write-Step 'Building adapter-rs.exe (release)'
    cargo build -p adapter-rs --release
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $releaseExe)) {
        Copy-Item -LiteralPath $releaseExe -Destination (Join-Path $OutputDir 'adapter-rs.exe') -Force
        Write-Ok 'adapter-rs release build succeeded'
        return
    }

    Write-Warn "adapter-rs release build failed with exit code $LASTEXITCODE, retrying once with single job"
    cargo build -p adapter-rs --release -j 1
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $releaseExe)) {
        Copy-Item -LiteralPath $releaseExe -Destination (Join-Path $OutputDir 'adapter-rs.exe') -Force
        Write-Ok 'adapter-rs release build succeeded on retry'
        return
    }

    Write-Warn "adapter-rs release build still failed with exit code $LASTEXITCODE, falling back to debug build"
    cargo build -p adapter-rs
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $debugExe)) {
        Copy-Item -LiteralPath $debugExe -Destination (Join-Path $OutputDir 'adapter-rs.exe') -Force
        Write-Warn 'adapter-rs packaged from debug build because release build was unavailable'
        return
    }

    throw "adapter build failed in both release and debug modes (last exit code: $LASTEXITCODE)"
}

function Sync-PackageToBundle {
    param(
        [string]$Service,
        [string]$PackageDir,
        [string]$BundleDir,
        [string]$ProjectRoot
    )

    if ([string]::IsNullOrWhiteSpace($BundleDir)) {
        return
    }

    New-Item -ItemType Directory -Force -Path $BundleDir | Out-Null

    switch ($Service) {
        'adapter' {
            Copy-IfExists -Source (Join-Path $PackageDir 'adapter-rs.exe') -Destination (Join-Path $BundleDir 'adapter-rs.exe')
        }
        'opencv' {
            Copy-IfExists -Source (Join-Path $PackageDir 'opencv-server.exe') -Destination (Join-Path $BundleDir 'opencv-server.exe')
        }
        'ocr' {
            Copy-IfExists -Source (Join-Path $PackageDir 'ocr-server.exe') -Destination (Join-Path $BundleDir 'ocr-server.exe')
        }
        'main' {
            Copy-IfExists -Source (Join-Path $PackageDir 'unified-server.exe') -Destination (Join-Path $BundleDir 'unified-server.exe')
            Copy-IfExists -Source (Join-Path $PackageDir 'frontend\dist') -Destination (Join-Path $BundleDir 'frontend\dist')
            Copy-IfExists -Source (Join-Path $PackageDir 'adb') -Destination (Join-Path $BundleDir 'adb')
            New-Item -ItemType Directory -Force -Path (Join-Path $BundleDir '.runtime') | Out-Null
            New-Item -ItemType Directory -Force -Path (Join-Path $BundleDir 'logs') | Out-Null
            Copy-IfExists -Source (Join-Path $ProjectRoot 'dist\windows-bundle\start-all.bat') -Destination (Join-Path $BundleDir 'start-all.bat')
        }
    }
}

$ProjectRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path $ProjectRoot ("dist\{0}" -f $Service)
}
$OutputDir = [System.IO.Path]::GetFullPath($OutputDir)
$BundleDir = Join-Path $ProjectRoot 'dist\windows-bundle'

Write-Step "Preparing output directory for $Service"
Reset-Dir -Path $OutputDir

switch ($Service) {
    'adapter' {
        Write-Step 'Checking toolchain'
        Ensure-Command -Name 'cargo'

        Push-Location (Join-Path $ProjectRoot 'adapter-rs')
        try {
            Invoke-AdapterBuild -OutputDir $OutputDir
        }
        finally {
            Pop-Location
        }
    }

    'main' {
        Write-Step 'Checking toolchain'
        Ensure-Command -Name 'go'
        Ensure-Command -Name 'npm'

        if (-not $SkipFrontendBuild) {
            Write-Step 'Building frontend dist'
            Push-Location (Join-Path $ProjectRoot 'frontend')
            try {
                npm run build
                if ($LASTEXITCODE -ne 0) {
                    throw "frontend build failed with exit code $LASTEXITCODE"
                }
            }
            finally {
                Pop-Location
            }
        }
        else {
            Write-Step 'Skipping frontend build'
        }

        Write-Step 'Building unified-server.exe'
        Push-Location $ProjectRoot
        try {
            go build -trimpath -ldflags '-s -w' -o (Join-Path $OutputDir 'unified-server.exe') .\cmd\unified-server
            if ($LASTEXITCODE -ne 0) {
                throw "main business build failed with exit code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }

        Write-Step 'Copying main business runtime assets'
        Copy-IfExists -Source (Join-Path $ProjectRoot 'frontend\dist') -Destination (Join-Path $OutputDir 'frontend\dist')
        Copy-IfExists -Source (Join-Path $ProjectRoot 'adb') -Destination (Join-Path $OutputDir 'adb')
        New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir '.runtime') | Out-Null
        New-Item -ItemType Directory -Force -Path (Join-Path $OutputDir 'logs') | Out-Null
    }

    'opencv' {
        Write-Step 'Checking toolchain'
        Ensure-Command -Name 'python'
        $pythonExe = Resolve-PythonForProject -ProjectDir (Join-Path $ProjectRoot 'opencv-server')
        Ensure-PyInstallerModule -PythonExe $pythonExe

        Write-Step 'Packaging opencv-server.exe'
        Push-Location (Join-Path $ProjectRoot 'opencv-server')
        try {
            & $pythonExe -m PyInstaller --noconfirm --clean --onefile --name opencv-server `
                --paths . `
                --add-data 'static;static' `
                run_opencv.py
            if ($LASTEXITCODE -ne 0) {
                throw "opencv-server pyinstaller failed with exit code $LASTEXITCODE"
            }
            Copy-Item -LiteralPath '.\dist\opencv-server.exe' -Destination (Join-Path $OutputDir 'opencv-server.exe') -Force
        }
        finally {
            Pop-Location
        }
    }

    'ocr' {
        Write-Step 'Checking toolchain'
        Ensure-Command -Name 'python'
        $pythonExe = Resolve-PythonForProject -ProjectDir (Join-Path $ProjectRoot 'ocr-server')
        Ensure-PyInstallerModule -PythonExe $pythonExe

        Write-Step 'Packaging ocr-server.exe'
        Push-Location (Join-Path $ProjectRoot 'ocr-server')
        try {
            & $pythonExe -m PyInstaller --noconfirm --clean --onefile --name ocr-server `
                --paths . `
                --collect-submodules onnxocr `
                --collect-data onnxocr `
                --collect-all flask `
                --collect-all werkzeug `
                --collect-all jinja2 `
                --collect-all click `
                --collect-all itsdangerous `
                --collect-all blinker `
                --add-data 'app-service.py;.' `
                --add-data 'templates;templates' `
                --add-data 'static;static' `
                --add-data 'onnxocr;onnxocr' `
                run_ocr.py
            if ($LASTEXITCODE -ne 0) {
                throw "ocr-server pyinstaller failed with exit code $LASTEXITCODE"
            }
            Copy-Item -LiteralPath '.\dist\ocr-server.exe' -Destination (Join-Path $OutputDir 'ocr-server.exe') -Force
        }
        finally {
            Pop-Location
        }
    }
}

Write-Step 'Package summary'
Get-ChildItem -LiteralPath $OutputDir | Select-Object Name, Length
Write-Ok ("{0} package is ready: {1}" -f $Service, $OutputDir)

if (-not $SkipBundleSync) {
    Write-Step "Syncing $Service package into windows-bundle"
    Sync-PackageToBundle -Service $Service -PackageDir $OutputDir -BundleDir $BundleDir -ProjectRoot $ProjectRoot
    Write-Ok ("windows-bundle updated: {0}" -f $BundleDir)
}
