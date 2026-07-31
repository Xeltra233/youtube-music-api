# Tasks

- [x] T1 收束 goal-5 遗留的 MP4 媒体校验改动
  - 目标：审计当前 `internal/download` 未提交差异；修正缓存淘汰、ffprobe 定位、视频流判定和错误行为。
  - 验证：`go test ./internal/download ./internal/httpapi`；检查 diff 不影响音频路径。
  - 完成记录：
    - 新下载 MP4 与三处缓存命中路径都执行严格视频流校验；使用 ffprobe `V:0`，排除封面/attached picture。
    - 音频格式仅做文件存在/非空检查，不启动 ffprobe，音频路径行为保持不变。
    - 无视频流或 ffprobe 明确拒绝文件时标记 `ErrInvalidMedia`；新下载映射为上游失败，token 读取映射为文件不存在。
    - 坏缓存按 token/path 条件淘汰，避免慢校验器误删同 key 下刚写入的新缓存；等待信号量后的缓存也会重新校验。
    - ffprobe 从配置目录、显式 ffprobe 或 ffmpeg 同目录解析；不再把自定义命名的 ffmpeg 可执行文件误当 ffprobe。
  - 验证结果：
    - `go test ./internal/download ./internal/httpapi` 通过。
    - `go test -race ./internal/download ./internal/httpapi` 通过。
    - 新增定向测试连续运行 20 次通过；`go test ./...`、`go vet ./...`、`git diff --check` 通过。
    - 实际 `bin/ffprobe.exe`：现有 1080p MP4 返回 `video,1920,1080`；临时生成的 audio-only MP4 对 `V:0` 返回空，临时文件已清理。
  - 剩余风险/下一步：视频下载现在严格依赖 ffprobe；Docker 的 ffmpeg 包和项目本地 `bin/ffprobe` 已满足。下一轮执行 T2，定义浏览器 Cookie 来源配置契约。

- [x] T2 定义浏览器 Cookie 来源配置契约
  - 目标：加入浏览器 spec、启动同步和周期配置；默认保持旧行为；参数不经 shell。
  - 验证：config 单测覆盖环境变量、默认值、下限与非法输入。
  - 完成记录：
    - 固化 `COOKIES_FROM_BROWSER`、`COOKIES_BROWSER_SYNC_ON_START`、`COOKIES_BROWSER_SYNC_INTERVAL_SECONDS` 三项配置及对应 `Config` 字段。
    - browser/keyring 前缀按 yt-dlp `2026.07.04` 支持列表校验并规范为小写；Windows 路径、带空格 profile 和 Firefox container 保持为一个完整字符串，不做 shell/字段拆分。
    - Chromium keyring 只允许用于 Chromium 系浏览器；控制字符、未知浏览器/keyring、多 keyring 和超长 spec 均 fail fast。
    - 默认未配置来源，旧 Cookie 文件策略不受影响；配置来源后默认启动同步、每 6 小时周期同步。
    - 正数周期下限为 1 分钟，`0` 明确定义为关闭周期同步；增加来源、启动调度、周期调度辅助方法供 T4 使用。
    - `goal-6/plan.md` 已同步最终变量名和 `0` 的语义；`.env.example` / README 留在 T7 集中更新。
  - 验证结果：
    - `go test ./internal/config -count=20` 通过。
    - `go test -race ./internal/config` 通过。
    - `go test ./...`、`go vet ./...`、`gofmt -l internal/config`、`git diff --check` 通过。
  - 剩余风险/下一步：本轮只落定配置契约，尚未调用 yt-dlp；下一轮 T3 实现浏览器档案到稳定 Netscape jar 的同步器，并用 fake runner 证明 spec 始终作为单个 argv 传递。

