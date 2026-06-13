param(
    [string]$OutputDir = ''
)

& (Join-Path $PSScriptRoot 'package-service-windows.ps1') -Service opencv -OutputDir $OutputDir
