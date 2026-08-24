# Combined single-container image: MediaMTX media server + the media-MTX
# raw-packet service + the Go fleet API, all supervised by supervisord.
#
# Build context = the repository root, because the media-MTX service lives
# outside prototype/ (it is its own Go module):
#
#   docker build -t bms-dev:latest -f prototype/Dockerfile .

# ---- build the Go fleet API (prototype/backend) ----
FROM golang:1.22-alpine AS build-api
WORKDIR /src
COPY prototype/backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mediamtx-console .

# ---- build the media-MTX raw-packet service (its own module) ----
FROM golang:1.22-alpine AS build-media
WORKDIR /src
COPY media-MTX/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/media-mtxd ./cmd/media-mtxd

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
COPY --from=build-api /out/mediamtx-console /usr/local/bin/mediamtx-console
COPY --from=build-media /out/media-mtxd /usr/local/bin/media-mtxd
COPY media-MTX/mediamtx.yml /mediamtx.yml
COPY media-MTX/supervisord.conf /etc/supervisord.conf
# Alternative process set: fleet API only, no MediaMTX/media-mtxd. Selected
# by docker-compose.no-mediamtx.yml, which overrides the entrypoint command.
COPY media-MTX/supervisord.no-mediamtx.conf /etc/supervisord.no-mediamtx.conf
USER 10001
# 8080 API | 1935 RTMP | 8554 RTSP | 8888 HLS | 8889 WebRTC sig | 9997 mtx API | 9996 playback | 30189 WebRTC media
# 8090 is media-mtxd, deliberately NOT exposed: only the backend talks to it,
# over localhost inside this container.
EXPOSE 8080 1935 8554 8888 8889 9997 9996 30189/udp
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["supervisord", "-c", "/etc/supervisord.conf"]