- [x] T3 实现浏览器档案到稳定 Netscape jar 的同步器
  - 目标：临时文件、yt-dlp 抽取、质量校验、强 jar 保护、原子提交、超时与清理。
  - 验证：fake runner 单测覆盖成功、失败、弱化、代理/argv、临时文件清理。
  - 完成记录：
    - 新增串行化 `BrowserSyncer` 与可注入 runner；以参数数组直接调用 yt-dlp，browser spec、代理和探测 URL 均保持独立 argv，不经过 shell。
    - 同步前在稳定 jar 同目录创建 `0600`、以 `.tmp` 结尾的临时 Netscape 文件；成功、命令失败、弱候选、超时与取消路径都会清理。
    - 候选必须包含有效且未过期的 YouTube/Google Cookie，并满足 `LOGIN_INFO` 或 SID family + APISID family 的登录态判定；支持 `#HttpOnly_` 行，SID 单独不再误判登录，严格拒绝 `evilgoogle.com` 等后缀伪装域。
    - `CommitSnapshotIfBetterDetailed` 保留旧 API 兼容，同时返回非敏感质量元数据：登录 jar 永不被匿名候选覆盖；匿名 jar 可被登录候选升级；登录状态相同则拒绝更低分候选。
    - 稳定文件改用同目录唯一临时文件、`fsync`、替换/Windows 备份回滚的原子写；包级读写锁协调去重、快照、提交及搜索 Header 读取，yt-dlp 快照改为 `.tmp`，崩溃残留不会被 drop-in 去重误提升。
    - yt-dlp 常见失败归类为 profile 不可用、数据库锁定、解密失败、超时/取消或通用抽取失败；错误不回显 browser spec、profile/临时路径、代理凭据、Cookie 正文或原始 stdout/stderr。
  - 验证结果：
    - `go test ./internal/cookies -count=20` 通过；`go test -race ./internal/cookies` 通过。
    - fake runner 覆盖成功提交、Windows/空格 profile 单 argv、代理、稳定 jar 保留、匿名候选拒绝、匿名到登录升级、较强登录 jar 保留、错误脱敏、超时清理和 8 goroutine 串行执行。
    - 解析/文件测试覆盖 `#HttpOnly_`、SID 单独、过期认证 Cookie、域名伪装、原子替换无 `.tmp-*` / `.bak-*` 残留。
  - 剩余风险/下一步：本轮同步器尚未接入进程启动/周期生命周期；真实浏览器档案仍受同 OS/用户/keyring 解密条件影响。下一轮执行 T4，接入启动同步、周期刷新和退出取消。

## 大型检查点 A（T1–T3 后）

- [x] 需求未偏离；MP4 与 Cookie 改动边界清晰。
- [x] `go test ./...`、`go vet ./...`、格式检查通过。
- [x] 没有真实 Cookie/浏览器档案/账号数据进入 diff。
- [x] 结果记录：
  - 边界：T1 的 MP4 视频流校验已独立提交；T2 只固化配置；T3 仅修改 `internal/cookies` 与 goal 记录，未提前接入 T4 生命周期或前端实现。追加的管理前端真实登录要求已保留在 T7–T10。
  - 正确性/并发：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 全部通过；`gofmt -l .` 无输出，`git diff --check` 通过。
  - 数据一致性：稳定 jar 的读取、快照、drop-in 去重和质量保护提交由同一进程锁协调；替换使用完整文件原子写，失败候选不改变现有 jar，临时快照不参与自动提升。
  - 安全/敏感数据：检查 diff、未跟踪文件和浏览器数据库/临时文件特征，未发现真实 Cookie、浏览器 profile、账号、数据库或残留导出文件；测试仅使用明确的虚构值。
  - 文档/回滚：配置契约与后续前端登录目标已在 plan/tasks 同步；部署文档按 T11 集中更新。不配置浏览器来源仍保留旧文件策略，T3 可按独立提交回滚。

- [x] T4 接入启动同步与周期刷新生命周期
  - 目标：启动时先尝试浏览器同步；后台串行刷新；失败继续使用稳定 jar；与 keepalive/退出等待正确协作。
  - 验证：生命周期测试或可控 fake runner；取消、超时、重复 tick 无泄漏。
  - 完成记录：
    - 新增 `CookieLifecycle`：浏览器抽取与稳定 jar keepalive 共用同一操作锁和同步事件循环，外部 yt-dlp 进程不并发启动；浏览器 ticker 的积压会自然合并，keepalive 使用完成后重置的单 timer，避免慢任务形成并发或持续追赶。
    - `main` 在解析稳定路径和 yt-dlp 后立即建立 signal context，并在创建搜索/下载客户端及启动 HTTP listener 前同步执行一次浏览器档案抽取；失败只记录安全摘要，继续消费原有稳定 jar 或保持匿名能力。
    - 周期浏览器同步与 keepalive 合并为一个后台生命周期；浏览器来源为空、周期为 `0`、keepalive 关闭时不创建对应 timer/ticker，默认配置保持旧行为。
    - shutdown signal 与 HTTP server 错误两条退出路径都会取消同一 context，并等待 cleanup 和 Cookie 生命周期 goroutine 结束；正在执行的浏览器抽取/keepalive 会收到取消并清理临时快照。
    - 原有 `RunKeepAliveLoop` 改为复用统一生命周期，保留自定义 URL；`KeepAliveOnce` 不再捕获或回显 yt-dlp 原始输出，只返回超时、取消、可执行文件缺失或通用刷新失败类别。
    - 生命周期日志只记录 phase、是否更新、登录态、Cookie 数和质量分，不记录 browser spec、profile、代理或 Cookie 正文。
  - 验证结果：
    - `go test ./internal/cookies -run '^TestCookieLifecycle' -count=50` 通过；`go test ./internal/cookies -count=20` 通过。
    - `go test -race ./internal/cookies ./cmd/ytmusic-bridge` 通过；`go test ./...` 与 `go test -race ./...` 通过。
    - fake lifecycle 覆盖启动参数/同步顺序、禁用配置、启动失败保留稳定 jar、周期失败后继续、浏览器与 keepalive 不重叠、浏览器/keepalive 取消、timer 重置、关闭 tick channel 和禁用时立即退出。
    - `go vet ./...`、`go build ./...`、`gofmt -l .`、`git diff --check` 通过；临时文件及浏览器数据库特征扫描无残留。
  - 剩余风险/下一步：本轮尚未把最近同步结果和登录质量暴露给管理 API；真实 profile 的系统解密条件仍在 T7/T12 实证。下一轮执行 T5，增加只含元数据的管理状态并验证鉴权与脱敏。

