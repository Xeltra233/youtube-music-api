# bot 接入文档：搜索歌曲 + 下载音频

本文档是 **bot 侧开发的接口契约**。服务端已实现完整链路；下方请求/响应结构稳定，可直接按它写 bot 代码。后续只可能新增可选字段，不会破坏已列字段。

| 项目 | 值 |
| --- | --- |
| 服务名 | ytmusic-bridge |
| 项目根 | `C:\project\test\youtube-music-api` |
| 默认地址 | `http://127.0.0.1:8787` |
| 协议 | HTTP/1.1，JSON（`Content-Type: application/json; charset=utf-8`） |
| 鉴权 | 可选请求头 `X-API-Key`；服务端 `API_KEY` 为空时不校验 |

## 0. 实现状态（写 bot 时先看这里）

| 接口 | 状态 | 说明 |
| --- | --- | --- |
| `GET /healthz` | **可用** | 已实现；实测返回 200 + yt-dlp 版本 |
| `POST /search` | **可用** | 已实现；InnerTube 搜索 + `match_score` + session |
| `POST /download` | **可用** | 已实现；二进制 / `?mode=json` 两种模式 |
| `GET /file/{token}` | **可用** | 已实现；支持 Range |

bot 侧只要接 `/search` → `/download` 就是完整链路。

---

## 1. 整体流程

```text
用户: /play 晴天 周杰伦
   |
   +--1--> POST /search  {"query":"晴天 周杰伦","limit":10}
   |       <-- session_id + results[N]（每条带 index、display_name、match_score）
   |
   +--2--  bot 渲染成编号列表发给用户，用户回「3」或回歌曲全名
   |
   +--3--> POST /download {"session_id":"...","index":3}
           <-- 音频二进制（mp3），直接转发给用户
```

要点：

- **一次搜索 = 一个 `session_id`**，它记住这次的候选列表。后续选歌用 `session_id` + `index`/`name`。
- `session_id` 默认 **30 分钟**过期（响应里的 `expires_in` 是剩余秒数）。过期后再选歌返回 `410`，bot 应提示用户重新搜索。
- 用户也可以不走 session，直接用 `video_id` 下载（例如 bot 自己做了收藏夹、重播功能）。

补充（官方音乐视频）：

- `/search` 的每条 `results[]` 会尽量附带官方 MV 字段：`official_video_id` / `official_video_url` / `has_official_video`。
- **听歌/下音频** 仍用原来的 `video_id` + `/download`。
- **发官方音乐视频** 用 `official_video_id` 或 `official_video_url`（本服务当前不提供整段 MV 文件下载）。
- 找不到官方视频时这三个字段仍返回，值为 `""` / `""` / `false`。

---

## 2. `POST /search` 搜索歌曲

### 请求

```json
{
  "query": "lemon kenshi yonezu",
  "limit": 3,
  "min_score": 0.0
}
```

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `query` | string | 是 | — | 模糊歌名，可含歌手。支持中/日/英。首尾空白会去掉，空串返回 `400` |
| `limit` | int | 否 | `10` | 要几条。**范围 1–20**；传 >20 会被截到 20（不报错，看 `limit_used`）；传 <1 返回 `400` |
| `min_score` | float | 否 | `0.0` | 相似度下限，低于它的结果被丢弃。`0.0` = 不过滤 |

**关于 `limit`**：条数由 bot 决定，服务端只保证「最多支持到 20」。bot 想给用户 5 条就传 5，想给 20 条就传 20。

**关于 `min_score`（重要）**：YouTube Music 上游 **永远不会返回空列表** —— 就算 query 是乱码，
它也会塞 20 条毫不相关的歌回来（这是实测结论）。所以「搜不到」这个状态需要判断相似度，两种做法：

1. 传 `min_score`（例如 `0.35`），让服务端帮你过滤；`total == 0` 就是「没搜到」；
2. 不传，自己看每条的 `match_score`，低于阈值就当没搜到。

