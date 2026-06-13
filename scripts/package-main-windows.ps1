param(
    [string]$OutputDir = '',
    [switch]$SkipFrontendBuild
)

& (Join-Path $PSScriptRoot 'package-service-windows.ps1') -Service main -OutputDir $OutputDir -SkipFrontendBuild:$SkipFrontendBuild
