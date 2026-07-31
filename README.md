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
>
> `/search` 结果额外返回官方 MV 字段：`official_video_id` / `official_video_url` / `has_official_video`。下音频用歌曲 `video_id` + `mp3/m4a/opus`；下官方 MV 文件用 `official_video_id` + `format=mp4`。

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
| `COOKIES_FILE` | 空 | 可选固定 cookies 文件路径（通常优先用 `COOKIES_DIR`） |
| `COOKIES_DIR` | `cookies` | cookies **文件夹**（云容器挂载这个目录；drop-in 任意 `.txt`） |
| `COOKIE_SOURCE_MODE` | `auto` | Cookie 来源模式：`auto` / `managed` / `external` / `file` |
| `COOKIES_FROM_BROWSER` | 空 | 外部已登录浏览器 profile 规格，作为**单个** argv 传给 yt-dlp |
| `COOKIES_BROWSER_SYNC_ON_START` | `true` | 配置外部 profile 后，启动时是否先同步一次 |
| `COOKIES_BROWSER_SYNC_INTERVAL_SECONDS` | `21600` | 外部 profile 周期同步间隔；`0` 关闭周期同步；正数下限 60 秒 |
| `YOUTUBE_LOGIN_BROWSER_PATH` | 空 | 管理端专用 Chromium 可执行文件；空则自动探测（容器默认 `/usr/bin/chromium`） |
| `YOUTUBE_LOGIN_PROFILE_DIR` | `browser-profile` | 服务端专用持久化登录 profile 目录（需可写，可挂载） |
| `YOUTUBE_LOGIN_HEADLESS` | `true` | 服务端浏览器是否无头；前端通过受保护通道交互 |
| `YOUTUBE_LOGIN_SESSION_TTL_SECONDS` | `900` | 交互登录会话空闲 TTL（秒），夹紧到 60~1800 |
| `YOUTUBE_LOGIN_REFRESH_INTERVAL_SECONDS` | `21600` | managed profile 后台刷新间隔；`0` 关闭；正数下限 60 秒 |
| `COOKIES_KEEPALIVE` | `0` | `1` 开启稳定 jar 自动保活回写 |
| `COOKIES_KEEPALIVE_INTERVAL_SECONDS` | `21600` | 保活间隔（秒），默认 6 小时 |
| `MAX_CONCURRENT_DOWNLOADS` | `2` | yt-dlp 并发上限；超出后**排队** |
| `MAX_FILESIZE_MB` | `500` | 单文件上限；超限返回 `413 FILE_TOO_LARGE`（默认已覆盖常见 1080p MV） |
| `ADMIN_PASSWORD` | 空 | 非空启用 `/admin` 管理端；空则关闭 |
| `ADMIN_SESSION_SECRET` | 空 | 管理会话签名密钥；可省略（启动时派生） |
| `ADMIN_SESSION_TTL_SECONDS` | `43200` | 管理会话有效期（秒） |
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
- 下载策略对齐 spotube 的 yt-dlp 音源思路：先取稳定音频流，再转码
- 默认优先 `android_vr` 客户端（实测比 ios/android+PO Token 稳）
- 一键转码失败时，会回退「先下原始音轨 + 本地 ffmpeg 转码」
- 仍失败时返回 `502 UPSTREAM_ERROR`（message 含「已自动重试」与 yt-dlp 摘要）
- 公网机器若持续失败，优先检查出口网络、可选 `PROXY`、以及 Cookie 来源（管理端登录 / 外部 profile / 文件上传），并保持镜像内 yt-dlp 为新版本

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
- 镜像内已含 `yt-dlp`、`ffmpeg`/`ffprobe` 与 **Chromium**（供管理端 YouTube 登录）
- 默认 `HOST=0.0.0.0`，端口读 `PORT`（默认 `8787`）
- 默认以非 root 用户 `app` 运行；预创建可写 `/app/cookies` 与 `/app/browser-profile`
- 公网请设置 `API_KEY`，bot 用请求头 `X-API-Key` 调用
- 域名在反代/DNS 侧配置；本服务只提供 HTTP API

### 云容器挂载

云平台通常**不能稳定挂单文件**，请挂载文件夹：

| 容器路径 | 说明 |
| --- | --- |
| `/app/cookies` | 稳定 Netscape jar 与 drop-in 文件目录 |
| `/app/browser-profile` | 服务端专用 YouTube 登录 profile（管理端主路线，需持久化） |
| `/app/downloads` | 可选，音频/视频缓存持久化 |

推荐环境变量：

