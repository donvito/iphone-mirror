# Allows iPhone Mirror through Windows Defender Firewall (needs admin; will ask to elevate).
# The iPhone must be able to reach this PC on: UDP 5353 (discovery), TCP 7000 (AirPlay)
# and a few random TCP/UDP ports for the video stream, so the rule is per-program.
param([string]$ExePath = "")

if (-not $ExePath) {
    # Release zip: the exe is next to this script. Source checkout: dist\ after .\build.ps1.
    $ExePath = Join-Path $PSScriptRoot "iPhoneMirror.exe"
    if (-not (Test-Path $ExePath)) { $ExePath = Join-Path $PSScriptRoot "dist\iPhoneMirror.exe" }
}
$ExePath = [IO.Path]::GetFullPath($ExePath)
if (-not (Test-Path $ExePath)) {
    Write-Error "Not found: $ExePath (build it first with .\build.ps1, or pass -ExePath)"
    exit 1
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -ExePath `"$ExePath`""
    exit
}

$name = "iPhone Mirror"
Get-NetFirewallRule -DisplayName "$name*" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName "$name (TCP)" -Direction Inbound -Program $ExePath -Protocol TCP -Action Allow -Profile Any | Out-Null
New-NetFirewallRule -DisplayName "$name (UDP)" -Direction Inbound -Program $ExePath -Protocol UDP -Action Allow -Profile Any | Out-Null
Write-Host "Firewall rules added for $ExePath"
Start-Sleep -Seconds 2
