# Plan：收束视频下载 + 浏览器档案 Cookie 策略

## 1. 目标态

1. 收束上个 goal 遗留的 MP4 媒体校验：新下载和缓存命中都要确认存在视频流，坏缓存自动淘汰。
2. 支持把一个**已登录 YouTube 的浏览器档案**作为 Cookie 刷新来源。
3. 搜索与下载继续共用稳定的 Netscape 文件 `cookies/youtube.txt`，不让业务层直接并发读取浏览器数据库。
4. 浏览器档案读取失败、被锁、不可解密或已退出登录时，保留现有有效 Cookie 文件并回退，不中断匿名能力。
5. 管理状态只暴露来源、是否像登录态、刷新时间与错误摘要，不返回 Cookie 正文。
6. 文档写清本机、Windows、Linux 容器的配置与限制，并说明真实账号凭据不得进入配置或仓库。

## 2. 当前证据

### 2.1 现有 Cookie 路径

- `internal/cookies/store.go`：任意 Netscape 文件按修改时间提升为 `youtube.txt`。
- `internal/cookies/keepalive.go`：可用 yt-dlp 读取/回写临时快照进行保活。
- `internal/cookies/ytdlp_guard.go`：避免 yt-dlp 把稳定登录 jar 覆盖成匿名 visitor jar。
- `internal/ytmusic/client.go`：搜索每次请求从稳定文件生成 Cookie header。
- `internal/download/downloader.go`：下载为 yt-dlp 创建稳定文件快照。
- 管理端当前只支持上传 Netscape 文本和查看文件元数据。

### 2.2 浏览器档案能力

项目内 yt-dlp `2026.07.04` 已支持：

```text
--cookies-from-browser BROWSER[+KEYRING][:PROFILE][::CONTAINER]
```

支持 chrome/chromium/edge/firefox 等。服务端不采用账号密码直登；“YouTube 登录”在本方案中表示：使用一个已经通过真实浏览器完成登录的持久化档案，再由 yt-dlp 抽取 Cookie。

### 2.3 工作区基线

当前有未提交的下载层改动：缓存删除、MP4 `ffprobe` 视频流校验及相关测试。它们属于“继续上个 goal”的范围，后续实现不得覆盖或混入不相关重写。

## 3. 策略设计

### 3.1 来源优先级

默认兼容现状；配置浏览器来源后采用：

1. 浏览器档案同步成功且产物仍像登录态：原子更新 `youtube.txt`。
2. 浏览器同步失败或质量下降：保留已有 `youtube.txt`。
3. 没有浏览器档案：继续使用管理端上传/目录 drop-in 的 Netscape 文件。
4. 两者都没有：维持匿名请求能力并给出状态元数据。

运行期下载和搜索只读稳定 jar。这样可以避免：

- 浏览器正在运行时 SQLite/文件锁竞争；
- 每次下载都重复解密整个浏览器档案；
- Windows DPAPI 档案被复制到 Linux 容器后不可解密；
- 直接把浏览器数据库暴露给多个并发 yt-dlp 进程。

### 3.2 配置契约（拟定）

```text
# 原样传给 yt-dlp；示例：chrome:Default
COOKIES_FROM_BROWSER=
# 启动后和周期同步浏览器档案到 cookies/youtube.txt
COOKIES_BROWSER_SYNC_INTERVAL_SECONDS=21600
# 默认 true；配置来源后启动阶段先同步一次
COOKIES_BROWSER_SYNC_ON_START=true
```

约束：

- `COOKIES_FROM_BROWSER` 作为单个 argv 参数传递，不经 shell 拼接。
- 未配置时行为与当前版本一致。
- 浏览器档案与服务需处于可解密的同 OS/用户上下文；容器场景应挂载同环境生成的持久化档案。
- `COOKIES_KEEPALIVE` 继续作为稳定 jar 的刷新/回退机制，不取代浏览器档案同步。

最终变量名可在 T2 基于代码一致性微调，但必须同步 `.env.example` 和 README。

### 3.3 同步流程

