# Builds dist\iPhoneMirror.exe (no console window). Requires Go and a 64-bit
# MinGW gcc on PATH (cgo is used for the FairPlay code).
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$env:CGO_ENABLED = "1"
New-Item -ItemType Directory -Force dist | Out-Null
go build -trimpath -ldflags "-H windowsgui -s -w" -o dist\iPhoneMirror.exe .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built dist\iPhoneMirror.exe"
