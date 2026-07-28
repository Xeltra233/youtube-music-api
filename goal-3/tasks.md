# Tasks

- [x] T1 配置项：`ADMIN_PASSWORD` / session 相关 + 测试
  - 完成：`ADMIN_PASSWORD` `ADMIN_SESSION_SECRET` `ADMIN_SESSION_TTL_SECONDS`
  - `AdminEnabled()`；TTL 下限 5m / 上限 7d；默认 12h
  - 单测：`TestAdminConfigFromEnv` `TestAdminSessionTTLFloor`；`go test ./internal/config` 通过
  - `.env.example` 已补充注释
- [ ] T2 admin session 登录/登出/check-auth API + 测试
- [ ] T3 cookies 上传/状态 API（写入 COOKIES_DIR，不回传内容）+ 测试
- [ ] T4 静态前端：独立登录页 + 上传页（居中、拖拽上传）
- [ ] T5 路由挂载 embed、与现有 middleware 协调
- [ ] T6 `.env.example` / README / Dockerfile 文档
- [ ] T7 浏览器截图验证 + 全量测试
- [ ] T8 提交推送（无敏感文件）

## 检查点
每完成 3 个 task 做一次构建/测试检查。

## 下一步
T2：实现 admin session 与登录 API。
