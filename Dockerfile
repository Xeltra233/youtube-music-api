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
    FFMPEG_LOCATION=/usr/bin/ffmpeg

EXPOSE 8787

CMD ["/app/ytmusic-bridge"]
