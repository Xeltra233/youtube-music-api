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

- [ ] T2 匹配与填充：search 层为每条结果写入官方视频字段
  - `search.Item` 增加 `OfficialVideoID` / `OfficialVideoURL` / `HasOfficialVideo`
  - songs+videos 并行（或可控串行）拉取
  - OMV 优先 + display_name/title 相似度匹配 + 阈值
  - videos 失败降级为空字段，主搜索仍成功
  - 单测：命中 / 未命中 / 上游 videos 失败 / 防御性拷贝
  - 验证：`go test ./internal/search`

- [ ] T3 HTTP 契约透出：`ResultItem` JSON 新字段 + 回归
  - `httpapi.ResultItem` / `itemToAPI` 映射新字段
  - server 测试断言 JSON 出现 `official_video_id` 等
  - 确认旧字段与 download 链路不受影响
  - 验证：`go test ./internal/httpapi ./internal/session ./internal/search`

- [ ] T4 bot 参数文档
  - 更新 `docs/BOT-INTEGRATION.md`：字段表、示例 JSON、与 `video_id` 区别、推荐 bot 用法
  - 更新 `docs/BOT-PARAMS.txt`：bot 侧如何用官方视频字段发 MV
  - 必要时 README 一句索引
  - 验证：文档字段名与代码 tag 完全一致；中文无乱码

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
