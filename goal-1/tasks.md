# Tasks：ytmusic-bridge

规则：每轮会话只执行一个未完成 task；完成后在该 task 下补写「做了什么 / 验证结果 / 剩余风险 / 下一步」；每 3 个 task 做一次大型全面检查-debug 循环（已在下方标出）。

状态图例：`[ ]` 未开始 / `[~]` 进行中 / `[x]` 已完成

---

## [ ] Task 1：项目骨架 + 独立虚拟环境 + 依赖安装

目标：在 `C:\Users\Xeltra\Desktop\ytmusic-bridge` 建立 Python 项目骨架，用 `uv` 创建 `.venv`，安装 `fastapi/uvicorn/ytmusicapi/yt-dlp/pydantic-settings/pytest/httpx/ruff/mypy`，`git init` + 首次提交。

独立验证：
- `.venv\Scripts\python.exe -c "import fastapi, ytmusicapi, yt_dlp; print(...)"` 打印版本成功。
- `git log --oneline` 有首次提交。
- 未修改全局 Python 环境（`pip list --user` 无新增，miniconda 不变）。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 2：网络与外部依赖实测（YouTube Music 搜索 + 单曲下载可行性）

目标：写一次性探针脚本（`scripts/probe.py`），验证 `ytmusicapi.search` 与 `yt_dlp` 下载在本机网络可用；记录是否需要 `PROXY`/`COOKIES_FILE`，把结论写回 `goal-1/plan.md` §2/§6。

独立验证：
- 探针输出真实搜索结果条数与前 3 条标题。
- 成功下载一首短音频到临时目录，`ffprobe` 能读出时长。
- 若失败，记录准确错误码与已尝试的 client/代理组合。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 3：配置模块 + 搜索服务层（limit 夹紧 / 不足即返回 / display_name 归一化）

目标：实现 `app/config.py`（pydantic-settings，含 `DEFAULT_LIMIT=10`、`MAX_LIMIT=20` 等）与 `app/search.py`（调用 ytmusicapi、归一化候选、生成 `display_name`、截断到 `limit_used`）。

独立验证：
- pytest 单测（mock ytmusicapi）：limit 缺省=10；limit=25→20；limit=0→报错；返回 8 条时 total=8 不报错。
- `display_name` 为「Title - Artist1, Artist2」且去除多余空白。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 3.5：大型全面检查-debug 循环 #1（覆盖 Task 1–3）

检查项：需求是否偏离、代码 bug、类型检查（mypy）、构建/导入、测试、安全（无路径穿越/无密钥泄漏）、数据一致性、回滚方案、文档同步。结果写入本文件。

结果：

---

## [ ] Task 4：会话/候选快照层（session_id + TTL），保证按序号选择不漂移

目标：`app/sessions.py`，内存字典 + TTL 清理；`/search` 生成 session 并保存候选快照；提供按 `index` / `name` 查候选的函数。

独立验证：pytest：index 越界报 404；过期 session 报 410；1-based 序号映射正确。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 5：按名字选择（全名精确 → 归一化 → 唯一模糊 → 歧义 409）

目标：`app/select.py` 实现 R7/R8 的完整选择优先级：`video_id` > `session_id+index` > `session_id+name`。

独立验证：pytest：全名精确命中；大小写/空白差异命中；两条同名返回 409 且带候选清单；无命中 404。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 6：下载服务层（yt-dlp + ffmpeg 转码 + 缓存去重 + 单飞 + 并发限流 + 体积上限）

目标：`app/downloader.py`：按 `video_id` 下载 bestaudio，转 `AUDIO_FORMAT`（默认 mp3 192k），写入 `DOWNLOAD_DIR`，同 key 复用缓存，asyncio 信号量限流，超过 `MAX_FILESIZE_MB` 报 413，文件名清洗防路径穿越。

独立验证：单测（mock yt-dlp）覆盖缓存命中/单飞/体积超限/文件名清洗；live 测试真实下载一首歌成功。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 6.5：大型全面检查-debug 循环 #2（覆盖 Task 4–6）

检查项同 #1，另加：并发下载压力（同一首歌 5 并发）、磁盘写入一致性、异常路径清理临时文件。结果写入本文件。

结果：

---

## [ ] Task 7：HTTP API 层（FastAPI 路由 + 鉴权 + 错误模型 + OpenAPI 文档）

目标：`app/main.py` 落地 `GET /healthz`、`POST /search`、`POST /download`（二进制 / `?mode=json`）、`GET /file/{token}`；可选 `X-API-Key`；统一错误 JSON（`code`/`message`/`detail`）。

独立验证：`httpx` + `TestClient` 全路由测试；`/docs` 与 `/openapi.json` 可访问；无 key 时不校验、有 key 时 401 生效。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 8：缓存清理任务 + 启动脚本（run.ps1 / .env.example）

目标：后台 TTL 清理协程（含目录总量上限）；`run.ps1` 一键启动；`.env.example` 列全部配置项及默认值。

独立验证：TTL 设 1 秒的单测证明文件被清理；`run.ps1` 实测启动并 `GET /healthz` 返回 ok（后台窗口隐藏，不抢焦点）。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 9：端到端真实联调（search → index 选择 → 拿到音频；search → 全名选择 → 拿到音频）

目标：`scripts/e2e_demo.py`（模拟 bot），跑通两条真实链路，输出证据（条数、display_name、文件路径、体积、ffprobe 时长）。

独立验证：脚本退出码 0；两次下载文件均可播放；第二次同曲命中缓存（耗时显著下降）。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 9.5：大型全面检查-debug 循环 #3（覆盖 Task 7–9）

检查项同 #1，另加：真实网络异常（断网/超时/无效 videoId）时 API 返回是否可读、bot 侧是否能据此重试。结果写入本文件。

结果：

---

## [ ] Task 10：文档交付（README + bot 接入指南 + GitHub 选型说明）

目标：`README.md` 写清安装/配置/启动/接口示例（含 PowerShell 与 Python bot 示例代码）、limit 规则、错误码表、缓存与合规说明；把 plan.md §3 的 GitHub 选型结论固化到 README「上游依赖」小节。

独立验证：按 README 从零在干净目录复现一次启动 + 一次搜索 + 一次下载；文档中的每条命令实际执行通过。

做了什么 / 验证结果 / 剩余风险 / 下一步：

---

## [ ] Task 11：最终 review（C 端体验 / 安全 / 数据一致性 / 权限 / 错误处理 / 测试 / 构建 / 文档 / 回滚）

目标：按 goal-mode 要求做最后最大 review，修复所有已知高风险问题，逐条对照 plan.md §1 的 R1–R10 给出证据，然后标记 goal 完成。

结果：
