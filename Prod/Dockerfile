# Single-container image for the Python fleet API (Prod/). No MediaMTX, no
# ffmpeg, no supervisord — unlike prototype/Dockerfile, Prod serves only
# vendors that hand back a direct playable URL (Chemito, Sumith), so there
# is nothing to remux and nothing else to run in this container.
#
# Build context = the repository root, to match prototype/Dockerfile's
# convention (kept even though Prod doesn't reach outside its own folder):
#
#   docker build -t bms-dev-py:latest -f Prod/Dockerfile .

FROM python:3.11-alpine
# Numeric UID/GID (10001) so Kubernetes runAsNonRoot can verify the user is
# non-root — a named user (USER app) fails that check. Same UID as
# prototype's image so both can share the same securityContext block.
RUN apk add --no-cache ca-certificates wget \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /config && chown 10001:10001 /config

WORKDIR /app
COPY Prod/*.py ./
COPY Prod/vendorclients/ ./vendorclients/
COPY Prod/vendors/ ./vendors/
COPY Prod/services/ ./services/

USER 10001
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["python", "main.py"]
