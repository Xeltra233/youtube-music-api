# Plan：ytmusic-bridge（YouTube Music 搜索 + 下载 HTTP API，供 bot 调用）

## 1. 需求解析（从 input.md 提炼）

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 先在 GitHub 上找到可用的 YouTube Music 相关仓库并选型 | plan.md 中列出候选仓库（含 star / 语言 / 活跃度）与最终选择理由 |
| R2 | 自己写一个项目，对外「导出 API」（HTTP API） | 项目可启动，`/docs` 或明确路由清单可访问，bot 可通过 HTTP 调用 |
| R3 | bot 转发「近似歌曲名」给本项目，本项目负责搜索 | `POST /search` 接受模糊关键词，返回排序后的候选列表 |
| R4 | 返回「歌单」默认约 10 首，最大 20 首 | 默认 limit=10；`limit` 可传，超过 20 被夹到 20（或明确报错，见 §6 假设） |
| R5 | 数量可配置，且配置权由 bot 侧决定；服务端必须支持到最大值 | 请求参数 `limit` 优先；服务端配置只提供默认值与硬上限 20 |
| R6 | 搜不到那么多就返回能搜到的数量 | 结果条数 = min(可得数量, limit)，不足不报错，返回 `total` 字段 |
| R7 | bot 可用「序号」选择 | `POST /download`（或 `/select`）接受 `index`（1-based，对应列表显示序号） |
| R8 | bot 也可用「名字（歌单上显示的全名）」选择 | 接受 `title` / `name` 字段，与列表中 `display_name` 精确匹配，其次归一化匹配 |
| R9 | 选中后项目去下载该音乐 | 下载得到音频文件（m4a/opus→mp3 可选），带基础元数据 |
| R10 | 下载完成后把音乐「转发给 bot」 | API 直接返回音频二进制流（`GET /file/{token}` 或 `/download` 直接返回文件），bot 拿到后转发给用户 |

## 2. 上下文与环境事实（已核验）

- 平台：Windows 10/11，PowerShell，cwd `C:\Users\Xeltra`。
- 已有工具（`Get-Command` 核验）：
  - `git` = `C:\Program Files\Git\cmd\git.exe`
  - `python` = `C:\Users\Xeltra\AppData\Local\miniconda3\python.exe`（Python 3.13.13，pip 26.0.1）
  - `node` v?（`C:\Program Files\nodejs\node.exe`）、`npm`
  - `uv` = `C:\Users\Xeltra\.local\bin\uv.exe`
  - `ffmpeg` = `C:\Program Files\ffmpeg-8.1.1-essentials_build\bin\ffmpeg.exe`（音频转码/合流可用）
  - `docker` 存在（本项目不强依赖）
  - `yt-dlp` **未安装**（需要作为 Python 依赖安装到项目虚拟环境，而不是全局改环境）
- 项目根目录：`C:\Users\Xeltra\Desktop\ytmusic-bridge`（本 goal 新建）。
- 网络：GitHub API 可直连（已成功调用 `api.github.com`）。YouTube 直连能力在 Task 2 中实测；若不可直连，走 §6 代理假设。

## 3. GitHub 选型调研（R1，数据来自 api.github.com，2026-07-26）

搜索/元数据来源仓库候选：

| 仓库 | Stars | 语言 | 最近 push | 用途评估 |
| --- | --- | --- | --- | --- |
| `sigma67/ytmusicapi` | 2883 | Python | 2026-07-25 | **首选**。YouTube Music 非官方 API，直接提供 `search(query, filter="songs", limit=N)`，返回 `videoId/title/artists/album/duration`，无需登录即可搜索；维护非常活跃 |
| `LuanRT/YouTube.js` | 5071 | TypeScript | 2026-07-03 | 备选（Node 生态 InnerTube 客户端），功能强但需要引入 Node 服务，与 Python 下载栈混用成本高 |
| `nick42d/youtui` | 194 | Rust | 近期 | Rust TUI+API，语言栈不匹配 |
| `z-huang/InnerTune` | 5995 | Kotlin | 2025-11-13 | Android 客户端，不适合做服务端 |
| `deepjyoti30/ytmdl` | 3523 | Python | 2024-08-15 | CLI 工具（YouTube→mp3+元数据），可作为「元数据补全」思路参考，但已近两年未更新，不作为依赖 |
| `zerodytrash/YouTube-Internal-Clients` | 593 | Python | 2022 | 仅作 InnerTube 客户端参数参考资料 |

