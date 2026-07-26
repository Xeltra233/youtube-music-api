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
go build -o bin\ytmusic-bridge.exe .\cmd\ytmusic-bridge
.\bin\ytmusic-bridge.exe

# 另开一个窗口探活
Invoke-RestMethod http://127.0.0.1:8787/healthz
```

## 配置

配置来源优先级：**环境变量 > `.env` 文件 > 默认值**。数值项写错会直接启动失败并打印原因（不静默回落）。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HOST` / `PORT` | `127.0.0.1` / `8787` | 监听地址。默认只听本机 |
| `API_KEY` | 空 | 非空时校验请求头 `X-API-Key` |
| `DEFAULT_LIMIT` | `10` | bot 未指定 `limit` 时返回的条数 |
| `MAX_LIMIT` | `20` | 单次上限。**低于 20 会被自动抬到 20** |
| `MIN_SCORE` | `0.0` | 相似度下限默认值，`0` = 不过滤 |
| `AUDIO_FORMAT` / `AUDIO_BITRATE` | `mp3` / `192` | 输出格式（`mp3`/`m4a`/`opus`）与码率 |
| `DOWNLOAD_DIR` | `downloads` | 缓存目录，启动时自动创建 |
| `FFMPEG_LOCATION` / `YTDLP_PATH` | 空 | 外部程序路径，留空则从 PATH 查找 |
| `PROXY` / `COOKIES_FILE` | 空 | 直连不可用时的逃生口 |
| `MAX_CONCURRENT_DOWNLOADS` | `2` | 同时运行的 yt-dlp 数量（多人使用时调高） |
| `MAX_FILESIZE_MB` | `50` | 单文件上限，超出返回 `413` |
| `DOWNLOAD_TIMEOUT_SECONDS` | `300` | 下载 + 转码总超时 |
| `SESSION_TTL_SECONDS` | `1800` | 搜索会话有效期 |
| `CACHE_TTL_SECONDS` / `CACHE_MAX_TOTAL_MB` | `86400` / `2048` | 音频缓存保留时长与目录总量上限 |
| `CLEANUP_INTERVAL_SECONDS` | `300` | 后台清理周期 |
| `SEARCH_TIMEOUT_SECONDS` | `15` | 搜索上游超时 |

## 接口一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 探活，返回版本与 limit 配置 |
| `POST` | `/search` | 模糊搜索，返回带 `index` / `display_name` / `match_score` 的候选列表 + `session_id` |
| `POST` | `/download` | 按 `video_id` / `index` / `name` 选歌，返回音频二进制（`?mode=json` 改回 JSON） |
| `GET` | `/file/{token}` | 取回缓存音频，支持 `Range` |

字段级契约见 [`docs/BOT-INTEGRATION.md`](docs/BOT-INTEGRATION.md)。

## 开发

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
- [ ] 匹配层 `internal/matching`（`display_name` / `match_score`）
- [ ] InnerTube 搜索客户端 `internal/ytmusic`
- [ ] 搜索服务层 `internal/search`
- [ ] 会话与选歌层 `internal/session`
- [ ] 下载层 `internal/download`（yt-dlp + singleflight + 限流）
- [ ] HTTP API 层 `internal/httpapi`
- [ ] 端到端联调 + 压测

逐任务进度与验证记录见 [`goal-1/tasks.md`](goal-1/tasks.md)。

## 合规声明

仅供个人学习与私人使用。请遵守 YouTube 服务条款及所在地版权法律，勿用于公开分发或商业用途。默认只监听 `127.0.0.1`。
