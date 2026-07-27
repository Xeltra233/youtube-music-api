# Plan: cookies 目录 + 自动保活

## 需求
1. `COOKIES_DIR` 文件夹挂载（云容器）
2. 任意 Netscape txt 丢入即用，稳定为 `youtube.txt`
3. `COOKIES_KEEPALIVE` 环境变量开启自动保活回写
4. 下载 `yt-dlp --cookies` 与搜索 InnerTube 共用
5. 真实 cookie 不入库、不推送

## 方案
- `internal/cookies`：Resolve / RefreshDropIns / HeaderFromFile / KeepAliveOnce
- config：`COOKIES_DIR` / `COOKIES_KEEPALIVE` / `COOKIES_KEEPALIVE_INTERVAL_SECONDS`
- main：启动 resolve + 可选 keepalive 协程
- Docker：`/app/cookies`；README 写明挂载

## 风险
- 保活不能保证 Google 永不踢登录；脏 IP 仍需 `PROXY`
- yt-dlp 回写依赖上游可访问