```text
HOST=0.0.0.0
API_KEY=你的密钥
ADMIN_PASSWORD=你的强密码
COOKIE_SOURCE_MODE=auto
COOKIES_DIR=/app/cookies
YOUTUBE_LOGIN_BROWSER_PATH=/usr/bin/chromium
YOUTUBE_LOGIN_PROFILE_DIR=/app/browser-profile
YOUTUBE_LOGIN_HEADLESS=true
COOKIES_KEEPALIVE=1
```

Windows / Linux 本机也可直接跑二进制；本机管理端登录通常无需手填 `YOUTUBE_LOGIN_BROWSER_PATH`（会自动探测 Chromium / Chrome / Edge）。

**不要**把真实 cookie、浏览器 profile 数据库或账号材料提交到 git 或推进公开仓库。

## Cookie 来源与登录策略

搜索与下载**只读取**稳定 Netscape 文件 `cookies/youtube.txt`（或 `COOKIES_FILE`），业务层不会直接并发读取浏览器数据库。

三条来源是**独立可选路线**，不是串行前置条件：

| 路线 | 配置 | 适用场景 |
| --- | --- | --- |
| 管理前端登录（推荐主路线） | `COOKIE_SOURCE_MODE=auto|managed` + 可写 `YOUTUBE_LOGIN_PROFILE_DIR` | 长期在服务端维护登录，不依赖日常浏览器 |
| 外部浏览器 profile | `COOKIES_FROM_BROWSER=...` + `COOKIE_SOURCE_MODE=auto|external` | 服务与已登录日常浏览器处于**同一 OS/用户**，可解密 profile |
| 文件上传 / 目录 drop-in | 管理端上传或往 `COOKIES_DIR` 丢 `.txt` | 浏览器路线不可用时的回退 |

`COOKIE_SOURCE_MODE` 语义：

- `auto`：已验证的 managed 登录优先；否则用外部 profile；再否则用文件
- `managed`：只走服务端浏览器登录
- `external`：只走 `COOKIES_FROM_BROWSER`
- `file`：只走文件

关键边界：

1. 外部 profile 与服务必须处于可解密的同一 OS/用户上下文；把 Windows DPAPI 加密的 profile 原样挂到 Linux 容器通常**无法解密**。
2. `COOKIES_FROM_BROWSER` 必须作为**单个参数**传给 yt-dlp（可含空格路径与 Firefox container），不要拆 shell。
3. 浏览器同步失败、被锁、未登录或质量下降时，**保留已有较强登录 jar**，不会用匿名 visitor cookie 覆盖。
4. managed 已登录时，外部 profile 周期同步会暂停；断开 managed 登录后，可继续使用文件回退或其他来源。
5. 管理状态 API 只返回来源、登录态、质量分、同步结果等元数据，**不返回 Cookie 正文**。

外部 profile 示例：

```text
COOKIES_FROM_BROWSER=chrome
COOKIES_FROM_BROWSER=edge:Default
COOKIES_FROM_BROWSER=chrome:C:\Users\you\AppData\Local\Google\Chrome\User Data\Default
COOKIES_FROM_BROWSER=firefox:Profile With Spaces
```

## 管理前端（登录 + Cookie）

独立管理前端与 bot `API_KEY` 分离：

| 地址 | 说明 |
| --- | --- |
| `/admin/login.html` | 管理员登录页 |
| `/admin/` | 进入管理端（默认打开登录页） |
| `/admin/index.html` | 登录后的认证管理页：YouTube 浏览器登录、Cookie 状态、文件上传 |

环境变量：

```text
ADMIN_PASSWORD=你的强密码
# 可选
# ADMIN_SESSION_SECRET=随机长串
# ADMIN_SESSION_TTL_SECONDS=43200
COOKIE_SOURCE_MODE=auto
COOKIES_DIR=/app/cookies
YOUTUBE_LOGIN_PROFILE_DIR=/app/browser-profile
COOKIES_KEEPALIVE=1
```

操作要点：

1. 设置 `ADMIN_PASSWORD` 后打开 `/admin/login.html` 进入管理端。
2. 在「YouTube 浏览器登录」面板点击「开始登录」，通过远程画面完成 Google 账号 / 密码 / 2FA。
3. 登录完成后点击「校验登录」；通过后写入稳定 jar，搜索与下载可立即共用。
4. 「断开已保存登录」会清理服务端浏览器登录，并切回文件或其他可用来源。
5. 文件上传始终保留：managed 生效时新上传会作为 drop-in 回退保存，不会覆盖当前强登录 jar。

安全约束：

- `ADMIN_PASSWORD` **为空则管理端关闭**（登录/上传 API 返回 503）
- 管理会话使用 HttpOnly Cookie；上传与状态接口只返回元数据
- 远程登录画面与敏感输入仅在受保护的同会话通道中内存转发，服务端不把密码/验证码写入日志
- 公网务必设置强管理员密码，并继续为 bot 接口设置 `API_KEY`
