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

## [x] Task G2：匹配层（internal/matching）display_name + 归一化 + match_score

目标：实现 `Normalize`（NFKC + casefold + 去标点 + 压空白）、`Tokenize`（拉丁按词、CJK 逐字）、`BuildDisplayName(title, artists)` → `"标题 - 歌手1, 歌手2"`、`MatchScore(query, displayName)` → 0~1（整串相似度 / 包含 / 词元覆盖率取最大）。

独立验证：
- `go test ./internal/matching`：完全相同=1.0；`"lemon"` vs `"Lemon - Kenshi Yonezu"` 高分；`"周杰伦 晴天"` vs `"晴天 - 周杰倫"` 高分（含简繁差异记录实际分值）；乱码 query 低分；空串=0；`display_name` 无 artist 时不带 `-`。
- `go test -bench=. ./internal/matching` 给出 ns/op（R11 证据）。

做了什么 / 验证结果 / 剩余风险 / 下一步：

**做了什么**
- 新增 `internal/matching/matching.go`（约 300 行）：
  - `BuildDisplayName(title, artists)` → `"标题 - 歌手1, 歌手2"`；无歌手时不带 `" - "`；过滤空歌手并 trim。此格式是 R8「按全名选歌」的契约，已写入 `docs/BOT-INTEGRATION.md §8` 承诺不变。
  - `Normalize` → NFKC（全角→半角）→ 小写 → 标点/符号/emoji 转空格 → 压缩空白；纯标点返回 `""`。
  - `Tokenize` → 拉丁/数字按空白分词，**CJK/假名/韩文逐字切分**（不逐字切的话「周杰倫晴天」会变成一个巨型词元，部分匹配彻底失效）。
  - `MatchScore(query, target)` → 取三策略最大值：① 整串 Levenshtein 相似度；② 子串包含（`0.75 + 0.25*len(q)/len(t)`）；③ 词元覆盖率 `tokenScore`。
  - `levenshtein` 用双行滚动数组：O(len(a)·len(b)) 时间、O(min(len)) 空间、循环内零分配。
- 新增 `internal/matching/matching_test.go`：**24 个测试函数 / 61 个子测试 + 7 个 benchmark**。
- `go get golang.org/x/text` → `go.mod` 新增 `golang.org/x/text v0.40.0`（唯一第三方依赖，只用其 `unicode/norm` 做 NFKC；`go mod tidy` 后已是直接依赖，非 indirect）。
- **本轮发现并修复 3 个真实缺陷（不是改测试目标，是改实现）**：
  1. **错拼 query 与乱码 query 无法区分（严重，直接违背 R3「近似名字」）**：初版 `tokenScore` 只做精确词元匹配，`"lemmon"` 对 `"Lemon - Kenshi Yonezu"` 只剩整串 Levenshtein 的 **0.2632**，而乱码 query 是 0.2059 —— 两者几乎重合，`min_score` 无论取什么值都无法同时「留下错拼、滤掉乱码」。修复：`tokenScore` 改为**两轮匹配**，第一轮精确、第二轮对长度 ≥4 的未命中词元做相似度 ≥0.7 的错拼匹配，命中权重取**实际相似度**而非 1.0（保证精确命中始终排在错拼命中之前）。实测 `"lemmon"` 0.2632 → **0.7500**，乱码仍 0.2059，区分度打开。
  2. **模糊匹配会抢走精确命中的词元**：若单轮遍历，`"yonezu yonezuu"` 里 `yonezuu` 可能先模糊吃掉唯一的 target 词元 `yonezu`，导致后面本该精确命中的 `yonezu` 落空。修复：拆成两轮（精确优先），并新增 `TestMatchScoreFuzzyDoesNotStealExactToken`。
  3. **超长 query 是 DoS 向量**：Levenshtein 是 O(n·m)，而 `/search` 的 query 完全由 bot 用户输入。修复：新增 `maxScoreRunes = 256`，打分前按字符数截断（`truncateRunes`，不切坏多字节字符）；`nq == nt` 的相等判断放在截断**之前**，保证「完全相同 = 1.0」契约不被截断破坏。实测 20 万字符 query 打分耗时 **1.17ms**（未截断则不可用）。