推荐做法 1，阈值 `0.3 ~ 0.4` 起步，再按实际效果调。

### 响应 `200`（实测样例）

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
      "thumbnail": "https://yt3.googleusercontent.com/Yiqnzq5SfMrNjf9XTAMCPadMclhC8ltAVaePndf64gdwvjaN6eEDFBw2aKRukpqxlb7rdkb27BdUFLIDfA=w120-h120-l90-rj",
      "match_score": 1,
      "official_video_id": "SX_r8WxC3jY",
      "official_video_url": "https://www.youtube.com/watch?v=SX_r8WxC3jY",
      "has_official_video": true
    },
    {
      "index": 2,
      "display_name": "Lemon (Kenshi Yonezu 2018) (feat. Fumito Iwai (Galileo Galilei)) - Flower.far",
      "title": "Lemon (Kenshi Yonezu 2018) (feat. Fumito Iwai (Galileo Galilei))",
      "artists": ["Flower.far"],
      "album": "What if…",
      "duration": "4:47",
      "duration_seconds": 287,
      "video_id": "27qbfxakISw",
      "thumbnail": "https://yt3.googleusercontent.com/rQ53238Rx_FiFyhXOqMyu-gpVKm4MWazwLFdqCd-baYJqrnV-emoePxFPdmxGspLeeNXehuYbZgTQL9B=w120-h120-l90-rj",
      "match_score": 0.8909090909090909,
      "official_video_id": "",
      "official_video_url": "",
      "has_official_video": false
    },
    {
      "index": 3,
      "display_name": "LADY - Kenshi Yonezu",
      "title": "LADY",
      "artists": ["Kenshi Yonezu"],
      "album": "LADY",
      "duration": "3:28",
      "duration_seconds": 208,
      "video_id": "zOkIe3RcTCs",
      "thumbnail": "https://yt3.googleusercontent.com/7e0qJAww69B2DFDDUgFqp59lWMXzuHGS-GG_BFR1sD8rcO80G71aP86hV9NGCqsjx4dMEzO1yxZojAA=w120-h120-l90-rj",
      "match_score": 0.7894736842105263,
      "official_video_id": "",
      "official_video_url": "",
      "has_official_video": false
    }
  ]
}
```

| 字段 | 说明 |
| --- | --- |
| `session_id` | 本次候选列表的句柄，下一步下载要用 |
| `limit_requested` / `limit_used` | 你要的条数 / 实际生效的条数（被夹紧时两者不同） |
| `min_score_used` | 实际生效的相似度下限 |
| `total` | `results` 的真实长度。**搜不到那么多就是更小的数字，不算错误** |
| `truncated` | 上游结果比 `limit_used` 多、被截断了则为 `true` |
| `expires_in` | `session_id` 剩余有效秒数 |

### `results[]` 每条字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `index` | int | **从 1 开始**连续编号。就是发给用户看的序号，用户回「3」就传 `index: 3` 回来 |
| `display_name` | string | `"标题 - 歌手1, 歌手2"`（无歌手时只有标题）。**用户回歌名时按这个全名匹配** |
| `title` | string | 纯标题 |
| `artists` | string[] | 歌手数组，可能是空数组 |
| `album` | string | 专辑名，可能是 `""` |
| `duration` | string | `"4:17"` 这种给人看的格式，可能是 `""` |
| `duration_seconds` | int | 秒数，`0` 表示未知。bot 可用它挡掉过长的曲子 |
| `video_id` | string | YouTube 视频 ID，全局唯一。**建议 bot 存这个做收藏/去重** |
| `thumbnail` | string | 封面图 URL，可直接给聊天平台做缩略图 |
| `match_score` | float | `0.0 ~ 1.0` 相似度，结果已按它降序排列。`1.0` = 与 query 完全一致 |

### 官方音乐视频字段（新增，可选增强）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `official_video_id` | string | 匹配到的**官方音乐视频** YouTube ID；没有则为 `""` |
| `official_video_url` | string | 便于 bot 直接发送的链接：`https://www.youtube.com/watch?v=...`；没有则为 `""` |
| `has_official_video` | bool | `official_video_id != ""` 的便捷布尔 |

