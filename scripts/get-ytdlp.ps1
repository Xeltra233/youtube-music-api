# Download yt-dlp into project bin/ (does not modify system PATH).
# Usage: powershell -NoProfile -File scripts\get-ytdlp.ps1
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) {
    $OutDir = Join-Path $Root "bin"
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

if ($env:OS -like "*Windows*" -or $IsWindows) {
    $Asset = "yt-dlp.exe"
} else {
    $Asset = "yt-dlp"
}
$Dest = Join-Path $OutDir $Asset

if ($Version -eq "latest") {
    $URL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/$Asset"
} else {
    $URL = "https://github.com/yt-dlp/yt-dlp/releases/download/$Version/$Asset"
}

Write-Host "Downloading $URL"
Write-Host "       -> $Dest"
# -UseBasicParsing avoids IE engine; no focus grab.
Invoke-WebRequest -Uri $URL -OutFile $Dest -UseBasicParsing
if (-not (Test-Path -LiteralPath $Dest)) {
    throw "download failed: $Dest missing"
}

# Ensure executable bit on non-Windows if present.
try {
    if ($Asset -eq "yt-dlp" -and (Get-Command chmod -ErrorAction SilentlyContinue)) {
        & chmod +x $Dest
    }
} catch {}

Write-Host "OK: $Dest"
try {
    $ver = & $Dest --version 2>$null
    if ($ver) { Write-Host "yt-dlp version: $ver" }
} catch {
    Write-Host "downloaded, but --version failed: $_"
}
