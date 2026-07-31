# cookies 目录（可挂载）

把浏览器导出的 **Netscape cookies.txt** 丢进这个文件夹即可，**文件名随意**。

服务会自动选用，并稳定保存为 `youtube.txt`，供搜索、下载与保活回写共用。

这是三条 Cookie 来源中的**文件回退路线**；另外两条是：

1. 管理前端 YouTube 登录（服务端专用 `browser-profile`）
2. 外部已登录浏览器 profile（`COOKIES_FROM_BROWSER`）

完整配置见仓库根目录 [`.env.example`](../.env.example) 与 [`README.md`](../README.md)。

## 云容器

挂载**文件夹**到容器内：

```text
容器路径: /app/cookies
```

环境变量：

```text
COOKIES_DIR=/app/cookies
COOKIES_KEEPALIVE=1
# 可选：保活间隔秒数，默认 21600（6 小时）
# COOKIES_KEEPALIVE_INTERVAL_SECONDS=21600
```

若同时使用管理端登录，建议额外持久化：

```text
YOUTUBE_LOGIN_PROFILE_DIR=/app/browser-profile
```

## 注意

- 不要把真实 cookie 提交到 git / 不要推送敏感文件
- 需要整站导出，不要只拷一个 `__Secure-1PAPISID`
- 浏览器同步失败时不会用匿名 visitor cookie 覆盖已有登录 jar