- **性能优化（数学无损）**：模糊匹配内层加 `similarityUpperBound` 剪枝 —— 长度差是 Levenshtein 距离的下界，故相似度上界 = `min(la,lb)/max(la,lb)`，上界够不到阈值或当前最优时直接跳过矩阵计算。`BenchmarkMatchScoreWorstCase` 15540 ns → **12716 ns（-18%）**、8296 B/117 allocs → **5992 B/73 allocs**，且**分值表逐条比对完全一致**（剪枝未改变任何行为）。另加 `TestSimilarityUpperBoundIsValid` 用 17×17 组合暴力验证上界成立。
- 顺带修 `gofmt`：新写的测试文件有对齐问题，`gofmt -w` 已修正。

**验证结果（实测）**
- `gofmt -l .` → 无输出；`go build ./...` → 无输出；`go vet ./...` → 无输出。
- `go test ./... -count=1` → `ok internal/config` + `ok internal/matching`，全绿。
- `go test ./... -race -count=1` → 全绿（matching 1.105s）。
- **实测分值表（target = `"Lemon - Kenshi Yonezu"`）——`min_score` 阈值取值依据**：

  | query | 分值 | 类别 |
  | --- | --- | --- |
  | `Lemon - Kenshi Yonezu` | **1.0000** | 全名精确 |
  | `lemon kenshi yonezu` | **1.0000** | 归一化后等价 |
  | `kenshi yonezu lemon` | **1.0000** | 词序颠倒 |
  | `kenshi yonezu` | 0.9500 | 只打歌手 |
  | `lemon` / `yonezu` | 0.9000 | 只打歌名 / 只打姓 |
  | `kenshi yonzu` | 0.8708 | 歌手错拼 1 字母 |
  | `lemmon` | 0.7500 | 歌名错拼 1 字母 |
  | `lemon tree` | 0.4500 | 同名不同歌（不同曲） |
  | `zzqqxxwweeyy nonexistent song 9182` | 0.2059 | 乱码 |
  | `bohemian rhapsody` | 0.1579 | 完全无关 |

- **CJK 与简繁实测**：`"晴天"` vs `"晴天 - 周杰倫"` = 0.9100；`"周杰倫 晴天"` = **1.0000**；`"晴天 周杰伦"`（简体）= 0.8333；`"周杰伦 晴天"` = 0.7760；`"邓紫棋 泡沫"` vs `"泡沫 - 鄧紫棋"` = 0.7760；`"レモン"` vs `"レモン - 米津玄師"` = 0.9143。**简繁差异最低 0.7760**，仍远高于乱码。
- **结论：`min_score` 推荐默认 `0.35`**（与 `docs/BOT-INTEGRATION.md` 第 254 行给 bot 的建议值一致，本轮实测确认合理）。理由：真实命中（含错拼、简繁、词序颠倒）最低 0.6067（`"周杰伦"` 只打简体歌手名），乱码最高 0.2059 —— **0.35 落在两者之间且两侧各有 ~0.15 余量**。服务端默认仍为 `0.0`（不过滤），决策权归 bot（R5）。
- **基准数据（R11 证据，i7-14650HX / 24 线程 / windows-amd64）**：

  | Benchmark | ns/op | B/op | allocs/op |
  | --- | --- | --- | --- |
  | `MatchScoreLatin` | 314.0 | 48 | 2 |
  | `MatchScoreCJK` | 1374 | 592 | 18 |
  | `MatchScoreWorstCase` | 13187 | 5992 | 73 |
  | **`ScoreFullPage`（一页 20 条，= 一次 `/search` 的打分总开销）** | **65244（≈65µs）** | 40640 | 480 |
  | `Normalize` | 363.7 | 48 | 1 |
  | `TokenizeCJK` | 200.4 | 320 | 10 |
  | `BuildDisplayName` | 62.54 | 80 | 2 |

  **关键结论**：一次 `/search` 的全部打分开销约 **65µs**，而实测上游 InnerTube 网络往返是 **1.18s** —— 打分占单次请求耗时的 **0.0055%**，匹配层不是性能瓶颈，R11 在本层已达标。
