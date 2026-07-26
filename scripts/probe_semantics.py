"""探针 2：验证 ytmusicapi 的 limit 语义、中文查询、结果不足场景。

用法：.venv\\Scripts\\python.exe scripts/probe_semantics.py
"""

from __future__ import annotations

import sys

from ytmusicapi import YTMusic

CASES: list[tuple[str, int]] = [
    ("周杰伦 晴天", 10),
    ("米津玄師 レモン", 10),
    ("lemon", 5),
    ("lemon", 20),
    ("zzqqxxwweeyy nonexistent song 9182", 10),
]


def main() -> int:
    yt = YTMusic()
    failures = 0
    for query, limit in CASES:
        try:
            results = yt.search(query, filter="songs", limit=limit)
        except Exception as exc:  # noqa: BLE001
            print(f"query={query!r} limit={limit} FAILED {type(exc).__name__}: {exc}")
            failures += 1
            continue
        print(f"query={query!r} limit={limit} -> raw_count={len(results)}")
        for item in results[:2]:
            artists = ", ".join(a.get("name", "") for a in item.get("artists") or [])
            print(
                f"    {item.get('videoId')} | {item.get('title')} | "
                f"{artists} | {item.get('duration')}"
            )
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
