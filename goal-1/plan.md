# Plan：ytmusic-bridge（Go 版｜YouTube Music 搜索 + 下载 HTTP API，供 bot 调用）

> 技术栈变更记录：初版设计为 Python + FastAPI（提交 `62cfae0`…`609a5b1`），用户在 Task 3 期间追加要求「改成 Go / 我要高性能」（见 input.md 追加 1），本 plan 已整体重写为 Go 方案，Python 产物已删除。R1–R10 需求不变。

## 1. 需求解析（从 input.md 提炼，未变）

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 先在 GitHub 上找到可用的 YouTube Music 相关仓库并选型 | plan.md §3 列出候选仓库（star / 语言 / 活跃度）与最终选择理由 |
| R2 | 自己写一个项目，对外「导出 API」（HTTP API） | 服务可启动，路由清单明确，bot 可通过 HTTP 调用 |
| R3 | bot 转发「近似歌曲名」给本项目，本项目负责搜索 | `POST /search` 接受模糊关键词，返回排序后的候选列表 |
| R4 | 返回「歌单」默认约 10 首，最大 20 首 | 默认 limit=10；`limit` 可传，超过 20 夹到 20 |
| R5 | 数量可配置，配置权由 bot 侧决定；服务端必须支持到最大值 | 请求参数 `limit` 优先；服务端配置只给默认值与硬上限（上限不得低于 20） |
| R6 | 搜不到那么多就返回能搜到的数量 | 返回条数 = min(可得数量, limit)，不足不报错，`total` 反映真实条数 |
| R7 | bot 可用「序号」选择 | `POST /download` 接受 `index`（1-based，对应列表显示序号） |
| R8 | bot 也可用「名字（歌单上显示的全名）」选择 | 接受 `name`，与 `display_name` 精确匹配，其次归一化/唯一模糊匹配 |
| R9 | 选中后项目去下载该音乐 | 下载得到音频文件，带基础元数据 |
| R10 | 下载完成后把音乐「转发给 bot」 | API 返回音频二进制流（或 JSON + `file_url` 供 bot 取回） |
| R11（追加） | **用 Go 实现，高性能** | Go 原生 HTTP 服务；无 Python 运行期依赖；并发安全；提供压测/基准证据 |
| R12（追加） | **先交付 bot 接入文档**，用户并行开发 bot 搜索功能 | `docs/BOT-INTEGRATION.md` 给出稳定接口契约、字段语义、错误码、示例代码，并标注实现状态 |

## 2. 环境事实（已核验）

- 平台：Windows，PowerShell，项目根 **`C:\project\test\youtube-music-api`**（2026-07-26 按用户要求从 `C:\Users\Xeltra\Desktop\ytmusic-bridge` 迁移，旧目录已删除；`.git` 一并迁移，历史保留）。所有新文件只允许放在此根目录下。
- `go version go1.26.4 windows/amd64`；`GOPATH=C:\Users\Xeltra\go`；`GOPROXY=https://proxy.golang.org,direct`。
- `ffmpeg` = `C:\Program Files\ffmpeg-8.1.1-essentials_build\bin\ffmpeg.exe`（转码可用，`ffprobe` 同目录）。
- `git` 可用；仓库已初始化，历史提交 `62cfae0` / `8d88720` / `609a5b1`。
- **网络实测结论（Python 探针阶段取得，对 Go 同样成立，因为走同一套 HTTP 端点）**：
  - YouTube Music 搜索接口可直连，1.18s 返回 20 条，无需代理/cookies。
  - YouTube 音频下载可直连，单曲 4~6s 完成下载+mp3 转码；`ffprobe` 校验 `duration=256.1s, bit_rate≈192k`。
  - 中文/日文查询正常。
  - 上游 `limit` 是「至少 N」语义，一页固定 20 条 → 服务端必须硬截断。
  - 搜索**永不返回空**：乱码 query 也返回 20 条无关结果 → 需 `match_score` + 可选 `min_score` 才能让 R6 真正可用。

## 3. GitHub 选型（R1｜Go 生态，数据来自 api.github.com 2026-07-26）

### 3.1 搜索/元数据

| 仓库 | Stars | 语言 | 最近 push | 评估 |
| --- | --- | --- | --- | --- |
| `raitonoberu/ytmusic` | 24 | Go | 2024-03-24 | Go 版 YouTube Music 搜索库。**作为 InnerTube 协议参考实现**，但 star 少、近两年未更新，直接依赖风险高 |
| `sigma67/ytmusicapi` | 2883 | Python | 2026-07-25 | Python 库，**作为协议权威参考**（请求体/解析路径），不作为运行期依赖 |
| `LuanRT/YouTube.js` | 5071 | TypeScript | 2026-07-03 | InnerTube 参考实现（TS），同上作参考 |

