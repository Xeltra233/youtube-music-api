# Build and start ytmusic-bridge.
# Usage:
#   .\run.ps1              # foreground
#   .\run.ps1 -Background  # hidden window, no focus steal
[CmdletBinding()]
param(
    [switch]$Background,
    [switch]$SkipYtdlpCheck,
    [string]$Port = ""
)

$ErrorActionPreference = "Stop"
$Root = $PSScriptRoot
Set-Location -LiteralPath $Root

$BinDir = Join-Path $Root "bin"
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$Exe = Join-Path $BinDir "ytmusic-bridge.exe"

# Optional: ensure local yt-dlp exists.
$Ytdlp = Join-Path $BinDir "yt-dlp.exe"
if (-not $SkipYtdlpCheck) {
    $has = $false
    if ($env:YTDLP_PATH -and (Test-Path -LiteralPath $env:YTDLP_PATH)) { $has = $true }
    if (Test-Path -LiteralPath $Ytdlp) { $has = $true }
    if (-not $has) {
        $cmd = Get-Command yt-dlp -ErrorAction SilentlyContinue
        if ($cmd) { $has = $true }
    }
    if (-not $has) {
        Write-Host "yt-dlp not found; downloading to bin\ via scripts\get-ytdlp.ps1"
        & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $Root "scripts\get-ytdlp.ps1")
    }
}

Write-Host "Building $Exe"
& go build -o $Exe .\cmd\ytmusic-bridge
if ($LASTEXITCODE -ne 0) { throw "go build failed: $LASTEXITCODE" }

if ($Port) {
    $env:PORT = $Port
}

if ($Background) {
    # Hidden window, no focus steal.
    $p = Start-Process -FilePath $Exe -WorkingDirectory $Root -WindowStyle Hidden -PassThru
    Write-Host "started pid=$($p.Id) (hidden). healthz: http://127.0.0.1:$(if($env:PORT){$env:PORT}else{8787})/healthz"
    return
}

Write-Host "Starting (foreground). Ctrl+C to stop."
& $Exe
