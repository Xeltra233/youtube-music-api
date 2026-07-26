# Tasks：ytmusic-bridge（Go 版）

规则：每轮会话只执行一个未完成 task；完成后补写「做了什么 / 验证结果 / 剩余风险 / 下一步」；每 3 个 task 一次大型全面检查-debug 循环。

状态：`[ ]` 未开始 / `[~]` 进行中 / `[x]` 已完成

> 技术栈变更（2026-07-26）：用户追加要求「改成 go / 我要高性能」。原 Python 版 Task 1–2 的成果中，**环境与网络实测结论保留**（写入 plan.md §2），代码产物已删除。下列 Task 编号为 Go 版重新编号。

> 路径变更（2026-07-26，G1 期间）：用户要求「所有文件都放 `C:\project\test\youtube-music-api`」，项目根已从 `C:\Users\Xeltra\Desktop\ytmusic-bridge` 整体迁移（含 `.git`），旧目录已删除。

> 追加需求 R12（2026-07-26，G1 期间）：用户要求先出 bot 接入文档以便并行开发 bot 搜索功能，并追问了并发支持。已交付 `docs/BOT-INTEGRATION.md`（含 §9 多人并发行为）。该文档在 G3–G7 实现完成后需由 Task G10 校订「实现状态」表。

---

## [x] Task G0：技术栈切换（清理 Python 产物 + Go 选型 + 文档重写）

目标：删除 Python 产物，核验 Go 环境，完成 Go 生态 GitHub 选型，重写 plan.md / tasks.md，input.md 记录追加需求。

独立验证：
- 项目根不再有 `pyproject.toml` / `app/` / `scripts/` / `.venv`。
- `go version` 输出可用版本。
- plan.md §3 含 Go 候选仓库的真实 star/活跃度数据与决策理由。

做了什么 / 验证结果 / 剩余风险 / 下一步：

**做了什么**
- 删除 `.venv` / `app/` / `scripts/` / `pyproject.toml` / `.ruff_cache`（Python 版产物全部作废）。
- `input.md` 追加「追加 1」逐字记录用户新要求。
- 重写 `plan.md`：新增 R11（Go + 高性能）、Go 目录规划、性能设计要点、Go 选型表。
- 重写本 `tasks.md` 为 Go 版任务分解。

**验证结果（实测）**
- `go version go1.26.4 windows/amd64`；`GOPATH=C:\Users\Xeltra\go`；`GOPROXY=https://proxy.golang.org,direct`。
- 清理后项目根仅剩：`.git` / `goal-1` / `.gitignore` / `README.md`。
- Go 生态选型数据（api.github.com 实测）：`raitonoberu/ytmusic` 24★ 停更于 2024-03；`kkdai/youtube` 3915★ 2026-06；`lrstanley/go-ytdlp` 314★ 2026-07-15；`wader/goutubedl` 172★ 2026-07-09；`yt-dlp` 最新 tag `2026.07.04`。
- 决策：搜索层自研 InnerTube 客户端（零第三方依赖、性能可控）；下载层用 yt-dlp 外部二进制。

**剩余风险**
- 自研 InnerTube 客户端需保证请求体与解析路径正确 → Task G3 用真实响应验证并固化样本。

**下一步**：Task G1 Go 模块骨架 + 配置模块。

---

## [x] Task G1：Go 模块骨架 + 配置模块（internal/config）

目标：`go mod init github.com/xeltra/ytmusic-bridge`；建立 `cmd/ytmusic-bridge/main.go` 最小可运行入口（仅 `/healthz`）；`internal/config` 从环境变量/`.env` 加载全部配置，含 `MAX_LIMIT` 下限保护（不得低于 20）、`DEFAULT_LIMIT` 夹紧、ffmpeg 探测。

独立验证：
- `go build ./...` 成功；`go vet ./...` 无告警；`gofmt -l .` 无输出。
- `go test ./internal/config` 覆盖：默认值、环境变量覆盖、`MAX_LIMIT=5` 被抬到 20、`DEFAULT_LIMIT=99` 被夹到 `MAX_LIMIT`、非法 bitrate 报错。
- 启动后 `GET /healthz` 返回 200 且含版本。

