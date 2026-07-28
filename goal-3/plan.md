# Plan: 管理员上传前端

## 目标
为 ytmusic-bridge 增加**仅用于上传**的管理前端：
1. 独立登录页（管理员密码由环境变量配置）
2. 登录后的上传页：把 cookie 文件上传到 `COOKIES_DIR`
3. 不暴露真实 cookie 内容到 git / 日志明文

## 现状
- 已有 HTTP API：`/healthz` `/search` `/download` `/file/{token}`
- 已有 `COOKIES_DIR` + 自动发现/保活，但**没有**上传 UI 与管理员会话
- 静态资源与 admin 路由均未实现

## 方案（默认假设）
### 鉴权
- 环境变量：`ADMIN_PASSWORD`（必填才启用管理端；为空则管理路由关闭或拒绝）
- 可选：`ADMIN_SESSION_SECRET`（空则从密码派生/随机启动密钥，重启登录失效可接受）
- 登录成功后发 HttpOnly Session Cookie（`Path=/admin` 或全站 `/`）
- 独立页面：`/admin/login.html`；上传页：`/admin/` 或 `/admin/index.html`
- 所有 `/api/admin/*` 需已登录

### 上传
- `POST /api/admin/cookies/upload`：multipart 文件
- 保存到 `COOKIES_DIR`，保留原名并可触发提升为 `youtube.txt`
- `GET /api/admin/cookies/status`：是否存在文件、大小、修改时间、是否保活开启（**不返回 cookie 内容**）
- 可选删除/替换接口

### 前端
- embed 静态资源：`internal/admin/assets/{login.html,admin.html,admin.css,admin.js}`
- 登录卡片垂直水平居中（`min-height: 100dvh` + flex）
- 上传页只做：选择/拖拽 txt、上传、状态展示、退出
- 视觉简洁清晰；后续若要玻璃质感可再加，本 goal 以可用上传为主

### 配置与文档
- `.env.example` / README / Dockerfile 补充 `ADMIN_PASSWORD`
- 管理端与 bot `API_KEY` 分离

## 验证
- 单元测试：登录失败/成功、未授权上传 401、上传后文件落盘且 status 正确
- 本地启动后截图验证登录页居中与上传页
- git 不包含任何真实 cookie

## 风险
- 弱管理员密码；要求文档强调公网必须设强密码
- Session 固定/CSRF：SameSite=Lax + POST；文件类型限制 .txt
- 与现有 API_KEY 中间件关系：admin 路由走独立鉴权，避免双重拦截

## 回滚
- 去掉 admin embed 路由与 `ADMIN_PASSWORD` 即可恢复纯 API
