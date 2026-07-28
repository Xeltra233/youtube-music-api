# Plan：下载组件支持视频文件 + 文档同步

## 1. 需求理解

用户要求把现有基于 yt-dlp 的下载组件从“只下音频”扩展为**也支持视频下载**，并更新 bot 文档。

结合上一目标：

- `/search` 已返回 `official_video_id` / `official_video_url` / `has_official_video`
- 当前 `/download` 仅支持 `mp3` / `m4a` / `opus`，内部强制 `--extract-audio`
- 文档仍写“不下官方 MV 文件，只发链接”

本 goal 目标态：

1. 下载层可用 yt-dlp 拉**视频文件**（至少 `mp4`）
2. HTTP `/download` 接受视频 format
3. bot 可用 `official_video_id` + 视频 format 拿到文件再发送
4. 文档改掉“不能下视频”的表述，写清音频/视频分流

## 2. 现状

| 层 | 现状 |
| --- | --- |
| `NormalizeFormat` | 只允许 mp3/m4a/opus |
| `buildYtdlpArgs*` | 固定 `--extract-audio` + `-f ba/bestaudio/best` |
| `contentTypeFor` | 只有 audio/* |
| cache key | `videoID|format|bitrate`（视频可复用，bitrate 对视频可空/占位） |
| docs | 明确禁止用 official_video_id 走下载拿 MV 文件 |

yt-dlp 本身支持视频：去掉 extract-audio，用 `bv*+ba/b` 等 selector + 可选 merge 到 mp4。

## 3. API / 行为设计（默认假设）

### 3.1 format 扩展

| format | 类型 | Content-Type | 说明 |
| --- | --- | --- | --- |
| `mp3` / `m4a` / `opus` | 音频 | 现有 | 不变 |
| `mp4` | 视频 | `video/mp4` | 新增；默认官方 MV / 视频下载容器 |

可选后续：`webm`。第一实现只加 **`mp4`**，降低 bot 与缓存复杂度。

### 3.2 调用方式

```json
// 官方 MV 文件
{"video_id": "<official_video_id>", "format": "mp4"}

// 仍可 session 选歌后下音频
{"session_id":"s_xxx","index":1,"format":"opus"}
```

约定：

- `video_id` 语义不变：任意可下载的 YouTube ID
- 官方 MV：bot 传 search 返回的 `official_video_id` + `format=mp4`
- 歌曲音轨：继续传 song `video_id` + 音频 format
- 不把 session 里 song 的 `video_id` 自动替换成 official

### 3.3 下载实现

音频路径（现有）：

- `--extract-audio --audio-format ... -f ba/bestaudio/best`

视频路径（新增）：

- 不 extract-audio
- `-f bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/bv*+ba/b`
- 需要时 `--merge-output-format mp4`
- 不跑 audio-only raw→ffmpeg 转码旁路；视频失败走既有 client 策略重试

缓存：

- 仍按 `videoID|format|bitrate`
- 视频 format 时 bitrate 可用 `"0"` 或固定占位，避免与音频 192 冲突

响应：

- 二进制：`Content-Type: video/mp4`，`Content-Disposition` 带 `.mp4`
- `?mode=json`：`format=mp4`，`file_url` 可用

## 4. 代码落点

| 文件 | 改动 |
| --- | --- |
| `internal/download/sanitize.go` | `NormalizeFormat` 接受 `mp4`；可加 `IsVideoFormat` |
| `internal/download/ytdlp.go` | 按 format 分支 audio/video 参数构建 |
| `internal/download/downloader.go` | content-type、视频 bitrate 占位、不必要转码 |
| `internal/config` | 校验文案；默认仍 audio；不必新增默认视频 format |
| `internal/httpapi` | 错误信息/测试覆盖 mp4 |
| docs / README | 更新官方 MV = 可下载文件；BOT-PARAMS 映射 |

## 5. 风险

| 风险 | 对策 |
| --- | --- |
| 视频文件更大，易触发 MAX_FILESIZE | 沿用现有上限；文档提示；413 行为不变 |
| 合并需 ffmpeg | 项目已依赖 ffmpeg；缺失时明确错误 |
| 音频回归 | 单测继续覆盖 mp3 路径 |
| bot 误把 song video_id 当下 MV | 文档强调用 official_video_id + mp4 |

## 6. 验证

1. 单测：NormalizeFormat(mp4)、build 参数无 extract-audio、有 merge
2. fake runner：视频下载写 `.mp4`，音频路径不变
3. `go test ./internal/download ./internal/httpapi ./...`
4. 若网络允许：对已知 OMV id live 下 mp4（可降级）
5. 文档字段与示例一致

## 7. 回滚

- 回滚 download/httpapi/docs 相关提交
- 旧 bot 只用音频 format 不受影响

## 8. 默认假设

1. 第一版视频容器只支持 **mp4**
2. 通过现有 `POST /download` 的 `format` 扩展，不新开路径
3. 官方 MV 文件 = `official_video_id` + `format=mp4`
4. 写文件必须 `apply_patch`
5. 不擅自改生产密钥/不抢焦点