**决策**：搜索层**自行实现 InnerTube `WEB_REMIX` 客户端**（Go 标准库 `net/http` + `encoding/json`）。理由：
1. 只需要 `/youtubei/v1/search` 一个端点，请求体固定，自研代码量小（约 300 行）且零第三方依赖 → 最高性能、最少供应链风险。
2. 不必受 `raitonoberu/ytmusic`（久未维护）拖累；协议细节对照 `ytmusicapi` 与 `YouTube.js` 实现，保证正确性。
3. 可完全控制连接复用（`http.Transport` keep-alive）、超时、并发，符合 R11 高性能要求。

### 3.2 下载

| 仓库 | Stars | 语言 | 最近 push | 评估 |
| --- | --- | --- | --- | --- |
| `yt-dlp/yt-dlp` | 180114 | Python | 2026-07-23 | 事实标准，抗 YouTube 变更能力最强。**以外部可执行文件方式调用**（`os/exec`），Go 服务本体不含 Python 依赖 |
| `lrstanley/go-ytdlp` | 314 | Go | 2026-07-15 | yt-dlp 的 Go CLI 绑定，**采用**：类型安全、可自动下载 yt-dlp 二进制、活跃维护 |
| `wader/goutubedl` | 172 | Go | 2026-07-09 | 同类方案，备选 |
| `kkdai/youtube` | 3915 | Go | 2026-06-02 | 纯 Go 下载器，无外部进程，但对 YouTube 签名/PO Token 变更的跟进速度远不及 yt-dlp，**作为快速路径（fast path）备选** |

**决策**：下载层采用 **yt-dlp 外部进程**（通过 `lrstanley/go-ytdlp` 或直接 `os/exec` 调用，Task 6 定型），ffmpeg 负责转码。理由：可靠性 >> 少一个进程；下载本身是 IO 密集，进程开销占比极低，不影响「高性能」目标（性能瓶颈在网络与转码，不在 Go/进程边界）。

**最终技术栈**：Go 1.26 + 标准库 `net/http`（服务端与 InnerTube 客户端）+ 自研搜索解析 + yt-dlp（外部二进制）+ ffmpeg。零 CGO，单文件可执行产物。

## 4. 架构设计

```text
bot ──HTTP──> ytmusic-bridge (Go, net/http)
   │
   ├─ POST /search    → internal/ytmusic  : InnerTube WEB_REMIX /search（连接复用、超时可控）
   │                    internal/matching : display_name 归一化 + match_score
   │                    internal/session  : 候选快照（sharded map + TTL），返回 session_id
   ├─ POST /download  → internal/session  : 按 index / name / video_id 定位候选
   │                    internal/download : yt-dlp + ffmpeg，singleflight 去重 + 信号量限流 + 磁盘缓存
   │                    → 直接回二进制流（http.ServeContent）或 JSON + file_url
   ├─ GET  /file/{token} → 取回缓存音频（token→路径映射，不接受任意路径）
   └─ GET  /healthz   → 探活
```

目录规划：

```text
cmd/ytmusic-bridge/main.go      # 入口：配置加载、路由装配、优雅关闭
internal/config/config.go       # 环境变量配置（含 MAX_LIMIT 下限保护）
internal/ytmusic/client.go      # InnerTube 客户端（search）
internal/ytmusic/parse.go       # 响应解析 → []Track
internal/matching/matching.go   # 归一化 / tokenize / display_name / match_score
internal/session/store.go       # 候选快照 + TTL
internal/download/downloader.go # yt-dlp 调用 + 缓存 + singleflight + 限流
internal/httpapi/*.go           # handler / 中间件（API Key、日志、恢复）/ 错误模型
internal/apitypes/types.go      # 请求响应结构体（bot 消费的 JSON 契约）
```

### 4.1 接口契约

1. `GET /healthz` → `{"status":"ok","version":"0.1.0","ytdlp":"2026.07.04"}`
2. `POST /search`
   - 入参：`{"query":string, "limit":int?, "min_score":float?}`
   - `limit` 缺省 → `DEFAULT_LIMIT`(10)；`limit > MAX_LIMIT`(20) → 夹到 20；`limit < 1` → 400。
   - 出参：`{"session_id","query","limit_requested","limit_used","min_score_used","total","truncated","expires_in","results":[{"index","display_name","title","artists":[],"album","duration","duration_seconds","video_id","thumbnail","match_score"}]}`
3. `POST /download`
   - 入参：`{"session_id"?, "index"?, "name"?, "video_id"?, "format"?}`；`?mode=json` 切 JSON 模式。
   - 选择优先级：`video_id` > `session_id+index` > `session_id+name`（精确→归一化→唯一模糊；多条同名 → 409 带候选清单）。
   - 默认返回音频二进制 + `Content-Disposition` + `X-Track-*` 头。