下载来源仓库候选：

| 仓库 | Stars | 语言 | 最近 push | 用途评估 |
| --- | --- | --- | --- | --- |
| `yt-dlp/yt-dlp` | 180114 | Python | 2026-07-23 | **首选**。作为库（`yt_dlp.YoutubeDL`）内嵌调用，负责按 `videoId` 下载 bestaudio 并用 ffmpeg 转码；社区最活跃，抗 YouTube 变更能力最强 |
| `pear-devs/pear-desktop`（原 th-ch/youtube-music） | 32798 | TypeScript | 2026-07-24 | 桌面播放器，非服务端方案，不采用 |

**最终选型**：Python 3.13 + FastAPI（HTTP API 层） + `ytmusicapi`（搜索/歌单元数据） + `yt-dlp`（下载） + `ffmpeg`（已装，转码）。依赖装在项目本地 `.venv`（用 `uv`），不污染全局 miniconda 环境。

## 4. 架构与接口设计（R2–R10）

```text
bot ──HTTP──> ytmusic-bridge (FastAPI)
                 ├─ search  : ytmusicapi.YTMusic().search(q, filter="songs", limit)
                 │            → 归一化候选列表 + 生成 session_id（含候选快照，带 TTL）
                 ├─ select  : 按 index 或 display_name 命中候选
                 ├─ fetch   : yt_dlp 下载 bestaudio → ffmpeg 转 mp3/m4a → 落盘缓存（按 videoId 去重）
                 └─ deliver : 返回音频二进制（StreamingResponse/FileResponse）+ 元数据响应头
```

接口清单（细节以 Task 中实现为准，保持向后一致）：

1. `GET /healthz` → `{"status":"ok","version":...}`，用于 bot 探活。
2. `POST /search`
   - 入参：`{"query": str, "limit": int|null}`
   - 行为：`limit` 缺省用配置 `DEFAULT_LIMIT=10`；`limit` 上限 `MAX_LIMIT=20`（>20 夹到 20）；`limit<1` 报 422。
   - 出参：`{"session_id": str, "query": str, "limit_used": int, "total": int, "results":[{"index":1,"display_name":"Title - Artist","title":...,"artists":[...],"album":...,"duration":"3:52","duration_seconds":232,"video_id":"...","thumbnail":"..."}]}`
   - `display_name` 即「歌单上面显示的全名」，是 R8 匹配的唯一权威字符串。
3. `POST /download`
   - 入参：`{"session_id": str|null, "index": int|null, "name": str|null, "video_id": str|null, "format":"mp3"|"m4a"|null}`
   - 选择优先级：`video_id` > `session_id+index` > `session_id+name`（精确 → 归一化 → 唯一模糊）；歧义（多条同名）返回 409 + 候选清单。
   - 出参（默认）：音频二进制 + `Content-Disposition: attachment; filename="..."` + 自定义头 `X-Track-Title / X-Track-Artists / X-Track-Video-Id / X-Track-Duration`。
   - 出参（`?mode=json` 或 `Accept: application/json`）：`{"video_id":...,"file_url":"/file/{token}","filesize":...,"metadata":{...}}`，便于 bot 大文件分步取。
4. `GET /file/{token}` → 取回上一步生成的文件（token 与 videoId+format 绑定，TTL 可配）。
5. 认证：可选 `API_KEY`（`X-API-Key` 头）。配置为空则不校验（本机内网默认）。
6. 配置（`.env` / 环境变量）：`HOST/PORT/API_KEY/DEFAULT_LIMIT/MAX_LIMIT/DOWNLOAD_DIR/AUDIO_FORMAT/AUDIO_BITRATE/CACHE_TTL_SECONDS/SESSION_TTL_SECONDS/PROXY/COOKIES_FILE/MAX_CONCURRENT_DOWNLOADS/MAX_FILESIZE_MB`。

关键设计决策：