- [x] T5 增加 Cookie 质量与管理状态元数据
  - 目标：状态返回 source、logged_in、最近同步时间/结果/错误摘要，不返回正文或敏感参数。
  - 验证：httpapi 单测检查字段、鉴权与敏感值不出现在响应。
  - 完成记录：
    - `CookieLifecycle` 新增读写锁保护的同步状态：是否配置浏览器来源、进行中、phase、最近结果、是否更新、完成时间和最近成功时间；启动/周期同步开始、成功、保留、失败及取消都会原子更新。
    - 浏览器错误在状态层归一为固定枚举，例如 `profile_database_locked`、`profile_decrypt_failed`、`not_logged_in`、`timeout` 和 `sync_failed`；任意 provider 原始文本会在 HTTP 边界再次收敛，不转发命令输出、profile 路径或 Cookie 值。
    - 稳定 jar 状态在包级读锁下同时读取文件元数据与质量，管理 API 新增 `source`、`browser_configured`、`valid`、`logged_in`、质量分、Cookie 计数、同步进行中、最近结果/错误/时间等字段。
    - `source=browser` 表示配置的主来源是浏览器档案；浏览器失败时 `present/logged_in` 仍反映当前稳定文件回退。未配置浏览器时，有稳定 jar 为 `file`，无文件为 `none`。
    - `httpapi.Options` 接受只读 `CookieSyncStatusProvider`，`main` 注入进程级生命周期；管理鉴权在读取 provider 和文件状态之前完成。
    - 状态响应移除绝对 `cookies_dir`，保留安全的文件名/大小/时间元数据；GET 状态仍会在包级锁下提升新 drop-in，维持运行期文件热更新。上传名为 `youtube.txt` 时先改成唯一 drop-in，再由 cookies 包替换稳定 jar，避免绕过写锁。
    - HTTP 状态路径不再修改共享 `cfg.CookiesFile`，消除管理请求与下载/后台生命周期之间的配置数据竞争。
  - 验证结果：
    - `go test ./internal/cookies ./internal/httpapi -count=20` 通过；状态/脱敏定向测试各连续运行 50 次通过。
    - `go test -race ./internal/cookies ./internal/httpapi ./cmd/ytmusic-bridge` 通过；`go test ./...` 与 `go test -race ./...` 通过。
    - 测试覆盖空状态、文件上传登录态、浏览器来源、成功/失败/进行中/取消、固定错误分类、恶意 provider 文本、未鉴权不调用 provider，以及 browser spec、代理、Cookie 值、原始错误和本机目录均不进入响应。
    - `go vet ./...`、`go build ./...`、`gofmt -l .`、`git diff --check` 通过；仓库未发现浏览器数据库、真实 Cookie 或临时导出残留。
  - 剩余风险/下一步：状态目前描述稳定 jar 与后台同步，不包含后续前端浏览器登录会话本身；该部分由 T8/T9 扩展。下一轮执行 T6，证明同一同步后 jar 被搜索和音频/MP4 下载共同消费，并验证上传回退。

