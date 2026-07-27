# 用户原始输入（逐字保留）

帮我从github上找哪种youtube music的仓库，我要写一个项目，可以导出api的，这个api用于bot，bot会转发歌曲近似名字信息给这个项目，然后项目去搜索然后返回歌单近似10首，最大20，可以设置，设置的部分我决定从bot哪里高，不过你这里起码要支持返回那么多，如果没有那么多能搜到多少就多少，然后我bot选择好数字，或者名字（歌单上面显示的全名），然后我们的项目就会去下载这个music然后转发给bot

## 补充说明（非用户原话，仅记录来源）

- 以上文本来自 goal objective，未做任何改写。
- 后续若用户追加需求，追加到本文件末尾的「追加需求」小节，并同步更新 plan.md / tasks.md。

## 追加需求

### 追加 1（2026-07-26，Task 3 执行中）

> 记得给我改成go
>
> 我要高性能

影响：技术栈由 Python/FastAPI 全面改为 Go。已作废的 Python 产物（`pyproject.toml`、`app/*.py`、`scripts/*.py`、`.venv`）需删除，`plan.md` / `tasks.md` 同步重写。R1–R10 需求本身不变。

### 追加 2（2026-07-26，Task G1 收尾时）

> 你写一个文档，我去给bot加上搜索歌曲的功能

影响：新增需求 R12 —— 需要**先行交付**一份面向 bot 开发者的接口契约文档（`docs/BOT-INTEGRATION.md`），让用户可以与服务端并行开发 bot 侧搜索功能。文档必须明确标注每个接口的实现状态，并在后续 Task 实现完成后同步更新（Task G10 负责最终校订）。

### 追加 3（2026-07-26，Task G1 收尾时）

> 你他妈的文件给我写那的，项目全部放在C:\project\test\youtube-music-api
>
> 所有的文件都放这里

影响：项目根目录从 `C:\Users\Xeltra\Desktop\ytmusic-bridge` 迁移到 `C:\project\test\youtube-music-api`（含 `.git`、`goal-1`、Go 源码），旧目录已删除。后续所有文件只允许创建在新根目录下。plan.md §2 / §8 的路径记录需同步。

### 追加 4（2026-07-27，Task G10 执行中）

> ？你在干嘛，如果你要用，请你单独拷贝一份进来，因为这是个完整项目
> 可以参考这个项目https://github.com/krtirtho/spotube
> 而且你为什么老用控制台写东西，用apply_patch不行吗
> 你记得把你为了编写而编写的脚本删除
>
> 去你妈的你必须给我用apply_patch那是你的用法不对导致的
>
> 你把apply_patch必须使用写进goal文档，每次捣腾其他方法还搞不好

影响与硬约束：
1. **写文件必须使用 `apply_patch`**（正确首行是 `*** Begin Patch`，禁止 `*** Begin Patch ***`）。禁止用 PowerShell/`Set-Content`/Python/`Out-File` 等控制台方式直接写中文文档或源码；此前误用导致编码与路径转义损坏。
2. 外部依赖（yt-dlp / ffmpeg / ffprobe）必须**单独拷贝进本项目** `bin/`，禁止挂外部工具目录；spotube 仅作交互参考，不改变本服务架构、不引入其运行时依赖。
3. 为编写而临时创建的脚本/探针文件（如 `_gen_*.py`、`_probe_*.py`、`_enc_test.md`）必须在 task 结束前删除。
4. 本约束同步写入 `plan.md` / `tasks.md`，后续 task 一律遵守。
