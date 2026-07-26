"""一次性探针：验证本机能否 (1) 搜索 YouTube Music、(2) 下载单曲音频。

用法：
    .venv\\Scripts\\python.exe scripts/probe.py [--query "关键词"] [--proxy http://127.0.0.1:7890]

退出码：0 = 搜索与下载均成功；1 = 搜索失败；2 = 搜索成功但下载失败。
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path


def probe_search(query: str, proxy: str | None) -> list[dict]:
    from ytmusicapi import YTMusic

    kwargs = {}
    if proxy:
        kwargs["proxies"] = {"http": proxy, "https": proxy}
    yt = YTMusic(**kwargs)
    started = time.monotonic()
    results = yt.search(query, filter="songs", limit=20)
    elapsed = time.monotonic() - started
    print(f"[search] query={query!r} proxy={proxy} took={elapsed:.2f}s count={len(results)}")
    for item in results[:3]:
        artists = ", ".join(a.get("name", "") for a in item.get("artists") or [])
        print(
            f"  - videoId={item.get('videoId')} | {item.get('title')} | {artists} "
            f"| album={(item.get('album') or {}).get('name')} | dur={item.get('duration')}"
        )
    return results


def probe_download(video_id: str, proxy: str | None, out_dir: Path) -> Path:
    from yt_dlp import YoutubeDL

    ffmpeg_dir = None
    ffmpeg_path = shutil.which("ffmpeg")
    if ffmpeg_path:
        ffmpeg_dir = str(Path(ffmpeg_path).parent)

    opts: dict = {
        "format": "bestaudio/best",
        "outtmpl": str(out_dir / "%(id)s.%(ext)s"),
        "quiet": True,
        "no_warnings": True,
        "noprogress": True,
        "nocheckcertificate": True,
        "postprocessors": [
            {"key": "FFmpegExtractAudio", "preferredcodec": "mp3", "preferredquality": "192"}
        ],
    }
    if ffmpeg_dir:
        opts["ffmpeg_location"] = ffmpeg_dir
    if proxy:
        opts["proxy"] = proxy

    url = f"https://music.youtube.com/watch?v={video_id}"
    started = time.monotonic()
    with YoutubeDL(opts) as ydl:
        info = ydl.extract_info(url, download=True)
    elapsed = time.monotonic() - started

    produced = sorted(out_dir.glob(f"{video_id}.*"))
    print(
        f"[download] videoId={video_id} took={elapsed:.2f}s "
        f"title={info.get('title')!r} duration={info.get('duration')}s "
        f"files={[p.name for p in produced]}"
    )
    mp3 = [p for p in produced if p.suffix == ".mp3"]
    target = mp3[0] if mp3 else produced[0]
    print(f"[download] picked={target} size={target.stat().st_size} bytes")
    return target


def probe_ffprobe(path: Path) -> None:
    ffprobe = shutil.which("ffprobe")
    if not ffprobe:
        print("[ffprobe] ffprobe 不在 PATH，跳过校验")
        return
    out = subprocess.run(  # noqa: S603
        [
            ffprobe,
            "-v",
            "error",
            "-show_entries",
            "format=duration,size,bit_rate,format_name",
            "-of",
            "json",
            str(path),
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    print(f"[ffprobe] {json.dumps(json.loads(out.stdout).get('format', {}), ensure_ascii=False)}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--query", default="lemon kenshi yonezu")
    parser.add_argument("--proxy", default=None)
    args = parser.parse_args()

    try:
        results = probe_search(args.query, args.proxy)
    except Exception as exc:  # noqa: BLE001
        print(f"[search] FAILED: {type(exc).__name__}: {exc}")
        return 1
    if not results:
        print("[search] FAILED: 空结果")
        return 1

    video_id = results[0]["videoId"]
    tmp = Path(tempfile.mkdtemp(prefix="ytmusic-probe-"))
    try:
        path = probe_download(video_id, args.proxy, tmp)
        probe_ffprobe(path)
    except Exception as exc:  # noqa: BLE001
        print(f"[download] FAILED: {type(exc).__name__}: {exc}")
        return 2
    finally:
        print(f"[cleanup] tmp dir = {tmp}")
    print("[probe] OK: 搜索与下载均成功")
    return 0


if __name__ == "__main__":
    sys.exit(main())
