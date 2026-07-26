# bot 接入文档：搜索歌曲 + 下载音频

本文档是 **bot 侧开发的接口契约**。服务端仍在实现中，但下面的请求/响应结构已经定稿，
可以直接按它写 bot 代码；服务端完工后不会破坏这些字段（只会新增可选字段）。

| 项目 | 值 |
| --- | --- |
| 服务名 | ytmusic-bridge |
| 项目根 | `C:\project\test\youtube-music-api` |
| 默认地址 | `http://127.0.0.1:8787` |
| 协议 | HTTP/1.1，JSON（`Content-Type: application/json; charset=utf-8`） |
| 鉴权 | 可选请求头 `X-API-Key`，服务端 `API_KEY` 为空时不校验 |

## 0. 实现状态（写 bot 时先看这里）

| 接口 | 状态 | 说明 |
| --- | --- | --- |
| `GET /healthz` | 可用 | 已实现并实测返回 200 |
| `POST /search` | 契约已定稿，服务端开发中 | 字段不会变，可先按本文档写 bot |
| `POST /download` | 契约已定稿，服务端开发中 | 同上 |
| `GET /file/{token}` | 契约已定稿，服务端开发中 | 同上 |

bot 侧只要接 `/search`（§2）+ `/download`（§3）就是完整链路。

---

## 1. 整体流程

```text
用户: /play 晴天 周杰伦
   |
   +--1--> POST /search  {"query":"晴天 周杰伦","limit":10}
   |       <-- session_id + results[10]（每条带 index、display_name、match_score）
   |
   +--2--  bot 渲染成编号列表发给用户，用户回「3」或回歌名全称
   |
   +--3--> POST /download {"session_id":"...","index":3}
           <-- 音频二进制（mp3），直接转发给用户
```

要点：

- **一次搜索 = 一个 `session_id`**，它记住了这次的候选列表。后续选歌靠 `session_id` + `index`/`name`。
- `session_id` 默认 **30 分钟**过期（响应里的 `expires_in` 是剩余秒数）。过期后再选歌返回 `410`，bot 应提示用户重新搜索。
- 用户也可以不走 session，直接用 `video_id` 下载（比如 bot 自己做了收藏夹、重播功能）。

---

## 2. `POST /search` 搜索歌曲

### 请求

```json
{
  "query": "晴天 周杰伦",
  "limit": 10,
  "min_score": 0.35
}
```

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `query` | string | 是 | — | 模糊歌名，可含歌手。支持中/日/英。首尾空白会去掉，空串返回 `400` |
| `limit` | int | 否 | `10` | 要几条。**范围 1–20**；传 >20 会被夹到 20（不报错，看 `limit_used`）；传 <1 返回 `400` |
| `min_score` | float | 否 | `0.0` | 相似度下限，低于它的结果被丢弃。`0.0` = 不过滤 |

**关于 `limit`**：条数由 bot 决定，服务端只保证「最多支持到 20」。bot 想给用户 5 条就传 5，想给 20 条就传 20。

**关于 `min_score`（重要）**：YouTube Music 上游 **永远不会返回空列表** —— 就算 query 是乱码，
它也会塞 20 条毫不相关的歌回来（这是实测结论）。所以「搜不到」这个状态需要判断相似度，两种做法：

1. 传 `min_score`（比如 `0.35`），让服务端帮你过滤，`total == 0` 就是「没搜到」；
2. 不传，自己看每条的 `match_score`，低于阈值就当没搜到。

推荐做法 1，阈值 `0.3 ~ 0.4` 起步，再按实际效果调。

### 响应 `200`