做了什么 / 验证结果 / 剩余风险 / 下一步：

**做了什么**
- `go.mod`：`module github.com/xeltra/ytmusic-bridge`，`go 1.26.4`，零第三方依赖。
- `internal/version/version.go`：`Version` 变量，支持 `-ldflags -X` 注入。
- `internal/config/config.go`：21 项配置，来源优先级「环境变量 > `.env` > 默认值」；`MinimumMaxLimit = 20` 保证 R5；`ResolveLimit` / `ResolveMinScore` 把 bot 请求参数收敛到合法区间；`Addr()` / `MaxFilesizeBytes()` / `CacheMaxTotalBytes()` / `EnsureDownloadDir()` 辅助方法。
- `cmd/ytmusic-bridge/main.go`：最小入口，`GET /healthz` + `ReadHeaderTimeout` + 信号优雅关闭骨架。
- `internal/config/config_test.go`：17 个用例 / 6 个子测试。
- **本轮加固（两处真实缺陷）**：
  1. 原 `getInt`/`getFloat`/`getDuration` 对非法值**静默回落默认值** → 用户把 `MAX_LIMIT` 写成 `abc` 却毫无感知。改为 `loader` 结构累积错误后 fail fast，并新增 `TestNonNumericValuesFailFast`（6 个 key）与负数秒数校验。
  2. `.gitignore` 里的 `ytmusic-bridge` 规则把 **`cmd/ytmusic-bridge/` 源码目录整个忽略**了（`git status` 看不到 `cmd/`）→ 改为 `/ytmusic-bridge` 只忽略根目录构建产物，`git check-ignore cmd/ytmusic-bridge/main.go` 返回 rc=1 确认已修复。
  3. `HOST` 为空白字符串时回落 `127.0.0.1`（新增 `TestBlankHostFallsBack`）。
- **附带完成（R12，用户追加需求）**：`docs/BOT-INTEGRATION.md` —— 面向 bot 开发者的接口契约文档，含流程图、`/search` 与 `/download` 全字段表、9 类错误码处置建议、Python/Go/PowerShell 三份示例、bot 侧实现清单、§9「多人同时使用」并发说明与契约变更承诺。
- **附带完成（路径迁移）**：项目根迁至 `C:\project\test\youtube-music-api`，`.git` 一并迁移（历史保留），旧目录删除。

**验证结果（新路径下实测）**
- `gofmt -l .` → 无输出；`go build ./...` → 无输出；`go vet ./...` → 无输出。
- `go test ./...` → `ok internal/config`，全部通过（`TestLoadDefaults` / `TestMaxLimitFloorIs20`(MAX_LIMIT=5→20) / `TestMaxLimitCanExceed20`(50→50) / `TestDefaultLimitClampedToMaxLimit`(99→20) / `TestResolveLimit`(7 组：0→10、-3→10、20→20、25→20、1000→20) / `TestResolveMinScore` / `TestInvalidValues`×4 / `TestNonNumericValuesFailFast`×6 / `TestBitrateWithKSuffix` / `TestEnvFileAndPrecedence` / `TestEnvFileMissingIsOK` / `TestDurationAndFloorValues` / `TestByteHelpers` / `TestBlankHostFallsBack`）。
- 真实启动（隐藏窗口，未抢焦点）：`GET /healthz` → `200 {"default_limit":10,"max_limit":20,"status":"ok","version":"0.1.0"}`；`DOWNLOAD_DIR` 自动创建成功。
- 路由与方法约束：未注册路径 → `404`；`POST /healthz` → `405`（`net/http` 方法路由生效）。
- fail fast 实测：`PORT=70000` → 退出码 1 + `启动失败: PORT 必须在 1-65535 之间，当前 70000`；`MAX_LIMIT=abc` → 退出码 1 + `配置校验失败: MAX_LIMIT 必须是整数，当前 "abc"`。
- 日志中文用 UTF-8 读取正常（此前乱码仅是 PowerShell 控制台解码问题，非程序缺陷）。

