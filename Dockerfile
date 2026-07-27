# 多阶段构建：编译 Go 服务，并在运行镜像内提供 yt-dlp + ffmpeg。
# 入口在 cmd/ytmusic-bridge，不要在仓库根目录直接 go build。

FROM golang:1.26.4-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ytmusic-bridge ./cmd/ytmusic-bridge

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl ffmpeg \
  && rm -rf /var/lib/apt/lists/* \
  && curl -fsSL -o /usr/local/bin/yt-dlp \
       "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux" \
  && chmod +x /usr/local/bin/yt-dlp \
  && yt-dlp --version \
  && ffmpeg -version >/dev/null

# 可选：安装 deno，降低 YouTube 需要 JS runtime 的提取失败率（android_vr 仍是主路径）。
RUN curl -fsSL "https://github.com/denoland/deno/releases/latest/download/deno-x86_64-unknown-linux-gnu.zip" -o /tmp/deno.zip \
  && apt-get update \
  && apt-get install -y --no-install-recommends unzip \
  && unzip -o /tmp/deno.zip -d /usr/local/bin \
  && chmod +x /usr/local/bin/deno \
  && rm -f /tmp/deno.zip \
  && rm -rf /var/lib/apt/lists/* \
  && deno --version \
  && yt-dlp --js-runtimes deno --version >/dev/null || true

WORKDIR /app

COPY --from=builder /out/ytmusic-bridge /app/ytmusic-bridge

RUN mkdir -p /app/downloads /app/bin \
  && ln -sf /usr/local/bin/yt-dlp /app/bin/yt-dlp \
  && ln -sf /usr/bin/ffmpeg /app/bin/ffmpeg \
  && ln -sf /usr/bin/ffprobe /app/bin/ffprobe

# 容器默认监听全部网卡；平台若注入 PORT 会覆盖端口。
ENV HOST=0.0.0.0 \
    PORT=8787 \
    DOWNLOAD_DIR=/app/downloads \
    YTDLP_PATH=/usr/local/bin/yt-dlp \
    FFMPEG_LOCATION=/usr/bin/ffmpeg \
    PATH="/usr/local/bin:${PATH}"

EXPOSE 8787

CMD ["/app/ytmusic-bridge"]
