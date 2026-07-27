# ytmusic-bridge

YouTube Music 搜索 + 下载 HTTP API，供 bot 调用。**Go 实现，无第三方运行时依赖。**

流程：bot 转发模糊歌名 → 本服务搜索并返回候选（默认 10，最大 20）→ bot 用 **序号** 或 **歌单显示全名** 选择 → 本服务下载音频并以二进制（或 JSON + `file_url`）返回。

> **Bot 开发者请先读 [`docs/BOT-INTEGRATION.md`](docs/BOT-INTEGRATION.md)**：完整契约、字段语义、错误码、Python/Go/PowerShell 示例与并发行为。

## 技术栈与选型

| 层次 | 选择 | 原因 |
| --- | --- | --- |
| 搜索 | 自研 InnerTube `WEB_REMIX` 客户端（Go 标准库） | 只需 `/youtubei/v1/search`；零依赖；协议对照 [`sigma67/ytmusicapi`](https://github.com/sigma67/ytmusicapi)、[`LuanRT/YouTube.js`](https://github.com/LuanRT/YouTube.js) |
| 下载 | [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) 外部二进制 + ffmpeg | 对 YouTube 变更抗性最强；Go 服务本身不含 Python 依赖 |
| HTTP | 标准库 `net/http` | 不用 gin/echo；每请求一个 goroutine，天生并发 |

详细仓库调研见 [`goal-1/plan.md`](goal-1/plan.md) §3。

## 依赖（必须拷进本项目）

外部工具请**单独复制/下载到本仓库 `bin/`**，不要挂外部工具目录：

| 文件 | 获取方式 |
| --- | --- |
| `bin/yt-dlp.exe` | `.\scripts\get-ytdlp.ps1`（standalone 构建） |
| `bin/ffmpeg.exe` | 拷贝 essentials / standalone 到 `bin/` |
| `bin/ffprobe.exe` | 同上（e2e / 探针用） |

`bin/` 已在 `.gitignore`，不入库。`run.ps1`、服务启动、e2e 均优先使用项目内 `bin/`。

其它：

- Go 1.26+
- 可选：复制 `.env.example` → `.env`
- 默认监听 `127.0.0.1:8787`

## 快速开始

```powershell
cd C:\project\test\youtube-music-api

# 1) 准备项目内工具
.\scripts\get-ytdlp.ps1
# 再把 ffmpeg.exe / ffprobe.exe 拷进 bin/

# 2) 启动（优先 bin/；-Background 不抢焦点）
.\run.ps1
# 或
# .\run.ps1 -Background

# 3) healthz
Invoke-RestMethod http://127.0.0.1:8787/healthz
```

期望响应（实测）：

```json
{"default_limit":10,"max_limit":20,"status":"ok","version":"0.1.0","ytdlp":"2026.07.04"}
```

手动构建：

```powershell
go build -o bin\ytmusic-bridge.exe .\cmd\ytmusic-bridge
.\bin\ytmusic-bridge.exe
```

## 配置表

环境变量优先于 `.env`。完整示例见 [`.env.example`](.env.example)。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | 监听地址 |
| `PORT` | `8787` | 监听端口 |
| `API_KEY` | 空 | 非空时要求请求头 `X-API-Key` |
| `DEFAULT_LIMIT` | `10` | `/search` 未传 `limit` 时默认条数 |
| `MAX_LIMIT` | `20` | 硬上限；配置值小于 20 会被抬到 20 |
| `MIN_SCORE` | `0.0` | 服务端默认相似度下限（请求参数可覆盖） |
| `DOWNLOAD_DIR` | `downloads` | 音频缓存目录 |
| `AUDIO_FORMAT` | `mp3` | `mp3` / `m4a` / `opus` |
| `AUDIO_BITRATE` | `192` | kbps |
| `YTDLP_PATH` | 空 → 自动 `bin/yt-dlp.exe` | yt-dlp 路径 |
| `FFMPEG_LOCATION` | 空 → 自动 `bin/ffmpeg.exe` 或 `bin/` | ffmpeg 路径或目录 |
| `PROXY` | 空 | 可选代理 |
| `COOKIES_FILE` | 空 | 可选 cookies 文件 |
| `MAX_CONCURRENT_DOWNLOADS` | `2` | yt-dlp 并发上限；超出时**排队** |
| `MAX_FILESIZE_MB` | `50` | 单文件上限 → `413 FILE_TOO_LARGE` |
| `DOWNLOAD_TIMEOUT_SECONDS` | `300` | 下载超时 |
| `SESSION_TTL_SECONDS` | `1800` | 搜索会话 TTL（秒） |
| `CACHE_TTL_SECONDS` | `86400` | 音频缓存 TTL |
| `CACHE_MAX_TOTAL_MB` | `2048` | 缓存目录总容量上限 |
| `CLEANUP_INTERVAL_SECONDS` | `300` | 清理周期 |
| `SEARCH_TIMEOUT_SECONDS` | `15` | InnerTube 搜索超时 |

## 接口一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查 + 版本 + yt-dlp 版本 |
| `POST` | `/search` | 模糊搜索 → `session_id` + 候选列表 |
| `POST` | `/download` | 按 `index` / `name` / `video_id` 下载；默认二进制，`?mode=json` 返回元数据 + `file_url` |
| `GET` | `/file/{token}` | 取回缓存文件（支持 Range） |

### limit 规则

- 请求里的 `limit` 优先；省略 → `DEFAULT_LIMIT`（10）
- `limit > MAX_LIMIT`（默认 20）→ **夹紧到 20**（见 `limit_used`）
- `limit < 1` → `400 INVALID_REQUEST`
- 实际可返回条数不足时，有多少返回多少，**不是错误**（见 `total`）

### 错误码

统一 envelope：

```json
{"code":"SESSION_EXPIRED","message":"会话已过期，请重新搜索","detail":null}
```

| HTTP | `code` | 含义 |
| --- | --- | --- |
| 400 | `INVALID_REQUEST` | 参数错误 |
| 401 | `UNAUTHORIZED` | API Key 缺失或错误 |
| 404 | `NOT_FOUND` | index / name / 文件不存在 |
| 409 | `AMBIGUOUS_NAME` | name 匹配多条 |
| 410 | `SESSION_EXPIRED` | 会话过期 |
| 413 | `FILE_TOO_LARGE` | 超过 `MAX_FILESIZE_MB` |
| 499 | `CANCELED` | 客户端取消 |
| 502 | `UPSTREAM_ERROR` | YouTube / yt-dlp 失败 |
| 504 | `TIMEOUT` | 上游超时（含下载排队过久） |
| 500 | `INTERNAL_ERROR` | 未处理的内部错误 |

> **说明**：下载并发打满时**不会立刻返回 429**。超出 `MAX_CONCURRENT_DOWNLOADS` 的请求会**排队**；排队/下载超时映射为 `504 TIMEOUT`。

## PowerShell 示例

```powershell
$Base = "http://127.0.0.1:8787"
# 若配置了 API_KEY：
# $Headers = @{ "X-API-Key" = $env:API_KEY }

# 1) 搜索
$searchBody = @{ query = "lemon kenshi yonezu"; limit = 3 } | ConvertTo-Json
$search = Invoke-RestMethod -Method Post -Uri "$Base/search" `
  -ContentType "application/json; charset=utf-8" -Body $searchBody
$search.results | ForEach-Object { "{0}. {1}  score={2}" -f $_.index, $_.display_name, $_.match_score }

# 2) 按 index 下载
$dlBody = @{ session_id = $search.session_id; index = 1 } | ConvertTo-Json
Invoke-WebRequest -Method Post -Uri "$Base/download" `
  -ContentType "application/json; charset=utf-8" -Body $dlBody -OutFile "out.mp3"

# 3) JSON 模式再取文件
$meta = Invoke-RestMethod -Method Post -Uri "$Base/download?mode=json" `
  -ContentType "application/json; charset=utf-8" -Body $dlBody
Invoke-WebRequest -Uri ($Base + $meta.file_url) -OutFile "out2.mp3"
```

实测搜索样例（`query=lemon kenshi yonezu`，`limit=3`）：

```json
{
  "session_id": "s_93595c3f7bdbe33731b8c26b",
  "query": "lemon kenshi yonezu",
  "limit_requested": 3,
  "limit_used": 3,
  "min_score_used": 0,
  "total": 3,
  "truncated": true,
  "expires_in": 1800,
  "results": [
    {
      "index": 1,
      "display_name": "Lemon - Kenshi Yonezu",
      "title": "Lemon",
      "artists": ["Kenshi Yonezu"],
      "album": "Lemon",
      "duration": "4:17",
      "duration_seconds": 257,
      "video_id": "3NNhrqHZqlI",
      "thumbnail": "https://yt3.googleusercontent.com/...",
      "match_score": 1
    }
  ]
}
```

`POST /download?mode=json` 样例（冷路径 / 带 session 元数据）：

```json
{
  "title": "Lemon",
  "artists": ["Kenshi Yonezu"],
  "display_name": "Lemon - Kenshi Yonezu",
  "video_id": "3NNhrqHZqlI",
  "duration_seconds": 257,
  "format": "mp3",
  "filesize": 6148269,
  "file_url": "/file/2a5367ca562ae7b657b1f1cf33ae4058",
  "expires_in": 85887,
  "cached": true
}
```

注意：若仅用 `video_id` 下载，且缓存条目没有标题元数据，则 `title` / `artists` / `display_name` / `duration_seconds` 可能为空，但 `file_url` / `filesize` / `video_id` 仍可用。

## Python bot 示例

```python
import json
import urllib.request

BASE = "http://127.0.0.1:8787"
# headers = {"X-API-Key": "..."}  # 若启用了 API_KEY

def post_json(path, payload, query=""):
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        BASE + path + query,
        data=data,
        headers={"Content-Type": "application/json; charset=utf-8"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=300) as resp:
        return json.loads(resp.read().decode("utf-8"))

search = post_json("/search", {"query": "lemon kenshi yonezu", "limit": 5, "min_score": 0.35})
print("session:", search["session_id"])
for item in search["results"]:
    print(f'{item["index"]}. {item["display_name"]}  ({item["duration"]})')

meta = post_json(
    "/download",
    {"session_id": search["session_id"], "index": 1},
    query="?mode=json",
)
print("filesize:", meta["filesize"], "url:", meta["file_url"])

with urllib.request.urlopen(BASE + meta["file_url"], timeout=300) as resp:
    open("track.mp3", "wb").write(resp.read())
```

## Go bot 示例

```go
package main

import (
        "bytes"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "os"
        "time"
)

func main() {
        client := &http.Client{Timeout: 5 * time.Minute}
        base := "http://127.0.0.1:8787"

        searchBody, _ := json.Marshal(map[string]any{
                "query": "lemon kenshi yonezu",
                "limit": 5,
        })
        resp, err := client.Post(base+"/search", "application/json; charset=utf-8", bytes.NewReader(searchBody))
        if err != nil {
                panic(err)
        }
        defer resp.Body.Close()
        var search struct {
                SessionID string `json:"session_id"`
                Results   []struct {
                        Index       int    `json:"index"`
                        DisplayName string `json:"display_name"`
                } `json:"results"`
        }
        _ = json.NewDecoder(resp.Body).Decode(&search)
        for _, r := range search.Results {
                fmt.Printf("%d. %s\n", r.Index, r.DisplayName)
        }

        dlBody, _ := json.Marshal(map[string]any{
                "session_id": search.SessionID,
                "index":      1,
        })
        resp2, err := client.Post(base+"/download", "application/json; charset=utf-8", bytes.NewReader(dlBody))
        if err != nil {
                panic(err)
        }
        defer resp2.Body.Close()
        f, _ := os.Create("track.mp3")
        defer f.Close()
        _, _ = io.Copy(f, resp2.Body)
        fmt.Println("saved track.mp3, content-type:", resp2.Header.Get("Content-Type"))
}
```

## 性能数据（G9 实测）

```powershell
.\scripts\e2e.ps1
# 报告：goal-1/e2e-report.json
```

本机快照（query 约 `lemon` / `lemon kenshi yonezu`，项目内 `bin/yt-dlp` + `bin/ffmpeg`）：

| 项目 | 结果 |
| --- | --- |
| index / name / cache 路径 | 通过（ffprobe 约 192 kbps） |
| `/search` 20 并发 × 60 请求 | **QPS ≈ 31.73**，**P50 ≈ 480 ms**，**P99 ≈ 745 ms** |
| 同曲 20 并发冷下载 | wall ≈ **5159 ms**（约一次下载），随后 `post_cached=true`（singleflight） |
| 服务 WorkingSet | ≈ **22.7 MB** |
| healthz | `ytdlp=2026.07.04` |

完整 JSON：[`goal-1/e2e-report.json`](goal-1/e2e-report.json)。本机快照，不是 SLA。

## 缓存与并发

- 搜索会话：分片内存 map + TTL（默认 30 分钟）
- 下载：`singleflight` + 信号量 + 磁盘缓存
- 缓存命中：`X-Cache: hit`，接近瞬时
- 后台清理：session TTL + 缓存 TTL/总容量上限

## 开发检查

```powershell
$env:GOCACHE = "C:\project\test\youtube-music-api\.gocache"
gofmt -l .          # 必须无输出
go build ./...
go vet ./...
$env:YTM_SKIP_LIVE = "1"; go test ./... -count=1
go test -bench=. ./internal/matching ./internal/session
```

## 合规

仅供个人学习 / 私有 bot 使用。请遵守 YouTube ToS 与当地版权法。默认只绑定 `127.0.0.1`。

任务日志：[`goal-1/tasks.md`](goal-1/tasks.md)。