- [x] T6 搜索与下载共用同步后稳定 jar 的集成回归
  - 目标：证明浏览器同步后的 jar 同时被 InnerTube 搜索和 yt-dlp 下载快照消费；上传文件仍可回退。
  - 验证：跨 package 集成测试；音频/mp4 下载与搜索既有测试全绿。
  - 完成记录：
    - 新增 `TestCookiePipelineBrowserSyncAndUploadFallback` 跨包回归，使用真实 `BrowserSyncer`、`ytmusic.Client`、`download.Downloader` 和管理 HTTP 上传处理器串起稳定 jar 全链路；搜索客户端与下载器在稳定文件尚不存在时先构造，证明后续按同一路径动态读取，而非缓存旧 Cookie 正文。
    - fake browser runner 只负责模拟 yt-dlp 抽取并产出已登录 Netscape jar；同步器仍执行真实质量判定、包级锁、原子提升和临时文件清理。InnerTube 测试上游实际捕获请求 `Cookie` header，确认包含本轮同步生成的登录 Cookie。
    - 音频 `mp3` 与视频 `mp4` 均通过真实下载器构造 yt-dlp 参数并消费稳定 jar 的独立快照；测试 runner 模拟 yt-dlp 改写快照，随后验证稳定 `youtube.txt` 字节不变、两份快照均已清理，且 mp4 仍经过视频流探针。
    - 通过管理端登录后上传名为 `youtube.txt` 的新 jar，验证它先作为 drop-in 提升为稳定文件；不重建搜索客户端或下载器，搜索、音频与 mp4 随即共同读取上传后的新一代 Cookie，证明文件回退和运行期热更新仍成立。
    - 管理上传响应不包含测试 Cookie 标记；浏览器抽取与下载快照没有 `.browser-cookies-*` / `.ytdlp-cookies-*` 残留。
  - 验证结果：
    - 新集成测试连续运行 50 次通过；`internal/cookies`、`internal/ytmusic`、`internal/download`、`internal/httpapi` 连续 10 次测试及四包 race 测试通过。
    - `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 全部通过。
    - `gofmt -l .` 无输出，`git diff --check` 通过；未发现浏览器数据库、账号材料或临时导出文件。工作区已有的运行时 `cookies/youtube.txt` 受 `.gitignore` 保护且修改时间早于本轮，本轮未读取正文、未改动、未纳入 diff。
  - 剩余风险/下一步：本轮以可重复的 fake browser/yt-dlp runner 证明稳定 jar 的数据流和隔离边界；真实服务端浏览器、持久化 profile 与交互登录条件由下一轮 T7 实证，最终真实账号链路仍由 T10 验收。

## 大型检查点 B（T4–T6 后）

- [x] 并发、数据一致性、取消与错误路径全面检查。
- [x] `go test ./...`、`go vet ./...`、格式检查通过。
- [x] 管理状态和日志不含 Cookie 正文、浏览器解密材料或账号信息。
- [x] 结果记录：
  - 需求边界：T4–T6 已完成生命周期编排、只读管理状态和稳定 jar 消费回归；未提前实现 T7–T10 的服务端浏览器登录与前端 UI，用户追加的前端真实登录硬验收仍完整保留。
  - 并发/取消：生命周期串行、浏览器与 keepalive 互斥、在途浏览器同步取消、在途 keepalive 取消及快照清理、状态进行中到取消转换等定向测试连续运行 50 次通过；`go test -race ./...` 通过。
  - 数据一致性：浏览器候选以质量保护方式原子写入稳定 jar；InnerTube 每次请求重读稳定路径，下载每次创建隔离快照；模拟 yt-dlp 原地改写后稳定文件未变化，管理上传提升后现有消费者立即切换到新内容。
  - 错误/回退：浏览器失败保留旧 jar、弱候选拒绝、状态错误枚举收敛、上传文件回退以及音频/mp4 两条下载路径均有回归；取消和失败路径没有 Cookie 临时文件残留。
  - 安全边界：鉴权与恶意 provider 脱敏测试、管理状态/集成上传测试连续运行 50 次通过；响应和日志只使用枚举及质量元数据。除 `.gitignore` 已覆盖且早于本轮存在的运行时 `cookies/youtube.txt` 外，文件名扫描未发现浏览器 `Cookies`、`Login Data`、SQLite 数据库、`.uploading` 或 Cookie 临时快照；运行时 jar 正文未读取，diff 仅含明确的虚构测试标记。
  - 全量质量门：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`、`gofmt -l .`、`git diff --check` 全部通过；本检查点没有 UI 改动。回滚仍可通过不配置浏览器来源恢复文件策略，T4–T6 也保持独立提交边界。

