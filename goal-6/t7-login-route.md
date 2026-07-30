# T7：前端 YouTube 登录路线实证与架构定稿

## 1. 用户澄清与最终选择

用户明确：“已有浏览器档案同步”与“前端直接登录 YouTube”是两条**可选路线**，不是串行步骤。

本任务的选择如下：

- **默认主路线：管理前端直接登录到服务专用的持久化 Chromium profile。**
  - 浏览器由服务启动和停止，profile 路径固定。
  - 交互通过受管理端认证保护的 CDP 图像/输入通道完成。
  - Cookie 由正在运行的浏览器通过 CDP 交给服务，再进入既有稳定 jar 提交流程。
  - 该路线不依赖 `COOKIES_FROM_BROWSER`，也不先读取另一个浏览器档案。
- **独立可选路线：已有外部浏览器档案同步。**
  - 继续使用现有 `COOKIES_FROM_BROWSER` + yt-dlp 同步器。
  - 适合服务和日常浏览器处于同 OS、同用户上下文的本机部署。
- **最终回退：Netscape 文件上传。**

对于“由管理前端登录、需要长期运行”的目标，服务专用 profile 更稳定：它避开日常浏览器数据库锁、profile 路径漂移、桌面用户切换和外部 Cookie 解密上下文不一致；浏览器自己解密并通过 CDP 提供 Cookie，也绕开外部读取 Chromium 数据库时的 DPAPI/keyring/app-bound encryption 问题。

已有外部 profile 在个人桌面上可能因为日常浏览器持续使用而保持更久，但在容器或后台服务中运维条件更脆弱，因此保留为独立高级选项，不作为前端登录的前置步骤。

## 2. 当前环境实证

### 2.1 环境

| 项目 | 当前证据 |
| --- | --- |
| Windows | Windows 10 企业版 `10.0.19045`，x64 |
| 系统浏览器 | Microsoft Edge `150.0.4078.105` |
| 实证浏览器 | Playwright Chromium `148.0.7778.96` |
| 自动化运行时 | Python Playwright `1.60.0` |
| Docker | CLI `29.5.3` 已安装；本轮 Docker daemon 未运行，未启动桌面程序 |
| 当前 Dockerfile | 仅安装 yt-dlp/ffmpeg/deno，尚无 Chromium；T8 实现时需要补齐浏览器运行依赖 |

全部浏览器实证使用 headless 持久上下文，没有复用或读取用户现有 Chrome/Edge profile。

### 2.2 持久 profile

在隔离临时 profile 中写入虚构的本地存储和测试 Cookie，关闭浏览器后用同一路径重新启动：

- `Local State` 和 `Default/Preferences` 正常落盘；
- localStorage 成功保留；
- 持久 Cookie 成功保留；
- 访问 YouTube 后得到的 Google/YouTube Cookie 元数据在再次重启后仍存在（7 个）；
- 实证结束后整个临时 profile 已清理。

这证明服务可以把登录状态保存在专用可挂载目录中，并跨浏览器进程重启复用。

### 2.3 Google 登录页

隔离 Chromium 访问 YouTube 的 Google 登录入口：

- HTTP `200`，最终主机为 `accounts.google.com`；
- 登录页标题存在；
- “邮箱或电话号码”输入框可见、可编辑、可聚焦；
- 页面没有出现“不安全浏览器”提示；
- 未填写、未提交任何账号、密码、2FA 或验证码；
- 响应包含 `X-Frame-Options: DENY`，普通 iframe 路线据此排除。

最终截图在本地忽略目录 `tmp/goal6-t7/evidence/`，未进入 Git：

| 截图 | SHA-256 | 实际读图结论 |
| --- | --- | --- |
| `google-login.png` | `126175E87A52047412EECB385B17CC65861AE600B097DF2978FFC3568F70CEAD` | Google 登录页正常渲染，账号输入框为空，无敏感值和明显错误 |
| `cdp-screencast.png` | `A60C0986087BB25EA668A4460F6CF58997293EB1622FE7EAFC67BAF9470E6619` | CDP 画面显示夹具标题、输入框和紫色边框，非空白帧 |

截图已由独立视觉检查实际读取；结论不是根据 DOM、OCR 或文件名推测。

### 2.4 CDP 交互通道

同一浏览器会话使用 Chrome DevTools Protocol 实测：

- `Page.startScreencast` 连续收到 4 帧，末帧为 `1280×800`、18,090 字节 PNG；
- 通过 `Input.dispatchMouseEvent` 聚焦测试输入框；
- 通过 `Input.insertText` 输入虚构字符串，页面值精确匹配；
- 图像帧与输入事件均可在服务进程中转发，不依赖系统桌面或普通 iframe。