- **搜索与选择解耦**：`session_id` 保存候选快照，保证 bot 用「序号」选择时不会因为二次搜索排序漂移而选错歌（这是纯 index 方案最大的正确性风险）。
- **限流上限硬编码 20**：R4/R5 明确最大 20，服务端夹紧，bot 侧可自由传 1..20。
- **不足即返回**：ytmusicapi 结果不足时不补齐、不报错，`total` 反映真实条数（R6）。
- **缓存去重**：同一 `video_id+format` 已下载则直接复用文件，避免 bot 重复请求打爆 YouTube。
- **并发保护**：下载用信号量限流 + 同 key 单飞（single-flight），避免同一首歌并行下载写坏文件。
- **文件安全**：文件名经白名单清洗，禁止路径穿越；对外只暴露 token→内部路径映射，不接受任意路径参数。

## 5. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| YouTube 反爬 / 需要 PO Token / 403 | 下载失败 | yt-dlp 保持可升级；支持 `COOKIES_FILE`、`PROXY`、多 client 回退（`android_music`/`web`/`ios`）；错误信息透传给 bot |
| 本机无法直连 YouTube | 搜索/下载全挂 | Task 2 实测；失败则用 `PROXY` 环境变量（假设见 §6），并在文档写明 |
| ytmusicapi `limit` 语义是「至少 N」并含续页 | 返回条数可能 >limit | 服务端统一截断到 `limit_used` |
| Python 3.13 与依赖兼容 | 装不上 | 用 `uv` 建独立 venv，锁定版本；失败则降级 3.12（uv 可自动装解释器） |
| 大文件把 bot 上传限制打爆 | bot 转发失败 | `MAX_FILESIZE_MB` 上限 + JSON 模式返回体积让 bot 预判 |
| 磁盘无限增长 | 占满磁盘 | `CACHE_TTL_SECONDS` 后台清理 + 缓存目录大小上限 |
| 版权/滥用 | 合规风险 | README 写明仅供个人学习/私有使用；默认不公开监听 0.0.0.0 之外无鉴权 |

## 6. 默认假设（无人值守，不向用户提问）

1. 语言/框架：Python + FastAPI + uvicorn（因为 ytmusicapi 与 yt-dlp 都是 Python，最短链路）。
2. 传输方式：HTTP REST + JSON；音频通过二进制响应返回（「转发给 bot」= bot 从本 API 取回文件后自行转发）。
3. 默认音频格式：mp3 192k（bot 平台兼容性最好）；可配 `m4a` 直出免转码。
4. `limit > 20` 采取「夹到 20」而非报错，更符合「起码支持返回那么多」。
5. 默认监听 `127.0.0.1:8787`，`API_KEY` 默认空（本机使用）；文档说明公网部署必须设 key。
6. 不做数据库；session/缓存索引用进程内字典 + 磁盘 JSON 索引，重启后缓存文件仍可复用。
7. 「歌单」理解为「搜索结果候选列表」（用户上下文指的是给 bot 展示的列表），不是 YouTube Music 的 playlist 实体。
8. 若本机无法直连 YouTube，允许通过 `PROXY` 环境变量走用户已有代理；不修改系统网络设置。
9. 不做 bot 本体（Telegram/QQ 等）；只交付 API + 一份 bot 侧调用示例文档/脚本。

## 7. 验证方式

- 单元测试（pytest）：limit 夹紧逻辑、不足即返回、index/name 选择与歧义处理、display_name 归一化、文件名清洗、鉴权。
- 集成测试（真实网络，标记 `-m live`）：`/search` 真实关键词返回 ≥1 条且 ≤limit；`/download` 真实下载一首短音频，校验文件存在、体积 >0、`ffprobe` 可读、时长与元数据一致。
- 端到端脚本：模拟 bot 流程（search → 选 index → 拿文件；search → 选 display_name → 拿文件）。
- 类型检查：`mypy`（或 `pyright`）+ `ruff` 静态检查。
- 手动核验：`curl`/PowerShell `Invoke-RestMethod` 调用，输出粘贴到 tasks.md 作为证据。

## 8. 回滚方案

- 全部工作限定在 `C:\Users\Xeltra\Desktop\ytmusic-bridge`，git 初始化后每个 task 一次提交；回滚 = `git revert` / `git reset --hard <sha>` 或直接删除该目录。
- 依赖只装在项目 `.venv`，不改全局 Python/PATH/系统配置；回滚 = 删除 `.venv`。
- 不触碰生产配置、密钥、系统网络设置。