- [x] T7 前端 YouTube 登录路线实证与架构定稿
  - 目标：比较“已有外部浏览器档案同步”与“管理前端直接登录到服务专用持久化档案”两条独立路线的耐久性；确认当前 Windows/Docker 环境可用的服务端浏览器、持久化档案和安全交互方式，并证明 Google 登录页可真实操作，而非普通 iframe 设想。
  - 验证：最小实证记录浏览器版本、档案落盘、交互通道与登录态抽取结果；不保留账号输入或 Cookie 正文。
  - 完成记录：
    - 根据用户追加说明，把“已有外部浏览器 profile”与“管理前端直接登录”固化为两条独立来源，而非串行步骤。耐久性比较后选择“服务专用持久化 Chromium profile + CDP Cookie 导出”为前端默认路线；现有 `COOKIES_FROM_BROWSER` 保留为本机高级选项，文件上传继续作为最终回退。
    - Windows 10 x64 上使用隔离的 Playwright Chromium `148.0.7778.96` 做无界面实证；profile 的 `Local State`/Preferences、虚构 localStorage 和测试 Cookie 均跨进程重启保留，实际 YouTube/Google Cookie 元数据在第三次启动仍保留 7 个，随后整个临时 profile 已清理。
    - Google 登录入口返回 `200` 并停留在 `accounts.google.com`；账号输入框可见、可编辑、可聚焦，未出现“不安全浏览器”页面。响应 `X-Frame-Options: DENY`，据此排除普通 iframe。
    - CDP `Page.startScreencast` 连续收到 4 帧；`Input.dispatchMouseEvent` + `Input.insertText` 能驱动页面输入。最终 screencast 末帧为 `1280×800`，证明管理前端可采用同源受保护 WebSocket 转发图像/输入，而不依赖系统桌面。
    - CDP `Storage.getCookies` 在匿名 YouTube 页面得到 8 个相关 Cookie、0 个认证 Cookie、`logged_in=false`，符合未输入账号的预期；只记录计数和布尔值，没有输出 Cookie 值。
    - 新增 `goal-6/t7-login-route.md`，定稿来源仲裁、managed profile 生命周期、API/WebSocket、Cookie 原子提交、Windows/Docker 形态、安全边界和 T8–T10 硬验收；`goal-6/t7-probe-result.json` 保存机器可读的非敏感证据。
  - 验证结果：
    - `t7-probe-result.json` 通过 JSON 解析；实证临时 profile 已删除，Playwright Chromium 进程无残留。
    - `google-login.png` 和修正取帧后的 `cdp-screencast.png` 均由独立视觉检查实际读图：登录输入为空且无敏感信息/错误，CDP 末帧显示完整夹具而非空白；截图仅位于被忽略的 `tmp/`，Git 中只记录 SHA-256。
    - `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 全部通过；`gofmt -l .` 无输出，`git diff --check` 通过。
    - 系统 Edge `150.0.4078.105` 与 Docker CLI `29.5.3` 已确认；当前 Docker daemon 未运行，Dockerfile 静态审计确认尚缺 Chromium，所需浏览器/持久卷约束已在架构文档固定。
  - 剩余风险/下一步：本轮没有输入真实账号、密码或 2FA，因此只证明登录页面和交互/持久化基础成立；真实认证成功仍由 T10 硬验收。T8 下一轮按已定稿契约实现管理鉴权后的浏览器会话、CDP 通道、profile 持久化、Cookie 导出和来源仲裁，并补齐容器 Chromium 运行依赖及构建测试。

- [x] T8 实现服务端浏览器登录会话与持久化档案
  - 目标：管理鉴权后创建/终止隔离登录会话，使用专用持久化 profile，提供受保护交互通道并在登录后触发 Cookie 同步。
  - 验证：会话 TTL、并发、终止、重启档案复用、未鉴权访问、敏感输入不落日志均有测试。
  - 完成记录：
    - 新增 `internal/managedlogin`：服务专用持久化 Chromium profile、自动浏览器探测、loopback 临时 CDP 端口、登录页导航、最新帧 screencast、鼠标/键盘/文本/resize、Cookie 导出、周期刷新、断开登录与进程清理；profile 根目录收敛为 `0700`，Preferences 原子写入 `0600`，并禁用密码保存、自动填充和浏览器同步。
    - 登录会话实行单实例/单控制连接、默认 15 分钟空闲 TTL、活动延长和 30 分钟硬上限；会话绑定创建它的管理 session 指纹，浏览器启动失败、崩溃、终止、过期和服务退出均保留 profile 并完成清理。浏览器上下文取消会先经 CDP 优雅关闭，避免硬杀导致登录档案来不及落盘。
    - 新增受管理鉴权保护的创建、查询、终止、校验、WebSocket 控制通道和断开登录 API；WebSocket 精确校验同源、关闭压缩、限制消息大小/类型/坐标/频率，二进制帧及敏感输入只在内存转发。访问日志隐藏完整登录 session ID，API/日志只返回固定错误枚举和质量元数据。
    - CDP `Storage.getCookies` 只保留 YouTube/Google 域，写入唯一 `0600` 临时 Netscape 文件后复用质量保护和原子提交；匿名/弱候选保留已有强 jar。managed、external profile 和文件上传由来源仲裁器作为独立可选路线选择，`auto` 下已验证 managed 优先，managed 生效时暂停 external，上传文件保留为断开后的 drop-in 回退。
    - 主程序接入 managed 启动探测、周期刷新、外部来源仲裁和 shutdown 等待；新增 `COOKIE_SOURCE_MODE` 及 managed 浏览器/profile/TTL/刷新配置。Dockerfile 安装 Chromium 与中日韩字体，使用非 root `app` 用户并预建可写 profile；浏览器档案和 cookies 同时加入 Git/Docker 构建上下文排除规则。
  - 验证结果：
    - `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff` 全部通过；managed/httpapi/cookies 定向测试连续 20 次通过。
    - Windows 原生构建与 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` 通过；`npx.cmd --yes dockerfilelint Dockerfile` 无问题，`gofmt -l .` 与 `git diff --check` 无输出。
    - 本机真实 `YTM_LIVE_CDP=1` 探测通过：自动发现 Chromium 系浏览器、收到非空 screencast、完成输入与 Cookie 元数据调用、通过上下文取消优雅退出、虚构持久 Cookie 跨同一 profile 二次启动恢复；结束后无匹配浏览器进程、profile、Cookie 临时文件或 `DevToolsActivePort` 残留。
    - Docker daemon 当前未运行，Linux 容器镜像的实际构建条件留待 daemon 可用时复验；本轮已完成 Dockerfile 静态检查、Linux 交叉构建和运行时依赖审计。
  - 剩余风险/下一步：真实账号、密码、2FA/验证码及“登录后搜索/音频/MP4”链路留在 T10；下一轮只执行 T9，先读取前端视觉验证指南，再实现管理前端登录 UI、浏览器交互与截图读图验证。

