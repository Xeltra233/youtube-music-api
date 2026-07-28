# Tasks

- [x] T1 下载层：format 扩展 + yt-dlp 视频参数分支
  - 完成：
    - `NormalizeFormat` 支持 `mp4`；`IsVideoFormat` / `IsAudioFormat`
    - 视频：`--merge-output-format mp4` + `bv*+ba` selector，无 extract-audio
    - 音频路径保持 extract-audio；视频失败不走 raw 音频旁路
    - 视频 cache bitrate 用 `"0"`；`contentTypeFor(mp4)=video/mp4`
    - 单测：`TestBuildYtdlpArgsVideoVsAudio`、`TestDownloadMP4Video`、NormalizeFormat(mp4)
  - 验证：`go test ./internal/download` 通过
  - 下一步：T2 HTTP 契约

- [x] T2 HTTP 契约与错误文案
  - 完成：
    - `/download` 透传 `format=mp4`（下载层已支持）
    - 新增 `TestDownloadMP4JSONAndBinary`：JSON format/file_url + 二进制 `video/mp4`
    - 新增 `TestDownloadInvalidFormatMentionsMP4`
    - 上游失败文案改为「媒体下载失败」
  - 验证：`go test ./internal/httpapi ./internal/download` 通过
  - 下一步：T3 文档同步

- [x] T3 文档同步
  - 完成：
    - `BOT-INTEGRATION.md`：官方 MV 可 `format=mp4` 下载；方式 E 示例；去掉“不能下 MV 文件”
    - `BOT-PARAMS.txt`：`mv/video/official` → `format=mp4` + `official_video_id`
    - `README.md`：音频/视频分流一句
  - 验证：文档含 `format=mp4` / `video/mp4`，旧“不提供整段 MV”表述已清除
  - 下一步：T4 全量验证

- [ ] T4 全量测试 + live 抽样（可降级）+ 收尾
  - `go test ./...`
  - 可选 live：official id + mp4
  - 清理临时脚本；提交（无敏感数据）

## 检查点

每完成 3 个 task 做一次构建/测试大检查，结果写回本文件。

## 最终 review 清单

- [ ] 音频下载未回归
- [ ] mp4 视频下载可用（单测/或 live）
- [ ] 文档与实现一致，bot 可按文档发官方 MV 文件
- [ ] 无敏感数据提交