**剩余风险**
- `FFMPEG_LOCATION` / `YTDLP_PATH` 目前只读取不校验，外部依赖探测留给 G6/G8（`/healthz` 届时补报 yt-dlp 版本）。
- `docs/BOT-INTEGRATION.md` 中 `/search`、`/download`、`/file` 三个接口仍是**契约先行**，文档已显式标注「服务端开发中」；G3–G7 实现后必须由 G10 回头校订状态表，防止文档与实现漂移。
- 目前尚无 HTTP 层测试（`httptest`），G7 补齐。

**下一步**：Task G2 匹配层（`Normalize` / `Tokenize` / `BuildDisplayName` / `MatchScore` + bench）。

---

## [ ] Task G2：匹配层（internal/matching）display_name + 归一化 + match_score

目标：实现 `Normalize`（NFKC + casefold + 去标点 + 压空白）、`Tokenize`（拉丁按词、CJK 逐字）、`BuildDisplayName(title, artists)` → `"标题 - 歌手1, 歌手2"`、`MatchScore(query, displayName)` → 0~1（整串相似度 / 包含 / 词元覆盖率取最大）。

独立验证：
- `go test ./internal/matching`：完全相同=1.0；`"lemon"` vs `"Lemon - Kenshi Yonezu"` 高分；`"周杰伦 晴天"` vs `"晴天 - 周杰倫"` 高分（含简繁差异记录实际分值）；乱码 query 低分；空串=0；`display_name` 无 artist 时不带 `-`。
- `go test -bench=. ./internal/matching` 给出 ns/op（R11 证据）。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G2.5：大型全面检查-debug 循环 #1（覆盖 G0–G2）

检查项：需求偏离、bug、`go vet`、构建、测试、安全、数据一致性、回滚、文档同步。结果写入本文件。

结果：

---

## [ ] Task G3：InnerTube 搜索客户端（internal/ytmusic）

目标：实现 `WEB_REMIX` 客户端 POST `/youtubei/v1/search`（`filter=songs`），复用 `http.Client`（keep-alive、超时、可选 proxy）；防御式解析 `musicResponsiveListItemRenderer` → `Track{VideoID,Title,Artists,Album,Duration,DurationSeconds,Thumbnail}`；上游一页 20 条，返回原始列表。

独立验证：
- live 测试：真实搜索 `"lemon kenshi yonezu"` 返回 ≥10 条，首条含 `videoId`/`title`/`artists`/`duration`。
- 固化一份真实响应 JSON 到 `testdata/`，离线单测解析正确、字段缺失不 panic。
- 中文/日文 query live 验证。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G4：搜索服务层（limit 夹紧 / 截断 / 打分 / min_score 过滤 / R6 不足即返回）

目标：`internal/search`：调 ytmusic → 生成 `display_name` 与 `match_score` → 按 `min_score` 过滤 → 截断到 `limit_used` → 填 `index`（1-based）、`total`、`truncated`。

独立验证：
- 单测（stub 上游）：limit 缺省=10；limit=25→20；limit=0→错误；上游 20 条 + limit=10 → total=10 且 truncated=true；上游 3 条 + limit=10 → total=3 且 truncated=false（R6）；`min_score=0.9` 过滤后条数减少且 index 重新连续。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G5：会话快照层 + 选择层（internal/session）

目标：分片 map + TTL 的候选快照存储；`Select(sessionID, index, name, videoID)` 实现优先级 `video_id` > `index` > `name`（精确→归一化→唯一模糊），歧义返回带候选清单的错误；index 越界 404、session 过期 410。

独立验证：
- 单测：1-based 映射正确；越界/过期错误类型正确；全名精确命中；大小写空白差异命中；两条同名→歧义错误含候选；无命中→未找到。
- `go test -race` 并发读写无竞态；`go test -bench` 给出 session 存取 ns/op。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G5.5：大型全面检查-debug 循环 #2（覆盖 G3–G5）

