# Remote Bus Streaming Setup

This app should run on a public server when buses are on different internet connections.

## Why Same WiFi Is Not Enough

The local setup works only when the camera publisher and dashboard can reach the same machine directly. Real buses usually use mobile networks, and those networks are commonly behind NAT or CGNAT. That means your server cannot reliably connect inward to the bus.

The reliable pattern is:

```text
Bus camera / bus gateway
  pushes video outward over the internet
        |
        v
Public VPS / cloud server
  MediaMTX + Go backend + frontend
        |
        v
Dashboard users
  open the web app
```

## Current App Structure

The current project has:

```text
prototype/
  docker-compose.yml
  backend/
    main.go
    Dockerfile
    go.mod
  frontend/
    index.html
  mediamtx_conf/
    mediamtx.yml
```

The Go backend serves the frontend and proxies MediaMTX:

```text
/live/     -> MediaMTX HLS      http://mediamtx:8888
/whep/     -> MediaMTX WebRTC   http://mediamtx:8889
/mtx-api/  -> MediaMTX API      http://mediamtx:9997/v3
/health    -> Go backend health
```

Inside Docker Compose, `backend` connects to `mediamtx` by service name:

```yaml
MEDIAMTX_HLS_URL: http://mediamtx:8888
MEDIAMTX_WEBRTC_URL: http://mediamtx:8889
MEDIAMTX_API_URL: http://mediamtx:9997/v3
```

## How It Works For Remote Buses

Deploy the full Docker stack on a public VPS or cloud VM.

Example public domain:

```text
stream.example.com
```

Each bus publishes to the public server:

```text
rtmp://stream.example.com:11935/live/bus_1
rtmp://stream.example.com:11935/live/bus_2
rtmp://stream.example.com:11935/live/bus_3
```

The dashboard opens from:

```text
http://stream.example.com:4000
```

For production, put the Go backend behind HTTPS:

```text
https://stream.example.com
```

This project includes an optional Caddy HTTPS overlay for that.

## Ports

The current Compose file exposes:

| Port | Protocol | Purpose |
|---:|---|---|
| `80` | TCP | HTTP, used by Caddy for Let's Encrypt |
| `443` | TCP | HTTPS dashboard, production |
| `4000` | TCP | Go backend + frontend |
| `11935` | TCP | RTMP ingest from buses |
| `18554` | TCP | RTSP, optional |
| `18888` | TCP | MediaMTX HLS, optional direct access |
| `18889` | TCP | MediaMTX WebRTC WHIP/WHEP, optional direct access |
| `19997` | TCP | MediaMTX API, optional direct access |
| `18189` | UDP | WebRTC media |

For production, avoid exposing `9997` publicly unless it is protected. The frontend only needs it through the Go backend proxy.

## HTTPS With A Domain

Use HTTPS when this runs on a public server. The included `docker-compose.https.yml` file adds Caddy in front of the Go backend and gets a real TLS certificate automatically.

Requirements:

1. A domain, for example `stream.example.com`.
2. DNS `A` record pointing that domain to the server public IP.
3. Server firewall allows inbound TCP `80` and `443`.

Start with HTTPS:

```bash
DOMAIN=stream.example.com docker compose \
  -f prototype/docker-compose.yml \
  -f prototype/docker-compose.https.yml \
  up -d --build
```

Open:

```text
https://stream.example.com
```

Caddy will automatically request and renew the certificate.

For local LAN testing, keep using:

```text
http://192.168.1.6:4000
```

Local HTTPS on a phone needs a trusted certificate installed on the phone, so it is usually not worth using for quick LAN tests.

## Running Locally

From the repository root:

```powershell
docker compose -f prototype\docker-compose.yml up -d --build
```

Open:

```text
http://localhost:4000
```

Publish a test stream with a camera app such as Larix:

```text
rtmp://<server-ip>:11935/live/bus_1
```

For local testing on the same machine:

```text
rtmp://localhost:11935/live/bus_1
```

Then play path:

```text
live/bus_1
```

## Recommended Protocols

Use buses as publishers:

| Protocol | Use Case |
|---|---|
| RTMP | Easiest to test; supported by many camera apps |
| SRT | Better for unstable mobile networks |
| WHIP/WebRTC | Lower latency, more sensitive to network/firewall setup |
| HLS | Best for browser playback, not publishing |

Recommended production flow:

```text
Bus publishes RTMP or SRT -> MediaMTX -> Dashboard plays HLS or WebRTC
```

## Security Notes

The current `mediamtx.yml` is development-friendly and allows anonymous publish/read.

Before exposing this server on the internet:

1. Add authentication for publishing.
2. Restrict or hide the MediaMTX API port.
3. Use HTTPS for dashboard access.
4. Use stable stream names such as `live/bus_001`.
5. Keep firewall rules tight.

Without authentication, anyone who can reach the server could publish or view streams.

## Deployment Checklist

1. Create a public VPS or cloud VM.
2. Install Docker and Docker Compose.
3. Copy this project to the server.
4. Point DNS to the server IP.
5. Open required firewall ports.
6. Run:

   ```bash
   DOMAIN=stream.example.com docker compose \
     -f prototype/docker-compose.yml \
     -f prototype/docker-compose.https.yml \
     up -d --build
   ```

7. Configure bus camera apps to publish to:

   ```text
   rtmp://stream.example.com:11935/live/<bus_id>
   ```

8. Open dashboard:

   ```text
   https://stream.example.com
   ```

## Current Status

The current code is already wired for this pattern:

- MediaMTX accepts incoming streams.
- The Go backend connects to MediaMTX internally.
- The frontend talks only to the Go backend.
- Remote buses need only outbound access to the public server ingest URL.
