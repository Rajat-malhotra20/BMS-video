# Remote Bus Streaming Setup

This app should run on a public server when buses are on different internet connections.

## Why Same WiFi Is Not Enough

The local setup works only when the camera publisher and API server can reach the same machine directly. Real buses usually use mobile networks, and those networks are commonly behind NAT or CGNAT. That means your server cannot reliably connect inward to the bus.

The reliable pattern is:

```text
Bus camera / bus gateway
  pushes video outward over the internet
        |
        v
Public VPS / cloud server
  MediaMTX + Go API backend
        |
        v
Client apps
  call the JSON API and play the returned stream URLs
```

## Current App Structure

The current project has:

```text
prototype/
  docker-compose.yml
  backend/
    main.go
    fleet.go
    api.go
    Dockerfile
    go.mod
  mediamtx_conf/
    mediamtx.yml
```

The Go backend is a pure JSON API that proxies MediaMTX:

```text
GET  /                              -> API service descriptor
GET  /api/fleet                     -> all buses, live cam counts, status
GET  /api/bus/{id}                  -> per-camera detail for one bus
GET  /api/stream/{id}               -> live WHEP/HLS URLs for one bus's cams
GET  /api/stream/{id}/recording     -> recording clip URL(s) for a time range
GET  /health                        -> Go backend health

/live/     -> MediaMTX HLS       http://mediamtx:8888
/whep/     -> MediaMTX WebRTC    http://mediamtx:8889
/playback/ -> MediaMTX playback  http://mediamtx:9996
/mtx-api/  -> MediaMTX API       http://mediamtx:9997/v3
```

Any external frontend (web, mobile, another service) calls these endpoints directly — the backend does not serve any UI itself.

Inside Docker Compose, `backend` connects to `mediamtx` by service name:

```yaml
MEDIAMTX_HLS_URL: http://mediamtx:8888
MEDIAMTX_WEBRTC_URL: http://mediamtx:8889
MEDIAMTX_API_URL: http://mediamtx:9997/v3
MEDIAMTX_PLAYBACK_URL: http://mediamtx:9996
```

## Bus and Camera ID Convention

Bus IDs are alphanumeric strings (e.g. `DL1PC0001`), passed through as-is. Each camera on a bus publishes to a path named `<BUS_ID>_<camNo>`, camera numbers starting at 1:

```text
DL1PC0001_1
DL1PC0001_2
DL1PC0001_3
```

The backend discovers buses dynamically from whatever paths are currently publishing to MediaMTX — there is no separate static roster to maintain.

## How It Works For Remote Buses

Deploy the full Docker stack on a public VPS or cloud VM.

Example public domain:

```text
stream.example.com
```

Each bus camera publishes to the public server:

```text
rtmp://stream.example.com:11935/DL1PC0001_1
rtmp://stream.example.com:11935/DL1PC0001_2
rtmp://stream.example.com:11935/DL1PC0001_3
```

Client apps call the API from:

```text
http://stream.example.com:4000/api/fleet
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
| `443` | TCP | HTTPS API access, production |
| `4000` | TCP | Go backend (JSON API) |
| `11935` | TCP | RTMP ingest from buses |
| `18554` | TCP | RTSP, optional |
| `18888` | TCP | MediaMTX HLS, optional direct access |
| `18889` | TCP | MediaMTX WebRTC WHIP/WHEP, optional direct access |
| `18189` | UDP | WebRTC media |
| `19996` | TCP | MediaMTX playback (recordings), optional direct access |
| `19997` | TCP | MediaMTX API, optional direct access |

For production, avoid exposing `9997` publicly unless it is protected. Client apps only need the Go backend's proxy routes.

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

Call the API at:

```text
https://stream.example.com/api/fleet
```

Caddy will automatically request and renew the certificate.

For local LAN testing, keep using:

```text
http://192.168.1.6:4000/api/fleet
```

## Running Locally

From the repository root:

```powershell
docker compose -f prototype\docker-compose.yml up -d --build
```

Check the API:

```text
http://localhost:4000/api/fleet
```

Publish a test stream with a camera app such as Larix, or FFmpeg:

```text
rtmp://<server-ip>:11935/DL1PC0001_1
```

For local testing on the same machine:

```text
rtmp://localhost:11935/DL1PC0001_1
```

Then query it:

```text
http://localhost:4000/api/bus/DL1PC0001
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
Bus publishes RTMP or SRT -> MediaMTX -> Client app plays HLS or WebRTC via the API's returned URLs
```

## Recording Retention

MediaMTX records every published camera to a rolling 1-hour buffer. Request a clip for any window inside that hour via:

```text
GET /api/stream/{busId}/recording?from=<RFC3339>&to=<RFC3339>
```

Requests for windows older than 1 hour will 404 when played, since the underlying segments have been deleted.

## Security Notes

The current `mediamtx.yml` is development-friendly and allows anonymous publish/read.

Before exposing this server on the internet:

1. Add authentication for publishing.
2. Restrict or hide the MediaMTX API port.
3. Use HTTPS for API access.
4. Keep firewall rules tight.

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
   rtmp://stream.example.com:11935/<bus_id>_<cam_no>
   ```

8. Call the API:

   ```text
   https://stream.example.com/api/fleet
   ```

## Current Status

The current code is already wired for this pattern:

- MediaMTX accepts incoming streams.
- The Go backend connects to MediaMTX internally.
- The backend exposes a pure JSON API — no UI is bundled or served.
- Remote buses need only outbound access to the public server ingest URL.
