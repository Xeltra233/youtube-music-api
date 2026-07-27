# cookies 目录（可挂载）

把浏览器导出的 **Netscape cookies.txt** 丢进这个文件夹即可，**文件名随意**。

服务会自动选用，并稳定保存为 `youtube.txt` 供下载与保活回写。

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

## 注意

- 不要把真实 cookie 提交到 git / 不要推送敏感文件
- 需要整站导出，不要只拷一个 `__Secure-1PAPISID`