检查项同 #1，另加：`-race` 并发、解析健壮性（畸形 JSON）、TTL 边界。结果写入本文件。

结果：

---

## [ ] Task G6：下载层（internal/download）yt-dlp + ffmpeg + 缓存 + singleflight + 限流

目标：按 `videoID+format` 下载；yt-dlp 外部进程（显式 `--ffmpeg-location`、超时 context、可选 proxy/cookies）；临时文件 + `os.Rename` 原子落盘；`singleflight` 去重；`semaphore` 限流；超 `MAX_FILESIZE_MB` 报 413；缓存索引落盘；文件名清洗防路径穿越；yt-dlp 缺失时给明确错误。

独立验证：
- 单测（伪造 yt-dlp 脚本）：缓存命中不重复执行；10 并发同曲只执行 1 次（singleflight 证据）；体积超限报错；路径穿越被拒；进程失败错误可读。
- live 测试：真实下载一首，ffprobe 校验时长/码率。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G7：HTTP API 层（internal/httpapi）

目标：路由 `/healthz`、`POST /search`、`POST /download`（二进制 + `?mode=json`）、`GET /file/{token}`；中间件：panic 恢复、请求日志、可选 `X-API-Key`、请求体大小限制、超时；统一错误 JSON；二进制响应带 `X-Track-*` 头与 `Content-Disposition`；`/file` 支持 Range。

独立验证：
- `httptest` 全路由测试：正常流、鉴权开关、各错误码（400/401/404/409/410/413）、`mode=json` 与二进制两种响应、Range 请求。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G8：优雅关闭 + 缓存清理 goroutine + 启动脚本 + yt-dlp 引导

目标：`main.go` 装配全部依赖、信号优雅关闭；后台清理 goroutine（TTL + 目录总量上限）；`scripts/get-ytdlp.ps1` 下载 yt-dlp 到 `bin/`；`run.ps1` 一键构建启动；`.env.example`。

独立验证：
- TTL=1s 的单测证明过期文件被删、目录超限时最旧文件被删。
- 实测 `run.ps1` 启动（隐藏窗口不抢焦点）→ `GET /healthz` 200 且报告 yt-dlp 版本 → 优雅关闭无 goroutine 泄漏。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G8.5：大型全面检查-debug 循环 #3（覆盖 G6–G8）

检查项同 #1，另加：真实网络异常（无效 videoId / 超时）返回是否可读、进程泄漏、临时文件清理。结果写入本文件。

结果：

---

## [ ] Task G9：端到端真实联调 + 性能压测（R11 证据）

目标：`scripts/e2e.ps1`（或 Go 程序）模拟 bot：search → index 选择 → 拿音频；search → 全名选择 → 拿音频；第二次同曲命中缓存。压测：`/search` 并发 QPS/P99、同曲 20 并发下载只触发 1 次 yt-dlp、内存占用。

独立验证：脚本退出码 0；两条链路音频均可 ffprobe 校验；压测数据写入本文件与 README。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G10：文档交付（README + bot 接入指南 + 选型说明）

目标：README 覆盖：安装（Go 构建 + yt-dlp 引导）、配置表、启动、全部接口示例（PowerShell + Go/Python bot 示例）、limit 规则、错误码表、性能数据、缓存与合规说明、上游依赖选型结论。

另需（R12 收尾）：校订 `docs/BOT-INTEGRATION.md` —— 把 §0「实现状态」表全部改为「可用」，用实测响应替换示例 JSON，用 G9 压测数据填充 §9.5 的空缺，并逐字段核对实现与文档一致（字段名、`index` 1-based、`display_name` 格式、状态码与 `code` 取值均不得漂移）。

独立验证：按 README 从零复现构建 + 启动 + 搜索 + 下载；文档中每条命令实际执行通过。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task G11：最终 review（R1–R11 逐条取证 + 修复 + 标记 goal 完成）

目标：从 C 端、代码、安全、数据一致性、权限、错误处理、测试、构建、文档、回滚全面 review；修复所有已知高风险；逐条对照 plan.md §1（R1–R12）给出证据。

结果：
