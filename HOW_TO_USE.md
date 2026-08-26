# How To Use — Fleet BMS API (Frontend)

This is a pure JSON API. All requests go to `http://<server-ip>:4000`
(or `https://bms-media.gna.energy` in production).

There are **3 endpoints** a frontend needs, plus one optional real-time
variant of the first. Media URLs (`directUrl`) always come *back* from
those endpoints — never build one yourself.

> **This deployment runs without a media server.** Every camera in use is
> delivered straight from the vendor to the browser, so there is no
> MediaMTX, no ffmpeg, and no RTSP relay. Endpoints that could only ever
> describe a relayed stream are **not registered** and return `404`, not an
> empty `200` — see [Endpoints that are not available](#endpoints-that-are-not-available).
> `GET /` lists exactly what this server is serving right now; trust it over
> this document if they ever disagree.

## 1. List every bus

```
GET /api/fleet
```

```json
{
  "buses": [
    { "id": "DL1PC0001", "cams": [1, 2, 3], "lastSeen": 1783352685, "camsAvailable": 3 },
    { "id": "DL1PC0002", "cams": [1], "lastSeen": 1783352685 }
  ],
  "totals": { "busesOnline": 2, "busesSeen": 2, "camsOnline": 4 }
}
```

- `cams` lists which camera numbers are currently live. Streams start
  automatically once at server boot (a redeploy/restart), so this is
  usually already populated — but streaming itself is still on-demand
  after that (see endpoint 3): if a bus's stream ever stops, nothing
  restarts it in the background until something requests it again.
- `camsAvailable` (when present) is how many cameras the vendor reports
  that bus having, independent of whether any are currently live — a
  free, no-cost signal (refreshed every ~30 min) for showing "this bus
  has N cameras" even before any of them are streaming.

### Real-time variant: `GET /api/fleet/stream`

Same data, pushed via [Server-Sent
Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events)
instead of polled — use this if you want live updates without repeatedly
calling `/api/fleet`:

```js
const es = new EventSource("http://<server-ip>:4000/api/fleet/stream");
es.onmessage = (event) => {
  const fleet = JSON.parse(event.data);
  // same shape as GET /api/fleet
};
```

A new message arrives roughly every 2 seconds while the fleet state
changes; the connection just stays open otherwise (no reconnect logic
needed — `EventSource` retries automatically if it drops).

## 2. Get detail for one bus

```
GET /api/bus/DL1PC0001
```

```json
{
  "id": "DL1PD8587",
  "cams": [
    { "cam": 1, "path": "DL1PD8587_1", "ready": true, "tracks": [],
      "bytesReceived": 0, "readers": 0,
      "kind": "flv", "directUrl": "/api/flv/DL1PD8587_1" },
    { "cam": 2, "path": "DL1PD8587_2", "ready": true, "tracks": [],
      "bytesReceived": 0, "readers": 0,
      "kind": "flv", "directUrl": "/api/flv/DL1PD8587_2" }
  ]
}
```

Every camera carries a `kind` and a `directUrl` — see endpoint 3 for how to
play each kind. Cams are sorted by `cam` number.

**`tracks`, `bytesReceived` and `readers` are always empty/zero here**, and
that is not a fault: those are byte counters from a media server relaying
the stream, and nothing is relayed in this deployment. Don't use them to
decide whether a camera is alive — use `ready`, and ultimately whether
playback succeeds.

## 3. Get a playable video link for one camera

```
GET /api/stream/DL1PC0001          # every camera on the bus
GET /api/stream/DL1PC0001?cam=1    # just one
```

Without `?cam=N` you get **every** channel the vendor reports for that bus,
starting any that aren't live yet — one call per bus is all a grid needs.
With `?cam=N` you get that one channel.

Either way, cameras that aren't already live are started on demand, so the
first call after a bus has been idle can take a few seconds; poll again if
`ready` isn't `true` yet.

`ready: true` means the session is registered, **not** that video is
flowing — the recorder is only contacted when you fetch the stream. A bus
reporting 9 cameras commonly has fewer wired: expect some tiles to 502 on
playback and render them as unavailable rather than treating it as an
error.

Response shape depends on how that camera is delivered — check for a
`kind` field to decide how to play it:

```json
[
  { "cam": 1, "path": "GJ03CU0206_1", "ready": true,
    "kind": "hls", "directUrl": "https://rtmpvideo.uffizio.com/hls/864819050951795_cam1.m3u8" }
]
```
```json
[
  { "cam": 1, "path": "DLPD8611_1", "ready": true,
    "kind": "flv", "directUrl": "/api/flv/DLPD8611_1" }
]
```

Every camera has a `kind`. There is no fourth, un-`kind`ed case in this
deployment: that shape (`whepUrl` + `hlsUrl`, a stream relayed through this
server) only exists when a media server is running, which it is not.

- **`kind: "flv"`** → live HTTP-FLV proxied by this server. Play
  `directUrl` with [mpegts.js](https://github.com/xqq/mpegts.js) (see
  below). All of a bus's cameras stream independently, so a 4-up grid is
  4 separate players.
- **`kind: "hls"`** → play `directUrl` straight in a `<video>` tag with
  hls.js — it's the vendor's own CDN URL, not proxied through this server.
- **`kind: "embed"`** → `directUrl` is a page, not a media URL — load it
  in an `<iframe>` instead.

### Playing `kind: "flv"`

```js
const player = mpegts.createPlayer(
  { type: "flv", isLive: true, url: "http://<server-ip>:4000" + cam.directUrl },
  { liveBufferLatencyChasing: true },
);
player.attachMediaElement(videoEl);
player.load();
player.play();
```

Two things to get right:

- **Don't open `directUrl` in a browser tab.** It serves `video/x-flv`,
  which no browser plays natively, so you'll get a file download instead
  of a picture. That's expected — the URL is for mpegts.js to fetch, not
  to navigate to.
- **Back off when you reconnect.** These devices can't close a session, so
  every reconnect leaves another one open on the recorder and a tight
  retry loop will take the whole device offline. Wait a few seconds and
  double it per failure; give up after a handful. The server enforces its
  own 20-second-per-camera cooldown regardless (see below), so a fast
  retry only earns a `503`.

There's a working reference implementation of a 4-up grid at
[prototype/flv-demo.html](prototype/flv-demo.html) — open it in a browser
and type a bus id.

### Errors from endpoint 3 and `/api/flv/{key}`

| Status | Means | What to do |
|---|---|---|
| `503` + `Retry-After` | That camera failed recently and is cooling down; the server didn't call the vendor at all | Wait for `Retry-After`, then retry |
| `502` "device did not deliver video" | The recorder is offline, asleep, or out of concurrent sessions | Show the camera as unavailable; retry much later |
| `502` "vendor closed the stream connection" | That channel number has no camera wired to it | Stop asking for this cam — it will never work |
| `404` | The bus isn't known to any vendor account | Check the bus id |

## Endpoints that are not available

These are **not routed** in this deployment and return `404`. They are not
broken and not empty — recording and stream relaying both require a media
server, and this deployment does not run one. Do not code against them, and
do not treat the `404` as an outage.

| Route | Was for | Why it is gone |
|---|---|---|
| `GET /api/stream/{id}/recording` | mp4 clips of a past window | needs the media server to have recorded it |
| `GET /api/bridge` | listing active remux jobs | no remux jobs can exist |
| `/live/...` | HLS playback of a relayed stream | proxy to a media server that is not running |
| `/whep/...` | WebRTC playback of a relayed stream | same |
| `/playback/...` | recording playback | same |

Clips and evidence come from each vendor's own API instead.

Any other unrecognised path also returns `404`. (It used to return `200`
with the endpoint listing, so a typo looked like success — that is fixed.)

These come back automatically if the raw-packet side is ever switched on
(`INGEST_URL` set, MediaMTX + `media-mtxd` running). Nothing was deleted;
the code for them lives in `media-MTX/`.

## Cheat sheet

| I want to... | Call this |
|---|---|
| See all buses | `GET /api/fleet` |
| See all buses, live-updating (no polling) | `GET /api/fleet/stream` |
| See one bus's cameras in detail | `GET /api/bus/{busId}` |
| Watch one specific camera live (starts it if needed) | `GET /api/stream/{busId}?cam=2` |
| Play what that returned | `kind: "flv"` → mpegts.js · `kind: "hls"` → hls.js · `kind: "embed"` → `<iframe>` |
| Stop a camera | `POST /api/bridge/stop?key={busId}_{cam}` |
| Check the server is alive | `GET /health` |
| See which endpoints actually exist | `GET /` |
| Watch a past moment | **not available** — no recording in this deployment |

## Things to know

- Bus IDs and camera counts are **not fixed** — the server discovers them
  from real camera traffic and vendor accounts. Nothing to register.
- Streams auto-start once when the server boots (a redeploy/restart) so
  the fleet is usually already populated — after that, streaming is
  on-demand: nothing restarts a stream in the background if it stops.
- For `kind: "flv"` buses, auto-start does **not** contact the recorder —
  it only marks the cameras as known. So `/api/fleet` can list a camera
  that turns out to be offline once you actually play it. Treat a `502`
  from playback, not absence from the fleet list, as the real liveness
  signal.
- One vendor account allows **one active login at a time** (Chemito
  enforces this — a new login invalidates the previous key). The server
  logs in once and shares that session, so a full grid is fine, but two
  backends on the same credentials (a local one and the deployed one) will
  knock out each other's cameras as they open. A stream already playing is
  unaffected; only ones being opened at that instant die.
- Each `kind: "flv"` viewer opens its own session on the recorder, and
  these devices support only a few at once (measured: 6-7 concurrent
  channels on one bus before it starts refusing). Several people watching the
  same bus at the same time can exhaust it. Don't hold players open on
  cameras nobody is looking at — destroy the player when its tile is
  hidden.
- Nothing is relayed through this server except the FLV byte stream. There
  is no transcoding and no recording, so a camera is either playable live
  from its vendor right now or not at all.
- `GET /` is the source of truth for which endpoints exist. It changes with
  the deployment, so a frontend can feature-detect instead of hardcoding.
- This is a dev setup: no login/auth yet. Don't expose it to the public
  internet as-is.
