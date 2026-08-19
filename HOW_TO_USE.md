# How To Use — Fleet BMS API (Frontend)

This is a pure JSON API. All requests go to `http://<server-ip>:4000`
(or `https://bms-media.gna.energy` in production).

There are exactly **4 endpoints** a frontend needs.

## 1. List every bus

```
GET /api/fleet
```

```json
{
  "buses": [
    { "id": "DL1PC0001", "cams": [1, 2, 3], "lastSeen": 1783352685 },
    { "id": "DL1PC0002", "cams": [1], "lastSeen": 1783352685 }
  ],
  "totals": { "busesOnline": 2, "busesSeen": 2, "camsOnline": 4 }
}
```

- `cams` lists which camera numbers are currently live. An empty list means
  either nobody's watching that bus right now, or it's not streaming —
  streams are on-demand (see endpoint 3), not auto-started in the
  background, so an idle bus with real cameras still shows `cams: []`
  until something requests one.

## 2. Get detail for one bus

```
GET /api/bus/DL1PC0001
```

```json
{
  "id": "DL1PC0001",
  "cams": [
    { "cam": 1, "path": "DL1PC0001_1", "ready": true, "tracks": ["H264"],
      "bytesReceived": 2820466, "readers": 0 }
  ]
}
```

Bitrate/codec info (`tracks`, `bytesReceived`) and viewer count (`readers`)
per camera. Some cameras may instead show `"kind": "hls"` or
`"kind": "embed"` with a `"directUrl"` — see endpoint 3 for what that means.

## 3. Get a playable video link for one camera

```
GET /api/stream/DL1PC0001?cam=1
```

**Always pass `?cam=N`.** Calling this without it only reports
already-active sessions and starts nothing — it'll return `[]` for a bus
nobody has watched yet, even if that bus is really online.

If that cam isn't already live, this starts it on demand — first call
after the bus has been idle can take a few seconds while it connects;
poll again if `ready` isn't `true` yet.

Response shape depends on how that camera is delivered — check for a
`kind` field to decide how to play it:

```json
[
  { "cam": 1, "path": "DL1PC0001_1", "ready": true, "tracks": ["H264"],
    "bytesReceived": 2820466, "readers": 0,
    "whepUrl": "/whep/DL1PC0001_1/whep",
    "hlsUrl": "/live/DL1PC0001_1/index.m3u8" }
]
```
```json
[
  { "cam": 1, "path": "GJ03CU0206_1", "ready": true,
    "kind": "hls", "directUrl": "https://rtmpvideo.uffizio.com/hls/864819050951795_cam1.m3u8" }
]
```

- **No `kind` field** → a normal camera relayed through this server. Use
  `whepUrl` first (low-latency WebRTC). Fall back to `hlsUrl`
  (works everywhere, ~2-5s delay) — play it in a `<video>` tag with
  [hls.js](https://github.com/video-dev/hls.js).
- **`kind: "hls"`** → play `directUrl` straight in a `<video>` tag with
  hls.js — it's the vendor's own CDN URL, not proxied through this server.
- **`kind: "embed"`** → `directUrl` is a page, not a media URL — load it
  in an `<iframe>` instead.

## 4. Get a recording from the last hour

```
GET /api/stream/DL1PC0001/recording?from=2026-07-06T10:00:00Z&to=2026-07-06T10:02:00Z
```

```json
[
  { "cam": 1, "path": "DL1PC0001_1",
    "url": "/playback/get?path=DL1PC0001_1&start=2026-07-06T10:00:00Z&duration=120&format=mp4" }
]
```

Open `url` directly — it's a playable/downloadable mp4 clip. Only the
**last 1 hour** of video is kept; older windows return nothing. Add
`?cam=2` to get just one camera instead of all of them.

## Cheat sheet

| I want to... | Call this |
|---|---|
| See all buses | `GET /api/fleet` |
| See one bus's cameras in detail | `GET /api/bus/{busId}` |
| Watch one specific camera live (starts it if needed) | `GET /api/stream/{busId}?cam=2` |
| Watch a past moment | `GET /api/stream/{busId}/recording?from=...&to=...` |
| Check the server is alive | `GET /health` |

## Things to know

- Bus IDs and camera counts are **not fixed** — the server discovers them
  from real camera traffic and vendor accounts. Nothing to register.
- This is a dev setup: no login/auth yet. Don't expose it to the public
  internet as-is.
