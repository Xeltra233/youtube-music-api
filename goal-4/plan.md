# Plan：搜索结果返回官方音乐视频参数

## 1. 需求理解

用户要在现有 bot 搜索链路里，给每条候选歌曲补上**官方音乐视频**相关返回字段。  
bot 侧之后加自己的命令参数时，可直接拿这些字段发官方 MV（例如发 YouTube 链接 / 视频消息），而不必自己再去猜哪个是官方视频。

同线程补充：服务端必须把**参数文档写好**，方便 bot 接入。

不在本 goal 默认范围内：

- 不改 bot 命令解析本身（bot 不在本仓库）
- 不强制实现“下载整段视频文件”接口（当前 `/download` 是音频链路）
- 不破坏现有 `video_id` 语义（它继续表示可下载音频的曲目 ID）

## 2. 现状

- `POST /search` 只走 YouTube Music InnerTube **songs** 过滤：
  - `params = EgWKAQIIAWoMEA4QChADEAQQCRAF`
  - 结果几乎全是 `MUSIC_VIDEO_TYPE_ATV`（音轨条目）
- 返回字段见 `docs/BOT-INTEGRATION.md` / `ResultItem`：
  - `video_id` / `title` / `artists` / `display_name` / `match_score` 等
- 本地 fixture `search_songs_lemon.json` 里没有 OMV；菜单也只有 credits/album/artist，没有现成 official video id
- 已知 videos 过滤参数：
  - `params = EgWKAQIQAWoMEA4QChADEAQQCRAF`
  - OMV 常见类型：`MUSIC_VIDEO_TYPE_OMV`

## 3. 目标 API 形状（默认假设）

在 `/search` 的 `results[]` **新增可选字段**（不删不改旧字段）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `official_video_id` | string | 官方音乐视频 YouTube ID；没有则为 `""` |
| `official_video_url` | string | 便于 bot 直接发送：`https://www.youtube.com/watch?v=...`；没有则为 `""` |
| `has_official_video` | bool | `official_video_id != ""` 的便捷布尔 |

约定：

1. 现有 `video_id` **继续表示歌曲音轨 ID**，供 `/download` 下音频。
2. `official_video_id` 仅用于“发官方 MV / 打开官方视频”，**不自动替换**下载 ID。
3. 找不到官方视频时字段仍返回，值为空/`false`，避免 bot 判空分支分叉过多。
4. 不新增请求必填参数；本阶段默认**总是尝试填充**官方视频字段。  
   若实测延迟明显，再加可选请求开关 `include_official_video`（默认 true 或 false 以实测为准），但第一实现优先“总是返回字段”。

## 4. 技术方案

### 4.1 数据获取

在 songs 搜索之外，对同一 `query` 再请求一次 **videos** 过滤搜索：

1. 解析 videos 结果，读取：
   - `videoId`
   - `musicVideoType`（优先 OMV）
   - title / artists / duration
2. 将 videos 候选与 songs 结果做匹配，给每首 song 选一个官方视频：
   - 优先 `MUSIC_VIDEO_TYPE_OMV`
   - 其次标题+艺人高相似（复用 `matching`）
   - 同 song 多候选时取最高分；低分阈值以下视为没有
3. 并发：songs 与 videos 两次上游请求可并行，降低延迟。
4. videos 上游失败时：搜索主链路仍成功，官方视频字段全部留空（降级，不把整次 search 打成 502），并在日志中记一笔。

### 4.2 代码落点

| 层 | 改动 |
| --- | --- |
| `internal/ytmusic` | `Search` 支持 filter（songs/videos）；解析 `musicVideoType`；Track 增加 `MusicVideoType` |
| `internal/search` | Item 增加官方视频字段；并行取 videos 并匹配填充 |
| `internal/httpapi` | `ResultItem` JSON 字段透出 |
| `internal/session` | 随 `search.Item` 自动带上（供后续若要用）；本 goal 不要求 download 改行为 |
| docs | `BOT-INTEGRATION.md` / `BOT-PARAMS.txt` / 必要时 README 增补字段说明与 bot 用法 |

### 4.3 匹配策略（默认）

对每条 song `S`：

1. 在 videos 列表中过滤 `MUSIC_VIDEO_TYPE_OMV`（若完全没有 OMV，再放宽到非 ATV 的 video）
2. 计算 `score = MatchScore(S.display_name, V.display_name)`，可叠加 title-only / artist 奖励
3. `score >= 0.55`（可调）才采纳
4. 一对多时只取最高分；同一 official video 可被多首歌引用（少见，可接受）

### 4.4 bot 接入文档要点

文档需写清：

- 新字段名、类型、空值语义
- 与旧 `video_id` 的区别
- 推荐 bot 行为：
  - 用户要听歌 / 下音频 → 仍用 `video_id` + `/download`
  - 用户要官方 MV → 用 `official_video_id` / `official_video_url` 发视频或链接
  - `has_official_video == false` 时提示“没有官方视频”，回退音频或普通结果
- 示例 JSON
- 兼容性：旧 bot 可忽略未知字段

## 5. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 多一次上游请求增加延迟 | 并行 songs+videos；失败降级 |
| 匹配错 MV（翻唱/直播版） | 优先 OMV + 相似度阈值；单测覆盖 |
| 部分歌没有官方视频 | 空字符串 + false，不报错 |
| 契约破坏 | 只新增字段；旧字段语义不变 |
| 编码/文档写入 | 一律 `apply_patch`；中文文档用 UTF-8 |

## 6. 验证方式

1. 单元：parse videos fixture、匹配函数、API JSON 含新字段
2. `go test ./...`
3. 若环境允许：对已知有 OMV 的歌（如流行日推/主流流行曲）做一次 live search 抽样
4. 文档中的示例字段与真实响应一致
5. 回归：原 `/search` 旧字段、`/download` 行为不变

## 7. 回滚

- 回滚 `ytmusic`/`search`/`httpapi` 相关提交即可
- 文档同步回滚
- 因只新增字段，旧 bot 不受影响

## 8. 默认假设（禁止向用户提问，已写入）

1. “官方视频”= YouTube Music / YouTube 上的 **Official Music Video（OMV）**，不是歌词视频/live 优先。
2. 返回参数加在 **`/search` results[]**，不是改 `/download`。
3. bot “发官方音乐视频”优先用 `official_video_url` / `official_video_id` 发平台支持的视频/链接，不要求本服务先下好 MP4。
4. 参数文档以 `docs/BOT-INTEGRATION.md` 为主，`docs/BOT-PARAMS.txt` 补 bot 侧用法摘要。
5. 写文件必须用 `apply_patch`。
