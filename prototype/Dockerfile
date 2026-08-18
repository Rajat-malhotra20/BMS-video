# Combined single-container image: MediaMTX media server + Go JSON API,
# both supervised by supervisord. Build context = prototype/.
#
#   docker build -t bms-dev:latest -f Dockerfile .

# ---- build Go backend ----
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mediamtx-console .

# ---- pull the MediaMTX binary from its official image ----
FROM bluenviron/mediamtx:1.19.2 AS mtx

# ---- final runtime image ----
FROM alpine:3.20
# Numeric UID/GID (10001) so Kubernetes runAsNonRoot can verify the user is
# non-root — a named user (USER app) fails that check.
RUN apk add --no-cache ca-certificates supervisor wget ffmpeg \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /recordings /config && chown 10001:10001 /recordings /config
COPY --from=mtx /mediamtx /usr/local/bin/mediamtx
COPY --from=build /out/mediamtx-console /usr/local/bin/mediamtx-console
COPY mediamtx_conf/mediamtx.yml /mediamtx.yml
COPY supervisord.conf /etc/supervisord.conf
USER 10001
# 8080 API | 1935 RTMP | 8554 RTSP | 8888 HLS | 8889 WebRTC sig | 9997 mtx API | 9996 playback | 30189 WebRTC media
EXPOSE 8080 1935 8554 8888 8889 9997 9996 30189/udp
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["supervisord", "-c", "/etc/supervisord.conf"]