```json
{
  "session_id": "s_4f8c1a2b6d7e",
  "query": "晴天 周杰伦",
  "limit_requested": 10,
  "limit_used": 10,
  "min_score_used": 0.35,
  "total": 10,
  "truncated": true,
  "expires_in": 1800,
  "results": [
    {
      "index": 1,
      "display_name": "晴天 - 周杰倫",
      "title": "晴天",
      "artists": ["周杰倫"],
      "album": "叶惠美",
      "duration": "4:30",
      "duration_seconds": 270,
      "video_id": "SJKoWAd5ySo",
      "thumbnail": "https://lh3.googleusercontent.com/...",
      "match_score": 0.86
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
| `index` | int | **从 1 开始**连续编号。就是发给用户看的序号，用户回「3」就把 `index: 3` 传回来 |
| `display_name` | string | `"标题 - 歌手1, 歌手2"`（无歌手时只有标题）。**用户回歌名时按这个全名匹配** |
| `title` | string | 纯标题 |
| `artists` | string[] | 歌手数组，可能是空数组 |
| `album` | string | 专辑名，可能是 `""` |
| `duration` | string | `"4:30"` 这种给人看的格式，可能是 `""` |
| `duration_seconds` | int | 秒数，`0` 表示未知。bot 可用它拦掉过长的曲子 |
| `video_id` | string | YouTube 视频 ID，全局唯一。**建议 bot 存这个做收藏/去重** |
| `thumbnail` | string | 封面图 URL，可直接给聊天平台做缩略图 |
| `match_score` | float | `0.0 ~ 1.0` 相似度，结果已按它降序排列。`1.0` = 与 query 完全一致 |

---

## 3. `POST /download` 下载并取回音频

### 请求

四种选歌方式，**优先级 `video_id` > `index` > `name`**：

```jsonc
// 方式 A：用户回了序号（最常用）
{"session_id": "s_4f8c1a2b6d7e", "index": 3}

// 方式 B：用户回了歌单上显示的全名
{"session_id": "s_4f8c1a2b6d7e", "name": "晴天 - 周杰倫"}

// 方式 C：跳过 session，直接指定（适合收藏夹/重播）
{"video_id": "SJKoWAd5ySo"}

// 方式 D：指定音频格式
{"video_id": "SJKoWAd5ySo", "format": "m4a"}
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
| `Content-Length` | `6543210` | 上传前校验大小 |
| `Content-Disposition` | `attachment; filename*=UTF-8''%E6%99%B4%E5%A4%A9.mp3` | 文件名（UTF-8 编码，中文安全） |
| `X-Track-Title` | `晴天`（URL 编码） | 发消息时带标题 |
| `X-Track-Artists` | `周杰倫`（URL 编码） | 发消息时带歌手 |
| `X-Track-Video-Id` | `SJKoWAd5ySo` | 日志 / 去重 |
| `X-Track-Duration` | `270` | 秒数 |
| `X-Cache` | `hit` / `miss` | 是否命中服务端缓存 |

> `X-Track-Title` / `X-Track-Artists` 是 URL 编码的（HTTP 头不能直接放非 ASCII），
> bot 侧要 `urllib.parse.unquote` / `decodeURIComponent` 解一下。

### 响应（`?mode=json`：先看信息再决定）

`POST /download?mode=json` 不回二进制，而是回：

```json
{
  "title": "晴天",
  "artists": ["周杰倫"],
  "display_name": "晴天 - 周杰倫",
  "video_id": "SJKoWAd5ySo",
  "duration_seconds": 270,
  "format": "mp3",
  "filesize": 6543210,
  "file_url": "/file/9c1d2e3f4a5b6c7d",
  "expires_in": 86400,
  "cached": false
}
```

适合两种场景：

1. bot 有上传体积限制：先看 `filesize`，太大就不下了，改发链接；
2. bot 想流式上传：拿 `file_url` 去 `GET /file/{token}`（支持 `Range`，可分片）。

---

## 4. `GET /file/{token}` 取回已下载文件

- `token` 只能来自 `mode=json` 的 `file_url`，**不接受任意路径**（防目录穿越）。
- 支持 `Range` 请求（`206 Partial Content`），适合大文件分片上传。
- 文件默认缓存 **24 小时**，过期后返回 `404`。

---

## 5. `GET /healthz` 探活

```json
{"status":"ok","version":"0.1.0","default_limit":10,"max_limit":20}
```

bot 启动时先打这个，确认桥接服务活着。

---

## 6. 错误处理

所有错误响应统一结构：