**与 `video_id` 的区别（非常重要）**：

| 字段 | 是什么 | bot 怎么用 |
| --- | --- | --- |
| `video_id` | 歌曲音轨 ID（YouTube Music songs 结果） | 传给 `POST /download` 下**音频** |
| `official_video_id` / `official_video_url` | 官方 MV（优先 OMV） | **不要**默认拿去 `/download`；用来发视频消息 / 发链接 / 打开官方 MV |

推荐 bot 行为：

1. 用户要听歌 / 下音频：继续 `session_id + index/name` 或 `video_id` → `/download`
2. 用户要官方 MV（例如命令尾参 `mv` / `video` / `官方`，由 bot 自己解析）：
   - 若 `has_official_video == true`：直接发 `official_video_url`，或按平台能力用 `official_video_id` 发视频
   - 若为 `false`：提示「没有找到官方视频」，可回退到音频或普通结果
3. 旧 bot 不认识新字段时，可忽略；JSON 解析请兼容未知字段

说明与边界：

- 服务端会并行请求 songs + videos，再按标题/艺人相似度匹配；匹配不到就留空，**不报错**。
- videos 上游失败时：主搜索仍 200，官方视频三字段全部为空。
- 当前 **没有**「下载官方 MV 成 mp4 文件」接口；发官方视频靠 ID/URL。

---

## 3. `POST /download` 下载并取回音频

### 请求

四种选歌方式，**优先级 `video_id` > `index` > `name`**：

