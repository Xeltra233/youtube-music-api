# Tasks

- [x] T1 上游能力：ytmusic 支持 videos 过滤搜索 + 解析 `musicVideoType`
  - 完成：
    - 新增 `SearchFilter` / `SearchFilterSongs` / `SearchFilterVideos`
    - `Client.Search` 仍默认 songs；新增 `Client.SearchFilter`
    - `videosFilterParams = EgWKAQIQAWoMEA4QChADEAQQCRAF`
    - `Track.MusicVideoType` 从 watchEndpoint / menu 解析
    - fixture：`testdata/search_videos_mixed.json`（OMV/UGC/ATV/menu-only）
    - 单测：`TestParseSearchResponseVideosMixedFixture`、`TestSearchFilterParams`、`TestSearchFilterVideosUsesVideosParams`；既有 lemon fixture 断言 ATV
  - 验证：`go test ./internal/ytmusic` 通过（含 live 抽样）
  - 残余风险：真实 videos 响应布局可能比 fixture 更杂，T2 匹配时再兜底
  - 下一步：T2 search 层填充 official video 字段

- [x] T2 匹配与填充：search 层为每条结果写入官方视频字段
  - 完成：
    - `search.Item` 增加 `OfficialVideoID` / `OfficialVideoURL` / `HasOfficialVideo`
    - `VideoUpstream` 可选接口；`*ytmusic.Client` 自动并行 songs+videos
    - `attachOfficialVideos`：OMV 优先，无 OMV 时降级非 ATV；`MatchScore >= 0.55`
    - videos 上游失败只打 warn，主搜索仍成功且官方字段为空
    - 同 song `video_id` 不重复当作 official
  - 验证：`go test ./internal/search` 通过（含命中/未命中/降级/无 VideoUpstream）
  - 残余风险：真实 OMV 标题噪声可能导致个别漏匹配；T5 live 抽样再确认
  - 下一步：T3 HTTP `ResultItem` 透出 JSON 字段

- [x] T3 HTTP 契约透出：`ResultItem` JSON 新字段 + 回归
  - 完成：
    - `ResultItem` 增加 `official_video_id` / `official_video_url` / `has_official_video`
    - `itemToAPI` 全量映射
    - `TestSearchOK` 断言有值/空值；`TestSearchOfficialVideoJSONFieldsPresent` 校验原始 JSON key
  - 验证：`go test ./internal/httpapi ./internal/session ./internal/search` 通过
  - 残余风险：文档尚未同步（T4）
  - 下一步：T4 bot 参数文档

## 检查点（T1–T3）

- 需求未偏：官方视频作为 `/search` results 新增字段，旧 `video_id` 不变
- `go test ./internal/ytmusic ./internal/search ./internal/httpapi ./internal/session` 通过
- 无 download 行为改动
- 文档尚未更新（T4）

- [x] T4 bot 参数文档
  - 完成：
    - `docs/BOT-INTEGRATION.md`：示例 JSON、字段表、与 `video_id` 区别、推荐 bot 行为、清单项
    - `docs/BOT-PARAMS.txt`：尾参 `mv/video/official`、delivery=video、4.5 发 MV 流程、伪代码
    - `README.md`：一句索引官方 MV 字段
  - 验证：三字段名与 `httpapi.ResultItem` json tag 一致（`rg official_video`）
  - 下一步：T5 全量测试 + live 抽样

- [ ] T5 全量验证 + 抽样 live（可降级）+ 收尾
  - `go test ./...`
  - 若网络可用：抽样 1~2 个 query，确认有歌能出非空 `official_video_id`
  - 网络不可用则记录降级原因，不阻塞以单测+契约为准
  - 清理临时脚本；需要时提交（无敏感数据）

## 检查点

每完成 3 个 task 做一次构建/测试大检查，结果写回本文件。

## 最终 review 清单

- [ ] 旧 `video_id` 语义未变
- [ ] 新字段稳定返回（有值或空值）
- [ ] 文档足够 bot 直接接入
- [ ] 测试通过
- [ ] 无敏感数据提交
