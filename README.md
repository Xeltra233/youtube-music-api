# ytmusic-bridge

YouTube Music search + download HTTP API for bots. **Implemented in Go with no third-party runtime deps.**

Bot sends a fuzzy song name -> this service searches and returns candidates (default 10, max 20) -> bot picks by **index** or **full display name** -> this service downloads audio and returns binary.

> **Bot developers: read [`docs/BOT-INTEGRATION.md`](docs/BOT-INTEGRATION.md)** for the full contract, fields, error codes, Python/Go/PowerShell samples, and concurrency notes.

## Stack

| Area | Choice | Why |
| --- | --- | --- |
| Search | Homegrown InnerTube `WEB_REMIX` client (Go stdlib) | Only needs `/youtubei/v1/search`; zero deps; protocol checked against [`sigma67/ytmusicapi`](https://github.com/sigma67/ytmusicapi) and [`LuanRT/YouTube.js`](https://github.com/LuanRT/YouTube.js) |
| Download | [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) external binary + ffmpeg | Best resilience to YouTube changes; Go service itself has no Python dependency |
| HTTP | stdlib `net/http` | No gin/echo; one goroutine per request |

See [`goal-1/plan.md`](goal-1/plan.md) section 3 for Go-ecosystem survey notes.

## Requirements

- Go 1.26+
- **Project-local tools under `bin/` (copy/download into this project; do not mount external tool directories)**
  - `bin/yt-dlp.exe`: `.\scripts\get-ytdlp.ps1` (standalone build)
  - `bin/ffmpeg.exe` + `bin/ffprobe.exe`: copy standalone/essentials binaries into `bin/`
- Optional: copy `.env.example` to `.env`; default listen `127.0.0.1:8787`

## Quick start

```powershell
cd C:\project\test\youtube-music-api

# 1) project-local tools (bin/ is gitignored)
.\scripts\get-ytdlp.ps1
# also copy ffmpeg.exe / ffprobe.exe into bin\

# 2) start (prefers bin/ tools; -Background avoids focus steal)
.\run.ps1
# or
# .\run.ps1 -Background

Invoke-RestMethod http://127.0.0.1:8787/healthz
```

## E2E + benchmarks (R11)

```powershell
.\scripts\e2e.ps1
# report: goal-1/e2e-report.json
```

Local snapshot (query=`lemon kenshi yonezu`, project-local `bin/yt-dlp` + `bin/ffmpeg`):

| Item | Result |
| --- | --- |
| index / name / cache paths | pass (ffprobe duration/bitrate ~192 kbps) |
| `/search` 20 concurrent x 60 requests | **QPS ~ 31.7**, **P50 ~ 480 ms**, **P99 ~ 745 ms** |
| same-song 20 concurrent downloads (cold) | wall ~ **5.2 s** (~one download), then `cached=true` (singleflight) |
| server WorkingSet | ~ **22.7 MB** |

Full JSON: [`goal-1/e2e-report.json`](goal-1/e2e-report.json).

## Dev checks

```powershell
gofmt -l .          # must be empty
go build ./...
go vet ./...
go test ./...
go test -bench=. ./internal/matching ./internal/session
```

## Progress

- [x] skeleton + config (`internal/config`)
- [x] bot integration doc (`docs/BOT-INTEGRATION.md`)
- [x] matching (`internal/matching`: `display_name` / `match_score`)
- [x] InnerTube search client (`internal/ytmusic`)
- [x] search service (`internal/search`)
- [x] session + select (`internal/session`)
- [x] download layer (`internal/download`: yt-dlp + singleflight + limits)
- [x] HTTP API (`internal/httpapi`)
- [x] e2e + benchmarks (`cmd/e2e` / `scripts/e2e.ps1`)

Task log: [`goal-1/tasks.md`](goal-1/tasks.md).

## Compliance

Personal learning / private use only. Follow YouTube ToS and local copyright law. Default bind is `127.0.0.1`.