```jsonc
// 方式 A：用户回了序号（最常用）
{"session_id": "s_93595c3f7bdbe33731b8c26b", "index": 1}

// 方式 B：用户回了歌单上显示的全名
{"session_id": "s_93595c3f7bdbe33731b8c26b", "name": "Lemon - Kenshi Yonezu"}

// 方式 C：跳过 session，直接指定（适合收藏夹/重播）
{"video_id": "3NNhrqHZqlI"}

// 方式 D：指定音频格式
{"video_id": "3NNhrqHZqlI", "format": "m4a"}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `session_id` | string | 用 `index` / `name` 选歌时必填 |
| `index` | int | 1-based，就是 `/search` 返回的 `index` |
| `name` | string | 与 `display_name` 比对：先精确，再忽略大小写/空白，再唯一模糊。命中多条返回 `409` 并附候选清单 |
| `video_id` | string | 直接指定，忽略 session |
| `format` | string | `mp3`（默认）/ `m4a` / `opus` |

### 响应（默认：音频二进制）

`200`，body 就是音频文件本身，可直接转发给聊天平台。

| 响应头 | 示例 | 用途 |
| --- | --- | --- |
| `Content-Type` | `audio/mpeg` | — |
| `Content-Length` | `6148269` | 上传前校验大小 |
| `Content-Disposition` | `attachment; filename*=UTF-8''Lemon.mp3` | 文件名（UTF-8 编码，中文安全） |
| `X-Track-Title` | `Lemon`（URL 编码） | 发消息时带标题 |
| `X-Track-Artists` | `Kenshi%20Yonezu`（URL 编码） | 发消息时带歌手 |
| `X-Track-Video-Id` | `3NNhrqHZqlI` | 日志 / 去重 |
| `X-Track-Duration` | `257` | 秒数 |
| `X-Cache` | `hit` / `miss` | 是否命中服务端缓存 |

> `X-Track-Title` / `X-Track-Artists` 是 URL 编码的（HTTP 头不能直接放非 ASCII），
> bot 侧要 `urllib.parse.unquote` / `decodeURIComponent` 解一下。
> 若缓存条目缺少元数据，这两个头可能缺省。

### 响应（`?mode=json`：先看信息再决定）

`POST /download?mode=json` 不回二进制，而是回：

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

适合两种场景：

1. bot 有上传体积限制：先看 `filesize`，太大就不下了，改发链接；
2. bot 想流式上传：拿 `file_url` 去 `GET /file/{token}`（支持 `Range`，可分片）。

**注意**：若只传 `video_id` 且缓存条目没有标题元数据，`title` / `artists` / `display_name` / `duration_seconds` 可能为空，但 `file_url` / `filesize` / `video_id` 仍可用。通过 `session_id+index/name` 走冷下载时，元数据通常完整。

---

## 4. `GET /file/{token}` 取回已下载文件

- `token` 只能来自 `mode=json` 的 `file_url`，**不接受任意路径**（防目录穿越）。
- 支持 `Range` 请求（`206 Partial Content`），适合大文件分片上传。
- 文件默认缓存 **24 小时**，过期后返回 `404`。

---

## 5. `GET /healthz` 探活

实测：

```json
{"status":"ok","version":"0.1.0","default_limit":10,"max_limit":20,"ytdlp":"2026.07.04"}
```

bot 启动时先打这个，确认对接服务活着，并确认 `ytdlp` 字段非空。

---

## 6. 错误处理

所有错误响应统一结构：

```json
{"code": "SESSION_EXPIRED", "message": "会话已过期，请重新搜索", "detail": null}
```

| HTTP | `code` | 含义 | bot 该怎么做 |
| --- | --- | --- | --- |
| 400 | `INVALID_REQUEST` | 参数非法（空 query、`limit<1`、没给任何选歌方式） | 提示用法，别重试 |
| 401 | `UNAUTHORIZED` | `X-API-Key` 缺失或错误 | 查 bot 配置 |
| 404 | `NOT_FOUND` | `index` 越界 / `name` 没匹配上 / 文件已清理 | 让用户重选 |
| 409 | `AMBIGUOUS_NAME` | `name` 命中多条同名 | `detail` 里有候选清单，让用户改用序号 |
| 410 | `SESSION_EXPIRED` | `session_id` 过期 | 提示重新搜索 |
| 413 | `FILE_TOO_LARGE` | 超过服务端 `MAX_FILESIZE_MB` | 提示曲子太大，或改发链接 |
| 499 | `CANCELED` | 客户端取消 | 一般忽略 |
| 502 | `UPSTREAM_ERROR` | YouTube 侧失败 / 解析失败 | 可重试一次，仍失败就报错给用户 |
| 504 | `TIMEOUT` | 上游超时（含下载排队过久） | 同上 |
| 500 | `INTERNAL_ERROR` | 未分类内部错误 | 记日志，提示稍后重试 |

**关于并发限流**：超出 `MAX_CONCURRENT_DOWNLOADS` 时，服务端会**排队等待**，**不会立刻返回 429 `RATE_LIMITED`**。排队/下载超时映射为 `504 TIMEOUT`。bot 侧仍应对 `502` / `504` 做有限重试。

**最小容错建议**：`502` / `504` 重试一次；`4xx` 一律不重试，直接把 `message` 展示给用户。

---

## 7. bot 侧实现清单

> 并发相关的细节见 §9「多人同时使用」。

写搜索功能时按这个勾：

- [ ] 启动时打 `/healthz`，失败就告警
- [ ] `/play <关键词>` 走 `POST /search`，`limit` 由 bot 配置决定（1–20）
- [ ] 传 `min_score`（建议 `0.35`），`total == 0` 时提示「没搜到」
- [ ] 把 `results` 渲染成 `index. display_name (duration)` 列表
- [ ] 若支持「发官方 MV」：读 `has_official_video` / `official_video_url`（或 `official_video_id`）
- [ ] 切记：`/download` 继续用 `video_id`，不要误用 `official_video_id` 当下音频 ID（除非你明确知道平台/链路支持）
- [ ] 每个会话存 `session_id`（含过期时间），别用全局变量
- [ ] 用户回复纯数字走 `index`；回复文字走 `name`
- [ ] 处理 `410`（过期）、`409`（同名歧义）、`413`（太大）三个高频错误
- [ ] 上传前检查 `Content-Length` 是否超过平台限制
- [ ] `X-Track-Title` / `X-Track-Artists` 记得 URL 解码
- [ ] 下载超时给足（首次下载 + 转码可能几十秒），建议 300s
- [ ] 同一用户并发下载做个限制，别把桥接服务打满

---

## 8. 契约变更承诺

服务端后续实现时保证：

- **不会**删除或重命名本文档已列出的字段；
- **不会**改变 `index` 的 1-based 语义、`display_name` 的 `"标题 - 歌手"` 格式；
- **不会**改变已列出的 HTTP 状态码与 `code` 取值；
- 只可能 **新增可选字段** —— bot 侧解析 JSON 时请忽略未知字段（Python / Go 默认行为即可）。

---

## 9. 多人同时使用（并发行为）

服务端是 Go 原生 `net/http`，**每个请求一个 goroutine**，天生并发。多个用户互不阻塞。两条链路的并发特性不一样，bot 侧要区别对待。

### 9.1 搜索：几乎无上限

`POST /search` 只是转发一次 HTTP 请求给 YouTube Music 再打分，无锁、无磁盘写。

- 所有 `/search` 共用一个 `http.Client`（keep-alive 连接池复用），不会每次新建连接；
- 打分是纯 CPU 计算，20 条数据微秒级；
- session 存储用 **分片 map**（按 `session_id` 哈希分到多个桶，各桶独立加锁），多人同时搜索不会抢同一把锁。

**结论**：十来个人同时搜索完全没问题，瓶颈是 YouTube 侧的响应时间（实测单次约 0.5–1.2s），不是本服务。

### 9.2 下载：有意限流，会排队

`POST /download` 要起 yt-dlp 进程 + ffmpeg 转码，是重活。服务端做了三层保护：

| 机制 | 作用 | 相关配置 |
| --- | --- | --- |
| 并发信号量 | 同时最多跑 N 个 yt-dlp，超出的请求**排队等待**（不是立刻失败） | `MAX_CONCURRENT_DOWNLOADS`（默认 `2`） |
| 同曲去重（singleflight） | 10 个人同时点同一首歌，**只下载 1 次**，10 个请求共享结果 | 无需配置 |
| 磁盘缓存 | 已下过的歌直接回文件，不再跑 yt-dlp（响应头 `X-Cache: hit`） | `CACHE_TTL_SECONDS`（默认 24h） |

所以多人场景下的实际表现：

- **点同一首歌** → 第一个人触发下载，其余人等同一个结果，几乎同时拿到；之后所有人都是缓存命中，秒回。
- **点不同的歌** → 按 `MAX_CONCURRENT_DOWNLOADS` 排队。默认 2 意味着第 3 个人要等前面的下完（单曲实测 4~6s）。
- **排队太久** → 请求可能超时，返回 `504 TIMEOUT`（**不是 429**）。bot 侧应有限重试。

### 9.3 多人使用时的配置建议

`.env` 里按 bot 的活跃用户数调：

```ini
# 几个人用：默认值够了
MAX_CONCURRENT_DOWNLOADS=2