- [x] T9 实现管理前端 YouTube 登录 UI
  - 目标：在管理前端加入开始登录、交互视图、状态、重试、终止/退出入口；登录成功后展示不含敏感值的 Cookie 状态。
  - 验证：按前端视觉验证指南完成浏览器交互测试、截图并实际读图；桌面/窄屏、加载/错误/成功状态正常。
  - 完成记录：
    - 管理首页新增服务浏览器登录入口、来源仲裁说明、档案/认证 Cookie/质量摘要，以及开始、恢复、重连、校验、结束会话和断开已保存登录操作；外部浏览器档案与服务浏览器明确展示为两条独立路线，文件上传继续保留，不形成串行前置条件。
    - 受管理鉴权保护的 WebSocket 通道可显示服务浏览器二进制画面，并转发鼠标、滚轮、键盘和 viewport resize；另提供默认遮蔽的密码/验证码文字发送框，发送后立即清空并恢复 password 类型，脚本不记录输入或画面数据。
    - 活跃会话 ID 只保存在 sessionStorage 并支持刷新恢复；管理端退出前会结束活跃浏览器会话。加载、交互、校验、未登录、同步失败、浏览器启动失败、服务重启后会话丢失、会话过期/结束和成功终态均有明确 UI；轮询得到 404/410 时会恢复“开始登录”，固定错误码映射为用户文案，不显示原始浏览器错误。
    - 登录成功后关闭控制通道、清除最后一帧、压缩终态画面并隐藏敏感文字/无效操作区，只展示 Cookie 数和可继续搜索/下载的结果；断开登录有二次确认。登录页、桌面管理页与 390px 窄屏采用响应式布局和高对比主按钮。
    - 静态资源回归测试覆盖登录面板、交互画面、WebSocket、浏览器启动错误映射、终态样式、文件上传保留及脚本无 console 输入日志，并阻止内部 task 标签进入产品文案。
  - 验证结果：
    - `tmp/goal6-t9/visual_check.py` 在真实无头 Chromium 中完成管理登录、创建真实服务浏览器会话、接收 Google 登录页、鼠标点击与 Tab 键转发、匿名校验错误、结束会话，以及 mocked 成功/浏览器启动失败/服务重启后会话丢失状态；结果为远程帧 `1076×673`、窄屏 `scrollWidth=390`，除夹具预期的 404 网络提示外没有 pageerror 或 console error。
    - 已实际查看 `admin-login-after.png`、`admin-desktop-after.png`、`admin-narrow-after.png`、`login-loading.png`、`login-interactive.png`、`login-error.png`、`login-start-error.png`、`login-session-missing.png` 和 `login-success.png`：登录页居中清楚；桌面层级与主按钮对比正常；窄屏按钮换行且无横向溢出；Google 登录输入框为空且已聚焦；加载/错误信息明确；会话丢失后主按钮恢复可用；成功终态紧凑且没有远程画面或文字发送区残留。
    - `node --check internal/admin/assets/admin.js`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...` 全部通过；`gofmt -l .` 无输出，`git diff --check` 通过。
    - 验证服务和仅使用 `tmp/goal6-t9/runtime/browser-profile` 的浏览器进程均已停止；临时 profile、测试 Cookie jar、SQLite/`DevToolsActivePort` 已清理，仓库跟踪文件未发现浏览器数据库、Cookie jar 或账号材料。
  - 剩余风险/下一步：本轮真实页面保持匿名，没有输入账号、密码、2FA 或验证码，也没有保存登录后画面；真实账号完成登录后对搜索、音频和 MP4 下载的共同消费、退出/失效及上传回退由下一轮 T10 验收。Google 页面结构变化仍可能影响人工定位，但画面/鼠标/键盘/文字转发不依赖其 DOM。

## 大型检查点 C（T7–T9 后）

- [x] 前端登录是实际可操作流程，管理鉴权、会话隔离和敏感数据边界正确。
- [x] 服务端浏览器 profile 可持久化，登录成功可同步为稳定 jar。
- [x] UI 截图已实际读图验证，桌面/窄屏与异常状态无明显问题。
- [x] `go test ./...`、构建、前端交互测试与安全审计通过。
- [x] 结果记录：
  - 需求边界：T7–T9 已完成两条可选浏览器来源的路线定稿、服务端 managed profile/会话编排和管理前端真实交互 UI；外部 profile、managed profile 与文件上传仍是独立来源。本检查点没有提前执行 T10 的真实账号搜索/下载验收。
  - 可操作性：真实 headless 服务浏览器打开 `accounts.google.com`，前端收到非空帧并成功转发点击与键盘；匿名校验、会话终止、刷新恢复、服务重启后会话丢失、WebSocket 重连入口、固定启动失败和成功终态均有浏览器验证或确定性状态夹具。
  - 持久化/数据一致性：T7 已证明同一 managed profile 跨进程保留，T8 已测试 CDP Cookie 导出、质量保护和稳定 jar 原子提交；T9 只消费对应管理契约，没有把外部 profile 设为前置步骤，也没有让业务请求直接读取浏览器数据库。
  - UI/UX：登录页、1280px 桌面、390px 窄屏及加载/交互/未登录/启动失败/成功状态均生成截图并实际读图；无横向溢出，成功终态不保留敏感输入控件或最后浏览器帧，内部任务文字已从产品 UI 移除。
  - 安全/清理：管理鉴权、单控制连接、同源 WebSocket、输入限流/内存转发和日志脱敏由 T8 测试覆盖；T9 脚本不使用 console 输出输入或帧。真实画面保持匿名，验证结束后专用浏览器进程、profile、测试 Cookie 和 DevTools/SQLite 残留计数均为 0。
  - 全量质量门：`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`node --check`、`gofmt -l .`、`git diff --check` 全部通过；T7–T9 保持独立提交边界，managed 路线仍可通过来源模式配置回滚到 external/file。