- 测试覆盖清单：`BuildDisplayName`(9) / `Normalize`(16) / `Tokenize`(11) / 精确=1.0(5) / 空=0(6) / 值域 [0,1](20 组合) / 部分 query 高分(8) / 无关低分(4) / 排序(1) / 错字容忍(3) / **错拼落在完整 display_name(4)** / **模糊不过宽(3)** / **精确优于错拼(1)** / **模糊不抢精确词元(1)** / **单汉字不模糊(1)** / 重复词元(1) / 简繁(5) / **上界有效性(289 组合)** / **超长输入有界(3)** / **truncateRunes(7)** / 阈值分值表(1)。

**剩余风险**
- **简繁转换未做**：`"周杰伦"`（简）对 `"周杰倫"`（繁）靠逐字 token 部分命中拿到 0.6067，能用但不理想。若后续发现中文用户体验不好，可引入简繁映射表（`golang.org/x/text` 不含此功能，需额外依赖或自带表）。当前不做，避免为假设需求增加依赖。
- **罗马字/拼音查询未支持**：`"kenshi yonezu"` 查日文原名 `"米津玄師"` 会低分。实际影响小 —— YouTube Music 上游搜索本身接受罗马字，返回的 `display_name` 通常已含罗马字，打分对象是 `display_name` 而非原始日文。G3 拿到真实响应后需复查此假设。
- `fuzzyTokenMinLen = 4` 与 `fuzzyTokenThreshold = 0.7` 是基于上述实测表调出的经验值，非配置项。若 G9 端到端联调发现误命中，再考虑提升为配置。
- 打分对象最终是 `display_name`，其质量取决于 G3 的解析正确性（歌手字段是否完整）；G3 完成后应回归本层的真实数据表现。

**下一步**：Task G2.5 大型全面检查-debug 循环 #1（覆盖 G0–G2）。

---

## [x] Task G2.5???????/debug ?? #1??? G0?G2?
?????????bug?`go vet`????????????????????????????????
???

**????**?G0???????+ G1???/??/???+ G2??????

### 1. ????
| ?? | ?? | ?? |
| --- | --- | --- |
| R1 ?? | ?? | `plan.md` ?3 + `README.md` ?????????? InnerTube??? yt-dlp ???? |
| R4/R5 limit ?? | ?? | `internal/config`?`MinimumMaxLimit=20`?`ResolveLimit`??????? 10 / >20 ?? / MAX_LIMIT ???? |
| R6 ?????????? | ???????? G4 | ????? `docs/BOT-INTEGRATION.md`?`total`/`truncated`????????? |
| R7/R8 ??/???? | ???? | R8 ? `display_name` ???? `matching.BuildDisplayName` ???????? G5 |
| R11 ??? Go | ????? | ? CGO???? HTTP???? 20 ?????? 63?s?????? G9 |
| R12 bot ???? | ???????? | `docs/BOT-INTEGRATION.md` ????`/search` `/download` `/file` ??????????? |

**???????**?R1?R12 ??????Python ??????

### 2. ?? / ???? / ????????
| ?? | ?? |
| --- | --- |
| `gofmt -l .` | ??? |
| `go build ./...` | ?? |
| `go vet ./...` | ?? |
| `go test ./... -count=1` | ???`internal/config` + `internal/matching` |
| `go test ./... -race -count=1` | ???config 1.033s / matching 1.123s? |
| `go test -bench=Benchmark -benchmem`?`internal/matching`? | ?????? |

**??????i7-14650HX / windows-amd64?**
| Benchmark | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `MatchScoreLatin` | 320.5 | 48 | 2 |
| `MatchScoreCJK` | 1346 | 592 | 18 |
| `MatchScoreWorstCase` | 12426 | 5992 | 73 |
| `ScoreFullPage`?20 ?? | 63522??63.5?s? | 40640 | 480 |
| `Normalize` | 353.9 | 48 | 1 |
| `TokenizeCJK` | 197.4 | 320 | 10 |
| `BuildDisplayName` | 60.09 | 80 | 2 |

