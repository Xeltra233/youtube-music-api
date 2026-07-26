# ytmusic-bridge

YouTube Music 搜索 + 下载 HTTP API，供 bot 调用。

- 搜索元数据：[`sigma67/ytmusicapi`](https://github.com/sigma67/ytmusicapi)
- 音频下载：[`yt-dlp/yt-dlp`](https://github.com/yt-dlp/yt-dlp) + 本机 ffmpeg

完整文档见 Task 10 交付（安装 / 配置 / 接口 / bot 接入示例）。

## 快速开始

```powershell
uv venv --python 3.13
uv pip install -e ".[dev]"
.\.venv\Scripts\python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 8787
```