1. 创建权限收敛的临时 Netscape 文件。
2. 以参数数组调用 yt-dlp：`--cookies-from-browser <spec>` + `--cookies <temp>`，使用公开 YouTube 探测 URL，带超时和可选代理。
3. 检查命令结果、文件格式、YouTube/Google 域 Cookie、登录态评分。
4. 仅当候选不弱于当前稳定 jar 时原子提交；失败时删除临时文件。
5. 记录不含敏感值的同步状态：时间、来源、成功/失败、错误摘要、登录态布尔值。
6. 周期任务串行化，避免与上传、keepalive、下载快照互相覆盖。

## 4. “是否更稳定”的结论标准

浏览器档案方案预期比手工反复导出文件更省维护，前提是档案本身保持登录且可被当前运行环境解密。它主要改善 Cookie 获取与轮换，不承诺绕过账号退出、风控、出口 IP 或上游协议变化。

本 goal 用以下证据判断实现成立：

- 成功路径可从浏览器来源生成有效稳定 jar；
- 失败/弱化路径不会覆盖较强登录 jar；
- 搜索与下载都读取同步后的同一稳定路径；
- 周期并发、文件原子性、超时和敏感信息边界有测试；
- 若本机存在兼容档案，做一次只记录元数据、不保留导出副本的 live 抽样；否则以可重复的 fake-runner 集成测试作为确定性证据，并明确 live 条件。

## 5. 代码落点

| 文件/区域 | 预计改动 |
| --- | --- |
| `internal/download/*` | 先收束当前 MP4 校验差异与回归测试 |
| `internal/config` | 浏览器来源、同步间隔、启动同步配置与校验 |
| `internal/cookies` | 浏览器抽取、原子提交、质量判定、同步状态/锁 |
| `cmd/ytmusic-bridge/main.go` | 启动同步、周期任务、与 keepalive 生命周期整合 |
| `internal/httpapi` | 管理 Cookie 状态增加来源/登录态/最近同步元数据 |
| `.env.example` / `README.md` | 本机与容器配置、档案兼容性、回退策略 |

## 6. 风险与处理

| 风险 | 处理 |
| --- | --- |
| 浏览器 Cookie 加密依赖 OS/用户/keyring | 同环境读取；失败保留文件回退并显示摘要 |
| 浏览器档案被浏览器锁定 | 使用 yt-dlp 自带读取逻辑；同步失败不破坏稳定 jar |
| 候选只剩匿名 visitor Cookie | 沿用/增强质量评分，禁止弱候选覆盖强 jar |
| 多个后台任务同时写 jar | 单进程锁 + 临时文件 + 原子替换 |
| 日志泄露 Cookie | 日志/状态只含元数据与截断错误，不打印参数中的敏感内容 |
| 当前未提交 MP4 改动被混入 | T1 单独审计、测试并形成清晰提交边界 |

## 7. 验证

1. `go test ./internal/download` 收束 MP4 校验基线。
2. config 单测覆盖默认值、合法 browser spec、间隔下限。
3. cookies 单测覆盖抽取 argv、成功提交、失败保留、弱候选拒绝、并发串行。
4. main/httpapi 集成测试覆盖启动/周期同步与状态字段。
5. `go test ./...`、`go vet ./...`、`gofmt -l .`。
6. 检查 git diff 与仓库，确保没有真实 Cookie、浏览器数据库、账号信息或临时导出文件。
7. 条件允许时执行 live profile smoke，并立即清理临时 jar。

## 8. 回滚

- 不配置 `COOKIES_FROM_BROWSER` 即回到现有 Netscape 上传/目录挂载行为。
- 浏览器同步代码可按独立提交回滚，不影响下载 API 契约。
- MP4 校验改动与 Cookie 策略分开提交，便于独立回退。

## 9. 默认假设

1. 用户更需要长期可维护的登录来源，而不是把 Google 账号密码交给服务。
2. 第一版不内置 Chromium、远程桌面或嵌入式 Google 登录页面；使用已有、已登录的浏览器档案。
3. 保留现有上传面板和 `COOKIES_DIR`，实现零配置兼容。
4. 不读取、打印或提交真实 Cookie 内容；live 检查只记录结果元数据。
5. 不抢 Windows 焦点，不启动可见浏览器窗口。