### 2.5 登录态提取

访问 YouTube 后通过 CDP `Storage.getCookies` 只统计元数据：

- Google/YouTube 域 Cookie：8 个；
- 认证 Cookie：0 个；
- `logged_in=false`，符合未输入账号的预期；
- 浏览器再次重启后仍保留 7 个相关 Cookie；
- Cookie 值没有写入结果、日志或 Git。

该结果同时证明：登录成功判定可以直接复用当前的 Cookie 名称/质量规则，而无需从磁盘解密浏览器数据库。

机器可读的非敏感结果保存在 `goal-6/t7-probe-result.json`。

## 3. 路线比较

| 维度 | 外部浏览器 profile | 管理前端直接登录的服务 profile |
| --- | --- | --- |
| 前端直接操作 | 不提供；需先在别处登录 | 提供，用户直接在管理前端操作 |
| 数据库锁 | 日常浏览器运行时可能冲突 | 浏览器生命周期由服务串行控制 |
| Cookie 解密 | 依赖同 OS/用户/keyring，可能受 app-bound encryption 影响 | 浏览器通过 CDP 返回已解密 Cookie |
| profile 路径 | 用户和浏览器升级可能改变 | 配置为固定持久目录 |
| Windows | 本机同用户场景可用 | 本轮已实证 Chromium/CDP 路线 |
| Docker | 挂载桌面 profile 很脆弱 | 安装 Chromium + 持久卷即可复用 |
| 日常刷新 | 日常浏览器会自然刷新 | 服务周期启动浏览器访问 YouTube 后导出 |
| 结论 | 独立可选来源 | 默认主路线 |

## 4. T8 实现架构

### 4.1 组件

1. `ManagedLoginManager`
   - 管理浏览器进程、持久 profile、单一活动登录会话、TTL、退出与崩溃清理。
2. `BrowserController`
   - 启动 Chromium、建立 CDP、打开 Google/YouTube 页面、转发 screencast 和输入事件。
3. `ManagedCookieExporter`
   - 调用 CDP `Storage.getCookies`，过滤 YouTube/Google 域并生成权限为 `0600` 的临时 Netscape jar。
   - 使用现有 `CommitSnapshotIfBetterDetailed` 和同一 Cookie 生命周期锁完成质量保护提交。
4. 管理 API / WebSocket
   - 只允许已认证管理员创建、查看、验证、终止登录会话。
5. 来源仲裁器
   - 保证 managed/external/file 同一时刻只有一个主动刷新来源，避免两个账号的同分 jar 来回覆盖。

### 4.2 来源模式

T8 固化以下配置：

```text
COOKIE_SOURCE_MODE=auto                 # auto | managed | external | file
YOUTUBE_LOGIN_BROWSER_PATH=             # 空值自动探测 Chromium/Edge
YOUTUBE_LOGIN_PROFILE_DIR=browser-profile
YOUTUBE_LOGIN_HEADLESS=true
YOUTUBE_LOGIN_SESSION_TTL_SECONDS=900
YOUTUBE_LOGIN_REFRESH_INTERVAL_SECONDS=21600
```

`auto` 的仲裁规则：

1. 服务专用 profile 已验证为登录态时选择 `managed`；
2. 否则，配置了 `COOKIES_FROM_BROWSER` 时选择 `external`；
3. 否则只使用现有稳定文件/上传回退。

这是来源选择，不是串行执行。选中 `managed` 后暂停 external 周期同步；显式 `external` 模式则不启动 managed 自动刷新。所有模式仍可保留稳定文件作为失败回退。

### 4.3 会话状态机

```text
idle -> starting -> interactive -> verifying -> authenticated -> synced -> closed
                    |              |                |
                    +-> expired    +-> not_logged_in +-> sync_failed
```

- 默认会话 TTL 15 分钟；输入活动可延长空闲时间，但硬上限不超过 30 分钟。
- 每个 managed profile 同时只允许一个活动会话和一个控制客户端。
- 浏览器崩溃、管理员终止、通道断开超时或进程退出都会关闭浏览器并保留已落盘 profile。
- `authenticated` 必须由 Cookie 质量规则确认；只看到页面跳转不算成功。

### 4.4 API 契约

```text
POST   /api/admin/youtube-login/sessions
GET    /api/admin/youtube-login/sessions/{id}
DELETE /api/admin/youtube-login/sessions/{id}
POST   /api/admin/youtube-login/sessions/{id}/verify
WS     /api/admin/youtube-login/sessions/{id}/channel
POST   /api/admin/youtube-login/disconnect
```

