# ytmusic-bridge

YouTube Music 搜索 + 下载 HTTP API，供 bot 调用。**Go 实现，零第三方运行期依赖。**

bot 发来模糊歌名 → 本服务搜索并返回候选歌单（默认 10 条，最多 20 条）→ bot 用**序号**或**歌名全称**选歌 → 本服务下载音频并回传二进制。

> **bot 开发者请直接看 [`docs/BOT-INTEGRATION.md`](docs/BOT-INTEGRATION.md)** —— 完整接口契约、字段说明、错误码、Python/Go/PowerShell 示例、并发行为。

## 技术选型

| 用途 | 方案 | 理由 |
| --- | --- | --- |
| 搜索 | 自研 InnerTube `WEB_REMIX` 客户端（Go 标准库） | 只需 `/youtubei/v1/search` 一个端点，零依赖、连接池与超时完全可控；协议细节对照 [`sigma67/ytmusicapi`](https://github.com/sigma67/ytmusicapi)（2.8k★）与 [`LuanRT/YouTube.js`](https://github.com/LuanRT/YouTube.js)（5k★） |
| 下载 | [`yt-dlp`](https://github.com/yt-dlp/yt-dlp)（外部二进制，180k★）+ ffmpeg | 抗 YouTube 变更能力最强，可独立升级；Go 服务本体不含 Python 依赖 |
| Web 框架 | 标准库 `net/http` | 不引 gin/echo，减少依赖、每请求一 goroutine 天生并发 |

备选与落选理由（Go 生态调研）见 [`goal-1/plan.md`](goal-1/plan.md) §3。

## 环境要求

- Go 1.26+
- ffmpeg / ffprobe（转码）
- yt-dlp（下载；后续由 `scripts/get-ytdlp.ps1` 下载到项目 `bin/`，不改系统 PATH）

## 快速开始

```powershell
cd C:\project\test\youtube-music-api
# ????? yt-dlp ? bin/
# .\scripts\get-ytdlp.ps1
# ???????-Background ?????????
.\run.ps1
# ??
# .\run.ps1 -Background
Invoke-RestMethod http://127.0.0.1:8787/healthz
```powershell
gofmt -l .          # 必须无输出
go build ./...
go vet ./...
go test ./...
go test -bench=. ./...
```

## 实现进度

- [x] 项目骨架 + 配置模块（`internal/config`）
- [x] bot 接入文档（`docs/BOT-INTEGRATION.md`）
- [x] 匹配层 `internal/matching`（`display_name` / `match_score`）
- [x] InnerTube 搜索客户端 `internal/ytmusic`
- [x] 搜索服务层 `internal/search`
- [x] 会话与选歌层 `internal/session`
- [x] 下载层 `internal/download`（yt-dlp + singleflight + 限流）
- [x] HTTP API 层 `internal/httpapi`
- [ ] 端到端联调 + 压测

逐任务进度与验证记录见 [`goal-1/tasks.md`](goal-1/tasks.md)。

## 合规声明

仅供个人学习与私人使用。请遵守 YouTube 服务条款及所在地版权法律，勿用于公开分发或商业用途。默认只监听 `127.0.0.1`。
