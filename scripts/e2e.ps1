# Live end-to-end + performance bench for ytmusic-bridge (R11 evidence).
# Usage:
#   .\scripts\e2e.ps1
#   .\scripts\e2e.ps1 -Base http://127.0.0.1:8787 -SkipStart
[CmdletBinding()]
param(
    [string]$Base = "http://127.0.0.1:8787",
    [string]$Query = "lemon kenshi yonezu",
    [switch]$SkipStart,
    [switch]$KeepServer,
    [int]$SearchConcurrency = 20,
    [int]$SearchRequests = 60,
    [int]$DownloadConcurrency = 20,
    [string]$Out = "goal-1/e2e-report.json"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $Root

$env:GOCACHE = Join-Path $Root ".gocache"
$env:YTDLP_PATH = Join-Path $Root "bin\yt-dlp.exe"
$env:FFMPEG_LOCATION = Join-Path $Root "bin"
$env:FFPROBE_PATH = Join-Path $Root "bin\ffprobe.exe"

if (-not (Test-Path -LiteralPath $env:YTDLP_PATH)) {
    Write-Host "yt-dlp missing at bin\yt-dlp.exe; run scripts\get-ytdlp.ps1 or copy a standalone yt-dlp.exe into bin\"
    throw "YTDLP_PATH missing: $($env:YTDLP_PATH)"
}

if (-not (Test-Path -LiteralPath $env:FFPROBE_PATH)) {
    Write-Host "ffprobe missing at bin\ffprobe.exe; copy standalone ffmpeg/ffprobe into bin\"
    throw "FFPROBE_PATH missing: $($env:FFPROBE_PATH)"
}
if (-not (Test-Path -LiteralPath (Join-Path $Root "bin\ffmpeg.exe"))) {
    Write-Host "ffmpeg missing at bin\ffmpeg.exe; copy standalone ffmpeg/ffprobe into bin\"
    throw "ffmpeg missing in project bin\"
}

$argsList = @(
    "run", "./cmd/e2e",
    "-base", $Base,
    "-query", $Query,
    "-search-concurrency", "$SearchConcurrency",
    "-search-requests", "$SearchRequests",
    "-download-concurrency", "$DownloadConcurrency",
    "-out", $Out,
    "-ffprobe", $env:FFPROBE_PATH
)
if ($SkipStart) { $argsList += "-skip-start" }
if ($KeepServer) { $argsList += "-keep-server" }

Write-Host "Running: go $($argsList -join ' ')"
& go @argsList
if ($LASTEXITCODE -ne 0) {
    throw "e2e failed with exit code $LASTEXITCODE"
}
Write-Host "e2e completed successfully"
