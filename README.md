# ytmusic-bridge

面向 bot 的 YouTube Music **搜索 + 下载** HTTP API。**Go 实现，运行时不依赖第三方 Python 包。**

典型流程：

1. bot 把模糊歌名转发给本服务
2. 本服务搜索并返回候选（默认 10 条，最多 20 条）
3. bot 用 **序号** 或 **歌单显示全名** 选定一首
4. 本服务下载音频，并以二进制（或 JSON + `file_url`）返回

> **Bot 开发者请先读 [`docs/BOT-INTEGRATION.md`](docs/BOT-INTEGRATION.md)**：完整契约、字段语义、错误码、Python / Go / PowerShell 示例，以及并发行为说明。
>
> 用户命令尾参（`mp3` / `m4a` / `opus` / `file` / `voice`）与 API 字段映射见 [`docs/BOT-PARAMS.txt`](docs/BOT-PARAMS.txt)。

## 技术栈与选型

| 层次 | 选择 | 原因 |
| --- | --- | --- |
| 搜索 | 自研 InnerTube `WEB_REMIX` 客户端（Go 标准库） | 只调用 `/youtubei/v1/search`；零依赖；协议对照 [`sigma67/ytmusicapi`](https://github.com/sigma67/ytmusicapi)、[`LuanRT/YouTube.js`](https://github.com/LuanRT/YouTube.js) |
| 下载 | 外部 [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) + ffmpeg | 对 YouTube 协议变更跟进更快；Go 服务本身不捆绑 Python 依赖 |
| HTTP | 标准库 `net/http` | 不引入 gin / echo；每个请求一个 goroutine，天然支持并发 |

更完整的仓库调研见 [`goal-1/plan.md`](goal-1/plan.md) 第 3 节。

## 依赖（必须拷进本项目）

外部工具请**单独复制或下载到本仓库 `bin/`**，不要挂载外部工具目录：

| 文件 | 获取方式 |
| --- | --- |
| `bin/yt-dlp.exe` | 运行 `.\scripts\get-ytdlp.ps1`（下载独立构建版） |
| `bin/ffmpeg.exe` | 把精简版 / 独立版拷贝到 `bin/` |
| `bin/ffprobe.exe` | 同上（端到端测试与探针会用到） |

`bin/` 已写入 `.gitignore`，不会入库。`run.ps1`、服务启动与端到端脚本都会优先使用项目内 `bin/`。

其它要求：

- Go 1.26+
- 可选：复制 `.env.example` 为 `.env`
- 默认监听 `127.0.0.1:8787`

## 快速开始

```powershell
cd C:\project\test\youtube-music-api

# 1) 准备项目内工具
.\scripts\get-ytdlp.ps1
# 再把 ffmpeg.exe / ffprobe.exe 拷进 bin/

# 2) 启动（优先使用 bin/；-Background 后台启动，不抢焦点）
.\run.ps1
# 或
# .\run.ps1 -Background

# 3) 健康检查
Invoke-RestMethod http://127.0.0.1:8787/healthz
```

期望响应（实测样例）：

```json
{"default_limit":10,"max_limit":20,"status":"ok","version":"0.1.0","ytdlp":"2026.07.04"}
```

手动构建：

```powershell
go build -o bin\ytmusic-bridge.exe .\cmd\ytmusic-bridge
.\bin\ytmusic-bridge.exe
```

## 配置说明

环境变量优先于 `.env`。完整示例见 [`.env.example`](.env.example)。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | 监听地址 |
| `PORT` | `8787` | 监听端口 |
| `API_KEY` | 空 | 非空时要求请求头 `X-API-Key` |
| `DEFAULT_LIMIT` | `10` | `/search` 未传 `limit` 时的默认条数 |
| `MAX_LIMIT` | `20` | 硬上限；配置值小于 20 时会被抬到 20 |
| `MIN_SCORE` | `0.0` | 服务端默认相似度下限（请求参数可覆盖） |
| `DOWNLOAD_DIR` | `downloads` | 音频缓存目录 |
| `AUDIO_FORMAT` | `mp3` | 支持 `mp3` / `m4a` / `opus` |
| `AUDIO_BITRATE` | `192` | 码率（kbps） |
| `YTDLP_PATH` | 空 → 自动 `bin/yt-dlp.exe` | yt-dlp 可执行文件路径 |
| `FFMPEG_LOCATION` | 空 → 自动 `bin/ffmpeg.exe` 或 `bin/` | ffmpeg 可执行文件路径或所在目录 |
| `PROXY` | 空 | 可选代理 |
| `COOKIES_FILE` | 空 | 可选 cookies 文件路径 |
| `MAX_CONCURRENT_DOWNLOADS` | `2` | yt-dlp 并发上限；超出后**排队** |
| `MAX_FILESIZE_MB` | `50` | 单文件上限；超限返回 `413 FILE_TOO_LARGE` |
| `DOWNLOAD_TIMEOUT_SECONDS` | `300` | 下载超时（秒） |
| `SESSION_TTL_SECONDS` | `1800` | 搜索会话有效期（秒） |
| `CACHE_TTL_SECONDS` | `86400` | 音频缓存有效期（秒） |
| `CACHE_MAX_TOTAL_MB` | `2048` | 缓存目录总容量上限（MB） |
| `CLEANUP_INTERVAL_SECONDS` | `300` | 后台清理周期（秒） |
| `SEARCH_TIMEOUT_SECONDS` | `15` | InnerTube 搜索超时（秒） |

## 接口一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查，返回版本与 yt-dlp 版本 |
| `POST` | `/search` | 模糊搜索，返回 `session_id` 与候选列表 |
| `POST` | `/download` | 按 `index` / `name` / `video_id` 下载；默认返回二进制，`?mode=json` 返回元数据与 `file_url` |
| `GET` | `/file/{token}` | 取回缓存文件（支持 Range） |

### `limit` 规则

- 请求中的 `limit` 优先；省略时使用 `DEFAULT_LIMIT`（10）
- `limit > MAX_LIMIT`（默认 20）时，**夹紧到 20**（见响应字段 `limit_used`）
- `limit < 1` 时返回 `400 INVALID_REQUEST`
- 实际上游可返回条数不足时，有多少返回多少，**不算错误**（见响应字段 `total`）

### 错误码

统一错误结构：

```json
{"code":"SESSION_EXPIRED","message":"会话已过期，请重新搜索","detail":null}
```

| HTTP | `code` | 含义 |
| --- | --- | --- |
| 400 | `INVALID_REQUEST` | 参数错误 |
| 401 | `UNAUTHORIZED` | API Key 缺失或错误 |
| 404 | `NOT_FOUND` | index / name / 文件不存在 |
| 409 | `AMBIGUOUS_NAME` | `name` 匹配到多条 |
| 410 | `SESSION_EXPIRED` | 会话过期 |
| 413 | `FILE_TOO_LARGE` | 超过 `MAX_FILESIZE_MB` |
| 499 | `CANCELED` | 客户端取消 |
| 502 | `UPSTREAM_ERROR` | YouTube / yt-dlp 上游失败 |
| 504 | `TIMEOUT` | 上游超时（含下载排队过久） |
| 500 | `INTERNAL_ERROR` | 未处理的内部错误 |

> **说明**：下载并发打满时**不会立刻返回 429**。超出 `MAX_CONCURRENT_DOWNLOADS` 的请求会**排队**；排队或下载超时映射为 `504 TIMEOUT`。

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

`POST /download?mode=json` 样例（冷路径 / 带会话元数据）：

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

注意：如果只用 `video_id` 下载，且缓存条目没有标题元数据，则 `title` / `artists` / `display_name` / `duration_seconds` 可能为空；但 `file_url` / `filesize` / `video_id` 仍然可用。

## Python 示例

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

## Go 示例

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

本机快照（查询词约 `lemon` / `lemon kenshi yonezu`，使用项目内 `bin/yt-dlp` + `bin/ffmpeg`）：

| 项目 | 结果 |
| --- | --- |
| index / name / 缓存路径 | 通过（ffprobe 约 192 kbps） |
| `/search` 20 并发 × 60 请求 | **QPS ≈ 31.73**，**P50 ≈ 480 ms**，**P99 ≈ 745 ms** |
| 同曲 20 并发冷下载 | 总耗时约 **5159 ms**（接近一次真实下载），随后 `post_cached=true`（`singleflight` 合并重复下载） |
| 服务工作集 | 约 **22.7 MB** |
| 健康检查 | `ytdlp=2026.07.04` |

完整 JSON 见 [`goal-1/e2e-report.json`](goal-1/e2e-report.json)。以上是本机快照，**不是**服务等级承诺。

## 缓存与并发

- 搜索会话：分片内存 map + 过期时间（默认 30 分钟）
- 下载：`singleflight` 合并重复请求 + 信号量限流 + 磁盘缓存
- 缓存命中：响应头 `X-Cache: hit`，接近瞬时返回
- 后台清理：按会话过期时间、缓存过期时间与总容量上限回收

## 下载稳定性

- `/search` 成功不代表 `/download` 一定立刻成功（搜索与拉流是两条上游链路）
- 下载失败时服务端会自动切换多个 YouTube player client，并带分片/网络重试
- 仍失败时返回 `502 UPSTREAM_ERROR`（message 含「已自动重试」与 yt-dlp 摘要）
- 公网机器若持续失败，优先检查出口网络、可选 `PROXY` / `COOKIES_FILE`，并保持镜像内 yt-dlp 为新版本

## 开发检查

```powershell
$env:GOCACHE = "C:\project\test\youtube-music-api\.gocache"
gofmt -l .          # 必须无输出
go build ./...
go vet ./...
$env:YTM_SKIP_LIVE = "1"; go test ./... -count=1
go test -bench=. ./internal/matching ./internal/session
```

## 合规与使用范围

仅供个人学习或私有 bot 使用。请遵守 YouTube 服务条款与当地版权法。默认只绑定本机 `127.0.0.1`。

任务日志：[`goal-1/tasks.md`](goal-1/tasks.md)。

## 容器部署

仓库根目录提供 `Dockerfile`：

- 编译入口：`./cmd/ytmusic-bridge`（不要在根目录裸 `go build`）
- 镜像内已含 `yt-dlp` 与 `ffmpeg`
- 默认 `HOST=0.0.0.0`，端口读 `PORT`（默认 `8787`）
- 公网请设置 `API_KEY`，bot 用请求头 `X-API-Key` 调用
- 域名在反代/DNS 侧配置；本服务只提供 HTTP API