4. `GET /file/{token}` → 音频二进制（支持 Range，便于 bot 断点/分片）。
5. 鉴权：可选 `X-API-Key`（`API_KEY` 为空则不校验）。
6. 错误模型：`{"code","message","detail"}`，HTTP 状态码语义化（400/401/404/409/410/413/429/502/504）。

### 4.2 高性能要点（R11）

- 单一 `http.Client` + 调优 `Transport`（`MaxIdleConnsPerHost`、HTTP/2、keep-alive）复用到 InnerTube。
- session 存储用**分片 map**（按 session_id 哈希分片，降低锁竞争），惰性 + 定时双重 TTL 清理。
- 下载去重用 `golang.org/x/sync/singleflight`：同一 `videoId+format` 并发请求只跑一次 yt-dlp。
- 并发上限用带权信号量（`golang.org/x/sync/semaphore`），避免 yt-dlp 进程风暴。
- 命中缓存时用 `http.ServeContent` 零拷贝式回文件（支持 Range / If-Modified-Since）。
- 搜索结果打分为纯 CPU 计算，避免正则回溯；`match_score` 对 20 条数据是微秒级。
- 提供 `go test -bench` 基准 + 并发压测脚本作为 R11 证据。

## 5. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| InnerTube 响应结构变更 | 搜索解析失败 | 解析走「防御式路径遍历」，字段缺失降级不 panic；单测固化真实响应样本；解析失败返回 502 + 可读原因 |
| YouTube 反爬 / PO Token / 429 | 下载失败 | yt-dlp 可独立升级；支持 `PROXY` / `COOKIES_FILE`；多 client 回退；错误透传给 bot |
| 依赖外部 yt-dlp 二进制 | 部署多一步 | `/healthz` 报告 yt-dlp 版本；缺失时启动即给出明确错误与安装指引；文档写清 |
| 搜索永不为空（已实证） | bot 拿到垃圾列表 | `match_score` + 可选 `min_score`（默认 0 不过滤，决策权归 bot） |
| 大文件超出 bot 上传限制 | 转发失败 | `MAX_FILESIZE_MB` + JSON 模式先返回体积 |
| 磁盘无限增长 | 占满磁盘 | TTL + 目录总量上限，后台清理 goroutine |
| 并发写同一缓存文件 | 文件损坏 | singleflight + 临时文件 + `os.Rename` 原子落盘 |
| 版权/滥用 | 合规风险 | README 声明仅供个人使用；默认只听 127.0.0.1 |

## 6. 默认假设（无人值守，不提问）

1. Go 1.26 + 标准库 `net/http`（不引 gin/echo，减少依赖并保证性能）；仅引入 `golang.org/x/sync`。
2. 搜索层自研 InnerTube 客户端；不依赖 star 数极低且停更的 Go 第三方库。
3. 下载层用 yt-dlp 外部二进制（可靠性优先）；若本机无 yt-dlp，由项目脚本下载到 `bin/` 目录（不改系统 PATH）。
4. 默认音频 mp3 192k；可配 `m4a`/`opus`。
5. 默认监听 `127.0.0.1:8787`，`API_KEY` 默认空。
6. 不用数据库；session 内存、缓存索引落盘 JSON。
7. 「歌单」= 搜索结果候选列表。
8. 直连可用，`PROXY` 仅作可选逃生口。
9. 不做 bot 本体，只交付 API + bot 接入示例。
10. `min_score` 默认 0.0（不过滤），阈值决策权归 bot。

## 7. 验证方式

- `go build ./...`、`go vet ./...`、`gofmt -l`（必须无输出）。
- `go test ./...`：matching/config/session/select/limit 逻辑单测；InnerTube 解析用固化样本；handler 用 `httptest`。
- `go test -bench`：matching 与 session 的基准数据（R11 证据）。
- live 测试（`-tags live` 或 `-run TestLive`）：真实搜索 + 真实下载 + ffprobe 校验。
- 并发压测：同一首歌 N 并发只触发一次下载；`/search` 并发 QPS 与 P99 记录。
- 端到端脚本：模拟 bot 两条链路（index 选择、全名选择）。

## 8. 回滚方案

- 全部改动在 `C:\project\test\youtube-music-api`，每个 task 一次 git 提交；回滚 = `git reset --hard <sha>` 或删目录。
- Go 依赖记录在 `go.mod`/`go.sum`，不改全局环境（无 CGO、不装系统包）。
- yt-dlp 若由脚本下载，落在项目 `bin/`，删除即回滚。
- 不触碰生产配置、密钥、系统网络设置。