创建响应只包含会话 ID、状态、过期时间、viewport 和通道路径。API 不返回 profile 路径、Cookie 值、浏览器命令行、账号输入或原始 CDP/浏览器错误。

WebSocket 消息：

- 服务到前端：二进制图像帧、viewport、状态、非敏感错误枚举；
- 前端到服务：归一化指针、键盘、文本、resize、verify、terminate；
- 后端只保留最新待发帧，丢弃过期帧，防止慢客户端造成内存积压；
- 文本/键盘消息转发后立即释放，不进入访问日志、结构化日志或状态对象。

### 4.5 Cookie 提交与刷新

1. 浏览器内部完成登录；
2. CDP 读取 Cookie 到内存；
3. 只保留 YouTube/Google 域，序列化到唯一临时文件；
4. 复用当前质量规则判定 `logged_in`；
5. 在现有包级写锁下原子提交到 `youtube.txt`；
6. 搜索和下载继续只消费稳定 jar；
7. 周期刷新时短暂启动同一 profile，访问 YouTube、CDP 导出、提交后关闭。

浏览器 profile 是持久登录载体，稳定 jar 是业务消费载体；两者属于同一 managed 路线，不需要外部 profile 同步器参与。

## 5. 安全边界

- 创建/查询/终止/通道升级前均校验管理端会话。
- 登录会话绑定创建它的管理 session token 哈希；不同管理员会话不能接管。
- WebSocket 必须校验精确同源 `Origin`，拒绝跨站 WebSocket；不把能力令牌放在 URL query。
- 当前 `statusWriter` 需在 T8 补齐 WebSocket/Hijacker 兼容测试。
- 公网部署要求 HTTPS/WSS；管理 Cookie 在 HTTPS 下设置 `Secure`，继续使用 `HttpOnly` 与 SameSite。
- 浏览器 profile 目录权限收敛，禁止静态服务、目录列表与下载。
- 禁用密码保存、自动填充和浏览器同步；profile 只持久化登录 Cookie/站点状态。
- 不开启 HAR、请求体、console 全量记录、视频录制或产品截图落盘。
- screencast 帧只在内存中转发；实际登录帧可能显示账号/验证码，因此禁止缓存并设置 `Cache-Control: no-store`。
- 输入消息限制类型、坐标、长度和频率；服务日志只记录 session ID 摘要、状态、时长和错误枚举。
- Cookie 导出临时文件始终 `0600`，成功/失败/取消后清理；API 和日志只返回计数、质量分和登录布尔值。

## 6. Windows 与 Docker 运行形态

### Windows

- 自动探测专用 Chromium，其次 Edge；默认使用 headless 新模式，不启动可见窗口。
- profile 使用服务专用目录，不读取用户默认浏览器目录。
- 浏览器和服务必须由同一用户运行，保证 profile 文件权限一致。

### Docker

- 当前 Dockerfile 需要在 T8 加入 Chromium 与必要字体；浏览器路径固定为 `/usr/bin/chromium`。
- profile 目录使用独立持久卷，例如 `/app/browser-profile`。
- 首选 Chromium headless 新模式；若真实登录在提交账号后出现上游浏览器限制，再启用 Xvfb 下的 headful Chromium，CDP/WebSocket 契约保持不变。
- Docker daemon 本轮处于停止状态，因此 Linux 容器内的真实进程实证留给 T8 构建测试；这不影响已完成的 Windows Chromium/CDP/Google 页面实证。

## 7. 后续硬验收

### T8

- 后端会话 TTL、单会话互斥、所有退出路径、浏览器崩溃、profile 重启复用有测试。
- 未认证、跨 Origin、其他管理员 session、重复控制连接全部拒绝。
- CDP Cookie 导出后能原子更新稳定 jar，失败保留旧 jar。
- 浏览器输入、Cookie、CDP 原始错误不进入日志或响应。

### T9

- 前端提供开始、交互、验证、重试、终止、断开登录入口。
- 桌面/窄屏、加载/错误/成功状态均做真实浏览器截图并实际读图。
- 登录画面不使用普通 iframe，也不持久化帧或输入。

### T10

- 使用隔离账号真实完成账号、密码、2FA/验证码中的实际流程。
- Cookie 质量识别为登录态后，搜索、音频和 MP4 都消费同一稳定 jar。
- 浏览器重启后登录仍在；退出/失效状态准确；文件上传回退仍可用。

## 8. 回滚

- `COOKIE_SOURCE_MODE=external`：只使用现有外部 profile 同步。
- `COOKIE_SOURCE_MODE=file`：只使用稳定 Netscape 文件/管理上传。
- managed 登录组件按独立提交回滚，不改变搜索、下载和现有 Cookie API 契约。