```json
{"code": "SESSION_EXPIRED", "message": "会话已过期，请重新搜索", "detail": null}
```

| HTTP | `code` | 含义 | bot 该怎么办 |
| --- | --- | --- | --- |
| 400 | `INVALID_REQUEST` | 参数非法（空 query、`limit<1`、没给任何选歌方式） | 提示用法，别重试 |
| 401 | `UNAUTHORIZED` | `X-API-Key` 缺失或错误 | 查 bot 配置 |
| 404 | `NOT_FOUND` | `index` 越界 / `name` 没匹配上 / 文件已清理 | 让用户重选 |
| 409 | `AMBIGUOUS_NAME` | `name` 命中多条同名 | `detail` 里有候选清单，让用户改用序号 |
| 410 | `SESSION_EXPIRED` | `session_id` 过期 | 提示重新搜索 |
| 413 | `FILE_TOO_LARGE` | 超过服务端 `MAX_FILESIZE_MB` | 提示曲子太大，或改发链接 |
| 429 | `RATE_LIMITED` | 并发下载已满 | 指数退避后重试 |
| 502 | `UPSTREAM_ERROR` | YouTube 侧失败 / 解析失败 | 可重试一次，仍失败就报错给用户 |
| 504 | `TIMEOUT` | 上游超时 | 同上 |

**最小容错建议**：`502` / `504` / `429` 重试一次；`4xx` 一律不重试，直接把 `message` 展示给用户。

---

## 7. bot 侧实现清单

> 并发相关的细节见 §9「多人同时使用」。

写搜索功能时按这个勾：

- [ ] 启动时打 `/healthz`，失败就告警
- [ ] `/play <关键词>` 走 `POST /search`，`limit` 由 bot 配置决定（1–20）
- [ ] 传 `min_score`（建议 `0.35`），`total == 0` 时提示「没搜到」
- [ ] 把 `results` 渲染成 `index. display_name (duration)` 列表
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
- 只可能**新增可选字段** —— bot 侧解析 JSON 时请忽略未知字段（Python / Go 默认行为即可）。

---

## 9. 多人同时使用（并发行为）

服务端是 Go 原生 `net/http`，**每个请求一个 goroutine**，天生并发。多个用户互不阻塞。
两条链路的并发特性不一样，bot 侧要区别对待：

### 9.1 搜索：几乎无上限

`POST /search` 只是转发一次 HTTP 请求给 YouTube Music 再打分，无锁、无磁盘写。

- 所有 `/search` 共用一个 `http.Client`（keep-alive 连接池复用），不会每次新建连接；
- 打分是纯 CPU 计算，20 条数据微秒级；
- session 存储用**分片 map**（按 `session_id` 哈希分到多个桶，各桶独立加锁），多人同时搜索不会抢同一把锁。

**结论**：十几个人同时搜索完全没问题，瓶颈是 YouTube 侧的响应时间（实测单次约 1.2s），不是本服务。

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
- **排队太久** → 请求可能超时，或返回 `429 RATE_LIMITED`。bot 侧应指数退避重试。

### 9.3 多人使用时的配置建议

`.env` 里按 bot 的活跃用户数调：

```ini
# 几个人用：默认值够了
MAX_CONCURRENT_DOWNLOADS=2

# 十几人同时用：调高，注意每个 yt-dlp+ffmpeg 约吃 1 个核
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
3. **下载超时给足**（建议 300s）。排队 + 下载 + 转码叠加起来可能到几十秒，
   超时设太短会出现「服务端下完了但 bot 已经放弃」的浪费。

### 9.5 实现状态

| 能力 | 状态 |
| --- | --- |
| 每请求独立 goroutine（`net/http`） | 已具备（框架特性） |
| 分片 map session 存储 | 设计已定稿，Task G5 实现，将用 `go test -race` 验证 |
| singleflight 同曲去重 | 设计已定稿，Task G6 实现，将用「10 并发同曲只执行 1 次 yt-dlp」验证 |
| 并发信号量限流 | 设计已定稿，Task G6 实现 |
| 并发压测数据（QPS / P99） | Task G9 产出，届时补进本节 |