? G2 ?????????????????????? 1.18s ????

### 3. Bug / ???
- ??? fail-fast?MAX_LIMIT ???DEFAULT_LIMIT ???MIN_SCORE ???`.env` ???????????????
- ??? 21 ???????????????? token?CJK ??????????????????????
- `TestScoreTableForThreshold` ????? 1.0 / ?? `lemmon` 0.75 / ?? 0.2059?? G2 ?????
- **????????? bug**?

### 4. ??
- ???? `127.0.0.1`??? plan ?????
- ?? `API_KEY` ????????????? G7??????????
- ??? `maxScoreRunes=256` ?? O(n?m) DoS ??
- ?????? / token ???????G6/G7 ????????????
- ?????/`.env` ???`.gitignore` ? `.env` / `downloads/` / `bin/`?

### 5. ????? / ????
| ? | ?? |
| --- | --- |
| `display_name` ?? | `matching.BuildDisplayName` ? `docs/BOT-INTEGRATION.md` ?2/?8 ???`"?? - ??1, ??2"` |
| `min_score` ???? 0.35 | ??? G2 ???????????? 0.0????? bot? |
| `healthz` ?? | ???? `status/version/default_limit/max_limit`???? ?5 ?????yt-dlp ????? plan ? G8? |
| README ???? | **????**??????? `[ ]` ?? `[x]`???? G2 ?????? |
| ????? | `/search` `/download` `/file` ????????????????? |
| ?? | ??? `C:\project\test\youtube-music-api`??? Desktop ?????? |

### 6. ??
- Git ?????`62cfae0`?`3c75626`??????????
- ????????? PATH ???? CGO?

