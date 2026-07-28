# Tasks

- [x] T1 配置项：`ADMIN_PASSWORD` / session 相关 + 测试
  - 完成：`ADMIN_PASSWORD` `ADMIN_SESSION_SECRET` `ADMIN_SESSION_TTL_SECONDS`
  - `AdminEnabled()`；TTL 下限 5m / 上限 7d；默认 12h
  - 单测：`TestAdminConfigFromEnv` `TestAdminSessionTTLFloor`；`go test ./internal/config` 通过
  - `.env.example` 已补充注释
- [x] T2 admin session 登录/登出/check-auth API + 测试
  - `internal/adminauth`：HMAC session cookie
  - API：`POST /api/admin/login` `POST /api/admin/logout` `GET /api/admin/check-auth`
  - admin 路径绕过 bot `API_KEY`
  - 单测：`go test ./internal/adminauth ./internal/httpapi` 通过
- [x] T3 cookies 上传/状态 API（写入 COOKIES_DIR，不回传内容）+ 测试
  - `GET /api/admin/cookies/status` `POST /api/admin/cookies/upload`
  - 写入 `COOKIES_DIR`，提升 `youtube.txt`；响应无 cookie 正文
  - 单测：`TestAdminCookieUploadAndStatus` 等通过
- [ ] T4 静态前端：独立登录页 + 上传页（居中、拖拽上传）
- [ ] T5 路由挂载 embed、与现有 middleware 协调
- [ ] T6 `.env.example` / README / Dockerfile 文档
- [ ] T7 浏览器截图验证 + 全量测试
- [ ] T8 提交推送（无敏感文件）

## 检查点
每完成 3 个 task 做一次构建/测试检查。

## 下一步
T4：静态登录页 + 上传页前端。

## 检查点（T1–T3）
- `go test ./internal/httpapi ./internal/adminauth ./internal/config` 通过
- 管理 API 已具备登录与上传能力，待前端接入