- [x] T10 前端登录到搜索/下载的端到端验证
  - 目标：证明管理前端登录后，稳定 jar 被搜索、音频下载和 MP4 下载共同使用；退出/失效后状态准确且文件上传回退仍可用。
  - 验证：真实或隔离账号流程 + 自动化回归，仅记录元数据；清理临时导出。
  - 完成记录：
    - 新增 `TestManagedFrontendLoginCookiePipelineEndToEnd` 隔离账号端到端回归，通过真实管理登录 HTTP、登录会话 API 和前端同款 WebSocket 控制协议驱动 `managedlogin.Manager`；浏览器夹具先发送非空远程画面，只有依次接收虚构账号、密码和 OTP 输入后才导出登录 Cookie，随后由正式质量判定与原子提交路径写入稳定 jar。
    - WebSocket 返回 `synced` 前验证登录态、更新结果和认证 Cookie 计数；随后直接检查稳定 jar 已是本次前端登录生成的版本，再用同一持久化 profile 路径执行无 screencast 后台刷新，证明前端会话提交并不依赖后续刷新补写。
    - 通过正式 `/search`、`/download?mode=json` 和 `/file/{token}` HTTP 链路验证搜索、MP3 与 MP4 共同消费同一稳定来源：InnerTube 夹具实际捕获搜索 Cookie header，下载 runner 实际读取每次隔离快照，MP4 继续经过视频流探针；稳定 jar 未被消费者改写，快照均在请求后清理。
    - managed 来源有效时上传新的 Netscape jar 不会覆盖当前强登录 jar；调用前端“断开已保存登录”后，浏览器 Cookie 清理一次、已上传文件被提升，管理状态切换为 `file` 且仍为已登录，现有搜索/下载对象无需重建即可共同消费新一代 Cookie。
    - 断开后再创建匿名 managed 会话并校验，API 返回固定 `not_logged_in` 状态，`managed_authenticated=false`，已生效的文件回退及其稳定 jar 字节保持不变；由此覆盖退出和失效两条状态路径。
    - 所有 API/WebSocket 响应与 managed 日志均检查不含虚构账号输入、管理口令或两代 Cookie 值；测试使用 `t.TempDir`，并检查 managed/browser/yt-dlp 临时 Cookie、原子替换备份和上传临时文件均无残留。
  - 验证结果：
    - 端到端测试连续运行 20 次通过；`go test -count=1 ./...` 与 `go test -race -count=1 ./...` 通过。
    - `go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`node --check internal/admin/assets/admin.js` 全部通过；`gofmt -l .` 与 `git diff --check` 无输出。
    - 仓库文件名审计未发现被跟踪的浏览器数据库、Cookie 数据库、`DevToolsActivePort` 或临时导出；工作区没有本轮 managed pipeline 残留，既有运行时 `cookies/youtube.txt` 继续受 `.gitignore` 保护且本轮未读取正文。
  - 剩余风险/下一步：本轮采用可重复的隔离账号浏览器夹具，没有输入外部真实账号、密码、2FA 或验证码；T7 已独立证明真实 Google 登录页和 CDP 交互通道可用，上游账号风控及页面变化仍属于运行环境变量。下一轮只执行 T11，同步部署与运维文档。

- [x] T11 更新部署与运维文档
  - 目标：更新 `.env.example`、README；覆盖前端登录、浏览器/profile 挂载、Windows/Linux 容器、同 OS/用户解密限制和文件回退。
  - 验证：文档变量、路径、前端操作和实际实现一致。
  - 完成记录：
    - 更新 `.env.example`：补充 `COOKIE_SOURCE_MODE`、`COOKIES_FROM_BROWSER`、浏览器同步间隔、`YOUTUBE_LOGIN_*` 全套变量注释，并明确三条来源是独立可选路线。
    - 更新 `README.md`：配置表补齐 Cookie/管理端变量，修正 `MAX_FILESIZE_MB` 默认为 500；重写容器挂载（含 `/app/browser-profile`）、Cookie 来源策略与管理前端操作步骤/安全边界。
    - 更新 `cookies/README.md`：指向完整策略，并说明文件路线与 managed profile 持久化关系。
  - 验证结果：
    - 对照 `internal/config/config.go` 全量配置键扫描：`.env.example` 与 `README.md` 均无缺失。
    - 文档中的默认值/模式语义与实现一致：`auto/managed/external/file`、外部同步 60 秒下限、登录会话 60~1800 秒、Dockerfile Chromium/profile 路径。
    - 提交：`552449b docs(ops): document cookie sources and managed login`。
  - 剩余风险/下一步：文档已同步实现，但最终 live 抽检与敏感数据收尾仍由 T12 执行；下一轮只执行 T12。
- [x] T12 最终 review、live 抽样、敏感数据审计与提交收尾
  - 目标：逐项核对 objective、实现、测试、视觉、文档、回滚；完成 live 抽样并按边界提交代码。
  - 验证：`git status` / `git diff` / `git log`、全量测试/构建、仓库敏感文件扫描、临时文件清理。
  - 完成记录：
    - 逐项核对 goal-6 objective：MP4 视频流校验、浏览器档案 Cookie 来源、管理前端 YouTube 登录、稳定 jar 共享、失败不破坏已有登录、文档与敏感边界均已落地。
    - 代码边界审查：`origin/master..HEAD` 共 11 个提交，覆盖 download 校验、config、browser sync、lifecycle、status、managed login、admin UI、e2e 与 ops 文档；工作区仅剩本文件收尾记录。
    - 全量质量门：`gofmt -l .` 无输出，`go vet ./...`、`go build ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`、`go mod verify`、`node --check internal/admin/assets/admin.js`、`git diff --check` 全部通过。
    - live 抽样：`YTM_LIVE_CDP=1 go test ./internal/managedlogin -run TestLiveCDPManagedBrowser` 通过；端到端登录链路由 `TestManagedFrontendLoginCookiePipelineEndToEnd` 覆盖（含搜索/音频/MP4 共用稳定 jar、断开与文件回退）。
    - 敏感数据审计：git 跟踪文件无 cookies/profile/DevTools/SQLite 实体；`cookies/youtube.txt` 与 `browser-profile/` 被 ignore；临时 `.browser-cookies-*` / `.ytdlp-cookies-*` / `DevToolsActivePort` 无残留；goal 工件只含元数据。
    - 回滚边界保持清晰：未配置浏览器来源时仍走旧文件策略；MP4 校验、cookies 同步、managed login 按独立提交可回退。
  - 验证结果：
    - 检查点 D 六项全部满足（见下方记录）。
    - 无需新增代码提交；T11 文档提交 `552449b`，T10 及之前实现提交均已在 `master` 本地历史中。
  - 剩余风险/下一步：真实外部 Google 账号风控与页面结构变化属于运行环境变量；仓库内已有可重复隔离端到端与 live CDP 探针。goal-6 可关闭。
## 最终大型检查点 D（T10–T12 后）

- [x] 浏览器档案方案在配置后可用，未配置时完全兼容旧策略。
- [x] 弱/失败同步不会破坏已有登录 jar。
- [x] 搜索和下载共享同一稳定 Cookie 来源。
- [x] MP4 视频流校验与坏缓存淘汰已验证。
- [x] 管理前端可完成真实 YouTube 登录，并通过搜索/音频/MP4 链路验证。
- [x] 文档、视觉验证、测试、提交与敏感数据边界完整。

### 检查点 D 证据

- 兼容旧策略：默认 `COOKIE_SOURCE_MODE=auto`，未配置 `COOKIES_FROM_BROWSER` 时 external 同步不启动；文件上传/目录 drop-in 仍可用。
- 弱同步保护：`TestBrowserSyncRejectsAnonymousCandidate` / `TestBrowserSyncKeepsStrongerLoggedInStable` 与 managed 上传保护路径证明弱/匿名候选不会覆盖强登录 jar。
- 共享稳定 jar：`TestCookiePipelineBrowserSyncAndUploadFallback` 与 `TestManagedFrontendLoginCookiePipelineEndToEnd` 证明搜索、MP3、MP4 读取同一稳定文件。
- MP4 校验：`internal/download` 使用 ffprobe `V:0`；坏缓存淘汰与 audio-only 拒绝已有单测与 race 全绿。
- 管理前端登录：T9 视觉读图 + T10 隔离端到端 + T12 live CDP 探针均通过；不依赖外部真实账号密码入库。
- 文档与安全边界：`.env.example` / README / cookies README 已同步；敏感路径 ignore；无真实 Cookie 正文进入 git。