### 7. ??????
- Go `1.26.4 windows/amd64`
- `ffmpeg` / `ffprobe` ???`C:\Program Files\ffmpeg-8.1.1-essentials_build\bin\`
- `yt-dlp` ?? PATH??????G6/G8 ?????

### 8. ????????
1. **????????**?README??????????????? ? ??? `[x]`?
2. **?????**?`go test -bench=. ./internal/matching` ?????????no Go files in .????????????? `-run=^$ -bench=Benchmark` ????????????????????
3. **?????????????**?`internal/ytmusic` / `search` / `session` / `download` / `httpapi` ?????G3 ????

### 9. ??
G0?G2 **??**???????????????? Task G3?InnerTube ???????

**????????? task???????**
- ????????????????????
- ?? InnerTube ?????????????? G3 ?????
- HTTP ??? `httptest`?G7??
- ?? yt-dlp ?????????? healthz??????? G6/G8?

**???**?Task G3 InnerTube ??????`internal/ytmusic`??

---
## [x] Task G3：InnerTube 搜索客户端（internal/ytmusic）
目标：实现 `WEB_REMIX` 客户端 POST `/youtubei/v1/search`（`filter=songs`），复用 `http.Client`（keep-alive、超时、可选 proxy）；防御式解析 `musicResponsiveListItemRenderer` → `Track{VideoID,Title,Artists,Album,Duration,DurationSeconds,Thumbnail}`；上游一页 20 条，返回原始列表。

独立验证：
- live 测试：真实搜索 `"lemon kenshi yonezu"` 返回 ≥10 条，首条含 `videoId`/`title`/`artists`/`duration`。
- 固化一份真实响应 JSON 到 `testdata/`，离线单测解析正确、字段缺失不 panic。
- 中文/日文 query live 验证。

做了什么 / 验证结果 / 剩余风险 / 下一步：

**做了什么**
- 新增 `internal/ytmusic` 包：
  - `types.go`：`Track{VideoID,Title,Artists,Album,Duration,DurationSeconds,Thumbnail}`
  - `client.go`：`WEB_REMIX` 搜索客户端；`http.Transport` keep-alive + HTTP/2；可选 `Proxy`；默认超时 15s；`clientVersion = 1.YYYYMMDD.01.00`；songs filter `EgWKAQIIAWoMEA4QChADEAQQCRAF`
  - `parse.go`：防御式遍历 `musicResponsiveListItemRenderer`；videoId 多路径回退；title/artists/album/duration/thumbnail 缺失不 panic
  - `client_test.go`：离线 fixture 解析、残缺 JSON、httptest 请求体/头校验、live 英/中/日搜索
  - `testdata/search_songs_lemon.json`：真实上游响应（约 680KB / 20 条）
- 仅返回原始 songs 列表；limit / match_score / session 留给 G4/G5。

**验证结果**
- `gofmt -l .` 无输出；`go build ./...` / `go vet ./...` 通过。
- `go test ./... -count=1` 通过：`config` / `matching` / `ytmusic`。
- 离线：fixture 解析 ≥10 条；首条 `Lemon` / `Kenshi Yonezu` / `4:17` / `257s` / 有 thumbnail。
- live：
  - `lemon kenshi yonezu` → 首条 `3NNhrqHZqlI | Lemon | [Kenshi Yonezu] | 4:17`（≥10 条）
  - `晴天 周杰伦` → 首条 `SJKoWAd5ySo | 晴天 | [周杰倫]`
  - `レモン 米津玄師` → 首条 `3NNhrqHZqlI | Lemon | [Kenshi Yonezu]`
- 残缺字段 / 无 videoId 条目：跳过且不 panic。

**剩余风险**
- InnerTube 响应结构可能变更；已用防御式路径 + fixture 单测兜底，但 live 仍可能偶发 429/地区差异。
- `hl/gl` 当前默认 `en/US`；中文歌手名在 en 响应里常显示繁体（如「周杰倫」），后续若 bot 需要可配置语言。
- 未做 limit/min_score（G4）与 session（G5）。
- live 测试默认联网执行；可用 `YTM_SKIP_LIVE=1` 跳过。

**下一步**：Task G4 搜索服务层（limit 夹紧 / 截断 / 打分 / min_score 过滤 / R6）。

---

## [x] Task G4：搜索服务层（limit 夹紧 / 截断 / 打分 / min_score 过滤 / R6 不足即返回）
目标：`internal/search`：调 ytmusic → 生成 `display_name` 与 `match_score` → 按 `min_score` 过滤 → 截断到 `limit_used` → 填 `index`（1-based）、`total`、`truncated`。

独立验证：
- 单测（stub 上游）：limit 缺省=10；limit=25→20；limit=0→错误；上游 20 条 + limit=10 → total=10 且 truncated=true；上游 3 条 + limit=10 → total=3 且 truncated=false（R6）；`min_score=0.9` 过滤后条数减少且 index 重新连续。

做了什么 / 验证结果 / 剩余风险 / 下一步：

**做了什么**
- 新增 `internal/search`：
  - `Upstream` 接口（`*ytmusic.Client` 可直接注入；测试用 stub）
  - `Service.Search` 管线：trim query → resolve limit/min_score → 上游搜索 → `BuildDisplayName` + `MatchScore` → min_score 过滤 → 按分数稳定降序 → 截断 → 1-based index
  - `Request.Limit` / `MinScore` 用指针区分「未传」与「显式 0」：未传 limit→DefaultLimit；显式 limit<=0→`ErrInvalidLimit`；limit>MaxLimit→夹到 MaxLimit；min_score 走 `config.ResolveMinScore`
  - `Response` 含 `Query/LimitRequested/LimitUsed/MinScoreUsed/Total/Truncated/Results`（不含 session_id，留给 G5）
- `service_test.go`：12 个用例覆盖 G4 验收点与排序/空 query/上游错误/nil artists

**验证结果**
- `gofmt -l .` 无输出；`go build ./...` / `go vet ./...` 通过
- `go test ./internal/search -count=1 -v` 12/12 PASS：
  - 缺省 limit=10，20→10 且 `truncated=true`
  - limit=25→`limit_requested=25`/`limit_used=20`
  - limit=0/-3 → `ErrInvalidLimit`
  - 上游 3 条 + limit=10 → total=3 / `truncated=false`（R6）
  - `min_score=0.9` 过滤后条数减少、index 从 1 连续、首条含 Lemon
  - 分数降序稳定排序
- `go test ./... -count=1` 全绿（含 ytmusic live）

**剩余风险**
- 本层尚未挂 session（G5）与 HTTP（G7）；`session_id`/`expires_in` 不在本包
- 打分对象是 `display_name`，依赖 G3 解析质量；简繁/罗马音边界仍由 matching 层承担
- 上游永远返回约 20 条时，`min_score=0` 的 truncated 语义正确；若未来上游分页，截断判断仍基于「过滤后条数 vs limit_used」

**下一步**：Task G5 会话快照层 + 选择层（`internal/session`）

---

## [x] Task G5：会话快照层 + 选择层（internal/session）

????? map + TTL ????????`Select(sessionID, index, name, videoID)` ????? `video_id` > `index` > `name`???????????????????????????index ?? 404?session ?? 410?

?????
- ???1-based ???????/???????????????????????????????????????????????
- `go test -race` ????????`go test -bench` ?? session ?? ns/op?

???? / ???? / ???? / ????

**????**
- ?? `internal/session`?
  - `types.go`?`Snapshot` / `Selection`?????? `ErrBadRequest` / `ErrNotFound` / `ErrGone` / `ErrAmbiguous`??????? `BadRequestError` / `NotFoundError` / `GoneError` / `AmbiguousError`??? `errors.Is` / `errors.As`?? HTTP ??? 400/404/410/409??
  - `store.go`??? map??? 32?????? 2 ???+ ??? `RWMutex`?`Put` ???????? `session_id`?`s_` + 12 ???? hex?? `expires_in`?`Get` ???????`Cleanup` ????????????????
  - `select.go`?`Select(sessionID, index, name, videoID)`???? **`video_id` > `index` > `name`**?
    - `video_id` ????? session?
    - `index` 1-based??? ? `ErrNotFound`?
    - `name`??? ? `Normalize` ?? ? ?????`MatchScore` ? 0.75 ??????????? ? `AmbiguousError` ??????
    - session ?? ? `ErrGone`?
- ???`store_test.go` + `select_test.go`??????TTL????1-based?????????/???/??/???video_id ??????????
- bench?`BenchmarkStorePut` / `Get` / `PutGetParallel`?

**????????**
- `gofmt -l ./internal/session` ? ????
- `go test ./internal/session -count=1` ? PASS?
- `go test -race ./internal/session -count=1` ? PASS????????
- `go test -bench=. -benchmem ./internal/session`?
  - `BenchmarkStorePut` ? **1363 ns/op**?1906 B/op?13 allocs/op
  - `BenchmarkStoreGet` ? **558 ns/op**?1696 B/op?11 allocs/op
  - `BenchmarkStorePutGetParallel` ? **399 ns/op**?1801 B/op?12 allocs/op
- `go vet ./internal/session` ? ????`go build ./...` ? ???
- `go test ./... -count=1` ? ???config / matching / search / session / ytmusic??
- ?????
  - index=1/2 ???? `video_id`?1-based??
  - index ?? / ?? session ? `ErrNotFound`?
  - TTL ?? ? `ErrGone` + ?????
  - ????????/????????
  - ???? ? `ErrAmbiguous` ? `Candidates` ????
  - ??? ? `ErrNotFound`?
  - ? `video_id` ?? session????? index/name?

**????**
- `video_id` ??? `Selection.Item` ?? `VideoID`?? title/artists??? ?????? session ?????????????????G6?? HTTP ?? session ???
- ???????? 0.75???????? bot ???????? G7/G10 ????????
- ?? session ??????? TTL??? ???????????G7 ?? CleanupInterval ??????????????????
- ???? G5.5 ?????? goal ??? 3 ? task ???G3/G4/G5 ?????? G5.5??

**???**?Task G5.5?G3?G5 ?????? / debug ????

---

## [x] Task G5.5???????/debug ?? #2??? G3?G5?

???? #1????`-race` ??????????? JSON??TTL ???????????

???

**????**?G3 `internal/ytmusic` + G4 `internal/search` + G5 `internal/session`??? G0?G2 ???????

### 1. ????
| ?? | ?? | ?? |
| --- | --- | --- |
| R1 ?? | ?? | ?? InnerTube ???????? G6 ? yt-dlp |
| R3 ???? | ?? | G3 live ?/?/? + G4 `match_score` ??/?? |
| R4/R5 limit | ?? | G4??? 10?25?20??? 0 ???`MaxLimit` ???? config ?? ?20 |
| R6 ????? | ?? | G4??? 3 ? + limit=10 ? total=3 / truncated=false |
| R7 index ?? | ?? | G5?1-based??? `ErrNotFound`?index ??? name |
| R8 ???? | ?? | G5??? ? Normalize ? ???????? `ErrAmbiguous` |
| session TTL | ?? | G5??? `ErrGone`?`expires_in` ?????? + Cleanup |
| R11 ?? | ????? | matching ?? ~142?s?session Put ~1.6?s / Get ~0.6?s??????? G9 |
| R12 bot ?? | ????? | ????? `docs/BOT-INTEGRATION.md` ???HTTP ????G7? |

### 2. ?? / ?? / race / bench
| ?? | ?? |
| --- | --- |
| `gofmt -l .` | ????????? |
| `go build ./...` | ?? |
| `go vet ./...` | ?? |
| `go test ./... -count=1` | ???config / matching / ytmusic / search / session |
| `go test -race`????? | ???????? |
| live ?? | `lemon` / `?? ???` / `??? ????` ? PASS |

**bench?i7-14650HX / windows-amd64?**
| Benchmark | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `matching.MatchScoreLatin` | 350.4 | 48 | 2 |
| `matching.MatchScoreCJK` | 1411 | 592 | 18 |
| `matching.ScoreFullPage`(20 ?) | 141891?~142?s? | 40640 | 480 |
| `session.StorePut` | 1575 | 1913 | 13 |
| `session.StoreGet` | 600.4 | 1696 | 11 |
| `session.StorePutGetParallel` | 521.6 | 1809 | 12 |

### 3. ?????
1. **`ytmusic.bytesTrimSpace` ?????? `string()` ???? `[]byte`**????????? KB???????????? `bytes.TrimSpace`?
2. **?????? `TestSearchContextCancel` ?????**?server ???? + ????????????????????????? context ????????
3. **search ?? stub ?????**???????? Artists ????????????????stub ????? Artists??? `TestSearchResultsDefensiveCopy` / `TestSearchStableOrderOnEqualScores` ?????????????
4. **????????????**?
   - ytmusic?? object ? JSON?????? renderer?context ???
   - session?`index=0` ?? name?????????Select ? Cleanup ???`expires_in` ?????`Put(nil)`?

**????????????**?G3?G5 ????? tasks ??????

### 4. ??
- ?????? `127.0.0.1`?main/config??
- session ?????? `session_id`???????????
- ?????? 8MiB ????????????
- `API_KEY` ??? HTTP ????G7??
- ????????? `/file`??

### 5. ?? / ?????
| ? | ?? |
| --- | --- |
| `display_name` ?? | search ?? `matching.BuildDisplayName`?? bot ?? `"?? - ??"` ?? |
| `index` 1-based | search ???session ? `Item.Index` ?? |
| ????? | `video_id` > `index` > `name`?? bot ???? |
| ???? | NotFound/Ambiguous/Gone/BadRequest ??? 404/409/410/400 |
| README ?? | G3?G5 ??????/HTTP/e2e ?? |
| `session_id`/`expires_in` | ? session.Put ???HTTP ???? G7 |

### 6. ??
- ???????? `c0b45e6`?G5 ?????
- ??? ytmusic ?????? + ?????? API ???

### 7. ??
- Go `1.26.4 windows/amd64`
- live YouTube Music ??
- yt-dlp ????? PATH?G6/G8?

### 8. ?????????? G6?
- InnerTube ????????????fixture + ???????? live ?? 429/???????
- `video_id` ???????????/HTTP ?????????
- session ?? TTL????????G7/G8 ? CleanupInterval ????
- ?? name ?? 0.75 ????????????????
- HTTP ???????? JSON ???????G7??

### 9. ??
G3?G5 **??**?????????? 1 ?????/???????????????? Task G6 ????

**???**?Task G6 `internal/download`?yt-dlp + ffmpeg + ?? + singleflight + ????

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
