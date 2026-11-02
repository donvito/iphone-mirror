# Builds dist\iPhoneMirror.exe (no console window). Requires Go and a 64-bit
# MinGW gcc on PATH (cgo is used for the FairPlay code).
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$env:CGO_ENABLED = "1"
New-Item -ItemType Directory -Force dist | Out-Null
# Embed the manifest (modern control styles, per-monitor DPI) if windres is available;
# otherwise the committed rsrc_windows_amd64.syso is used as is.
if (Get-Command windres -ErrorAction SilentlyContinue) {
    Push-Location res
    windres -O coff -F pe-x86-64 -i app.rc -o ..\rsrc_windows_amd64.syso
    Pop-Location
}
go build -trimpath -ldflags "-H windowsgui -s -w" -o dist\iPhoneMirror.exe .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built dist\iPhoneMirror.exe"