# 十来人同时用：调高，注意每个 yt-dlp+ffmpeg 约吃 1 个核
MAX_CONCURRENT_DOWNLOADS=4

# 缓存给足，多人点热门歌时命中率就是性能
CACHE_TTL_SECONDS=86400
CACHE_MAX_TOTAL_MB=4096
```

> 不建议超过 CPU 核数。yt-dlp 是下载（IO 密集）+ ffmpeg 转码（CPU 密集），调太高会把机器打满反而更慢。

### 9.4 bot 侧必须注意的三件事

1. **`session_id` 要按会话隔离存**。每次 `/search` 都会生成新的 `session_id`，
   A 用户的搜索结果和 B 用户的完全独立。**别用全局变量存**，否则 B 搜索会覆盖 A 的列表，
   A 回「3」就点到 B 的歌。建议用 `dict[(chat_id, user_id)] -> session_id`。
2. **给每个用户加下载并发限制**。比如同一用户同时最多 1 个下载任务，
   防止一个人连点 20 次把队列占满、其他人全在排队。
3. **下载超时给足**（建议 300s）。排队 + 下载 + 转码加起来可能到几十秒；
   超时设太短会出现「服务端下完了但 bot 已经放弃」的浪费。

### 9.5 实测并发数据（G9）

| 能力 | 状态 / 数据 |
| --- | --- |
| 每请求独立 goroutine（`net/http`） | **可用**（框架特性） |
| 分片 map session 存储 | **可用**；`go test -race` 通过 |
| singleflight 同曲去重 | **可用**；同曲 20 并发冷下载 wall ≈ **5159 ms**（约一次下载），随后 `post_cached=true` |
| 并发信号量限流 | **可用**；超出排队，超时 → `504 TIMEOUT` |
| `/search` 压测 | 20 并发 × 60 请求：**QPS ≈ 31.73**，**P50 ≈ 480 ms**，**P99 ≈ 745 ms**，成功 60/60 |
| 服务内存 | WorkingSet ≈ **22.7 MB** |
| 工具版本 | healthz `ytdlp=2026.07.04`（项目内 `bin/yt-dlp.exe`） |

完整报告：[`goal-1/e2e-report.json`](../goal-1/e2e-report.json)。以上是本机快照，不是 SLA；上游 InnerTube / yt-dlp 抖动会导致波动。

---

## 10. 示例代码

### PowerShell

```powershell
$Base = "http://127.0.0.1:8787"

$search = Invoke-RestMethod -Method Post -Uri "$Base/search" `
  -ContentType "application/json; charset=utf-8" `
  -Body (@{ query = "lemon kenshi yonezu"; limit = 3 } | ConvertTo-Json)

$search.results | ForEach-Object {
  "{0}. {1}  ({2})  score={3:N2}" -f $_.index, $_.display_name, $_.duration, $_.match_score
}

$dl = @{ session_id = $search.session_id; index = 1 } | ConvertTo-Json
Invoke-WebRequest -Method Post -Uri "$Base/download" `
  -ContentType "application/json; charset=utf-8" -Body $dl -OutFile "out.mp3"
```

### Python

```python
import json
import urllib.parse
import urllib.request

BASE = "http://127.0.0.1:8787"

def post_json(path, payload, query=""):
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        BASE + path + query,
        data=data,
        headers={"Content-Type": "application/json; charset=utf-8"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=300) as resp:
        ctype = resp.headers.get("Content-Type", "")
        body = resp.read()
        if "application/json" in ctype:
            return json.loads(body.decode("utf-8")), resp.headers
        return body, resp.headers

search, _ = post_json("/search", {"query": "lemon kenshi yonezu", "limit": 5, "min_score": 0.35})
for item in search["results"]:
    print(f'{item["index"]}. {item["display_name"]} ({item["duration"]})')

audio, headers = post_json("/download", {"session_id": search["session_id"], "index": 1})
title = urllib.parse.unquote(headers.get("X-Track-Title", ""))
print("got", len(audio), "bytes, title=", title)
open("track.mp3", "wb").write(audio)
```

### Go

见仓库根目录 [`README.md`](../README.md) 中的 Go bot 示例。

---

## 11. 依赖与部署提示

- 外部工具必须放在**本项目** `bin/`：`yt-dlp.exe`、`ffmpeg.exe`、`ffprobe.exe`。不要挂外部工具目录。
- `.\scripts\get-ytdlp.ps1` 下载 yt-dlp；ffmpeg/ffprobe 自行拷贝 standalone/essentials 到 `bin/`。
- `.\run.ps1` / `.\run.ps1 -Background` 启动服务（优先 `bin/`）。
- 完整安装与配置表见 [`README.md`](../README.md)。
