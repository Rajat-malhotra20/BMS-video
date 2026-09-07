# How To Use — Fleet BMS API (Frontend)

This is a pure JSON API. All requests go to `http://<server-ip>:4000`
(or `https://bms-media.gna.energy` in production).

There are **3 endpoints** a frontend needs, plus one optional real-time
variant of the first. Media URLs (`directUrl`) always come *back* from
those endpoints — never build one yourself.

> **This deployment runs without a media server.** Every camera in use is
> delivered straight from the vendor to the browser, so there is no
> MediaMTX, no ffmpeg, and no RTSP relay, and — as of this version — no
> proxy for the FLV bytes either: `kind: "flv"` cameras hand back the
> vendor's own stream URL directly, and the browser connects to the
> vendor, not to this server. Endpoints that could only ever describe a
> relayed stream are **not registered** and return `404`, not an empty
> `200` — see [Endpoints that are not available](#endpoints-that-are-not-available).
> `GET /` lists exactly what this server is serving right now; trust it over
> this document if they ever disagree.

## 0. Authentication

Every request except `GET /health` needs the shared token, either as a
header or a query parameter:

```
Authorization: Bearer <token>
```
```
GET /api/stream/DL1PC0001?token=<token>
```

Use the query form for URLs a browser fetches on its own — a `<video src>`
playing HLS cannot attach a header. Ask whoever deployed the API for the
token; a missing or wrong one returns `401`.

If the deployment has no token configured the API answers without one, but
do not rely on that: it is the unlocked-development state, not the contract.

---

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
- **`kind: "flv"` buses are never counted in `cams` here.** They are not
  tracked as "active sessions" any more (see endpoint 3), so they cannot
  appear in the fleet snapshot the way an RTSP-ingested or embed/HLS bus
  does. Don't use `/api/fleet` to decide whether an FLV camera is live —
  call `/api/stream/{id}` and check the response instead.

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
      "bytesReceived": 0, "readers": 0 }
  ]
}
```

Same caveat as `/api/fleet`: this reflects tracked sessions, and
`kind: "flv"` cameras are never tracked as one, so an FLV bus with nobody
currently mid-request against `/api/stream` will show an empty `cams`
list here even though it's fully available. Use endpoint 3 to actually
find out what's playable.

**`tracks`, `bytesReceived` and `readers` are always empty/zero**, and
that is not a fault: those are byte counters from a media server relaying
the stream, and nothing is relayed in this deployment.

## 3. Get a playable video link for one camera

```
GET /api/stream/DL1PC0001          # every camera on the bus
GET /api/stream/DL1PC0001?cam=1    # just one
```

Without `?cam=N` you get **every** channel the vendor reports for that bus.
With `?cam=N` you get that one channel.

**This is the endpoint that actually talks to the vendor**, and it does so
on every single call for `kind: "flv"` cameras — there is no "already
active, here's the cached link" shortcut for FLV any more. Each call asks
the vendor for a fresh stream URL and hands it straight back. Practical
consequences:

- Only call this when you're actually about to hand the URL to a player
  (on load, or to reconnect after an error). Don't poll it on a timer the
  way you might poll `/api/fleet` — every poll is a real request against
  the vendor device.
- Every viewer who calls this for the same camera gets their **own**
  vendor session/channel — there is no sharing any more. Ten people
  watching one camera now costs the device ten channels, not one (see
  "Things to know" below for the device's actual channel ceiling).

```json
[
  { "cam": 1, "path": "GJ03CU0206_1", "ready": true,
    "kind": "hls", "directUrl": "https://rtmpvideo.uffizio.com/hls/864819050951795_cam1.m3u8" }
]
```
```json
[
  { "cam": 1, "path": "DLPD8611_1", "ready": true,
    "kind": "flv", "directUrl": "http://<vendor-host>:<port>/api/v1/live/video?...&key=..." }
]
```

Every camera has a `kind`. There is no fourth, un-`kind`ed case in this
deployment: that shape (`whepUrl` + `hlsUrl`, a stream relayed through this
server) only exists when a media server is running, which it is not.

- **`kind: "flv"`** → live HTTP-FLV, played **directly from the vendor** —
  `directUrl` is the vendor's own absolute URL, not a path on this server.
  Play it with [mpegts.js](https://github.com/xqq/mpegts.js) (see below).
  This server is not in the data path for FLV at all any more: it only
  brokers the login/URL resolution, then gets out of the way. All of a
  bus's cameras stream independently, so a 4-up grid is 4 separate
  players, each with its own vendor URL.
- **`kind: "hls"`** → play `directUrl` straight in a `<video>` tag with
  hls.js — it's the vendor's own CDN URL, not proxied through this server.
- **`kind: "embed"`** → `directUrl` is a page, not a media URL — load it
  in an `<iframe>` instead.

### Playing `kind: "flv"`

```js
const player = mpegts.createPlayer(
  { type: "flv", isLive: true, url: cam.directUrl },   // already an absolute vendor URL — do NOT prefix it with this server's address
  { liveBufferLatencyChasing: true },
);
player.attachMediaElement(videoEl);
player.load();
player.play();
```

Three things to get right:

- **`directUrl` is absolute already.** Earlier versions of this API served
  FLV through a same-origin proxy path (`/api/flv/{key}`) and needed the
  server address prepended. That proxy is gone — the URL you get back
  already points at the vendor, so use it as-is.
- **Don't open `directUrl` in a browser tab.** It serves `video/x-flv`,
  which no browser plays natively, so you'll get a file download instead
  of a picture. That's expected — the URL is for mpegts.js to fetch, not
  to navigate to.
- **On reconnect, re-fetch `/api/stream`, don't resume the old URL.** The
  vendor's link is single-use/short-lived, so a stale one won't work. Back
  off between attempts too: these devices can't close a session on their
  end, so a tight retry loop opens a new phantom session on every attempt
  and can take the whole device offline. Wait a few seconds and roughly
  double it per failure; give up after a handful of tries.

### Stopping a `kind: "flv"` camera

`POST /api/bridge/stop?key={busId}_{cam}` exists, but for `kind: "flv"`
it has nothing to stop server-side — FLV sessions aren't tracked as
"active" here (see above), so this call will report `"stopped": false`
for one even while it's genuinely playing in a browser. The only way to
actually end an FLV stream is for the browser to stop requesting/playing
it (destroy the player). `/api/bridge/stop` still works for real for
`embed`/`hls` sessions and RTSP-ingested ones.

### Errors from endpoint 3

| Status | Means | What to do |
|---|---|---|
| `502` | The vendor call failed — device offline, no live ports, vendor rejected the request, etc. (see server logs for the specific vendor error) | Show the camera as unavailable; retry later, with backoff |
| `404` (bare bus, no `?cam=`) | The bus isn't known to any vendor account | Check the bus id |
| *(cam omitted from the array)* | For a bare `GET /api/stream/{id}` (no `?cam=`), a single bad channel is silently dropped rather than failing the whole request | Treat a missing cam number as unavailable, not an error |

## Endpoints that are not available

These are **not routed** in this deployment and return `404`. They are not
broken and not empty — recording and stream relaying both require a media
server, and this deployment does not run one. Do not code against them, and
do not treat the `404` as an outage.

| Route | Was for | Why it is gone |
|---|---|---|
| `GET /api/stream/{id}/recording` | mp4 clips of a past window | needs the media server to have recorded it |
| `GET /api/bridge` | listing active remux jobs | no remux jobs can exist |
| `/api/flv/{key}` | this server proxying FLV bytes | removed — `kind: "flv"` now hands back the vendor's own URL directly instead |
| `/live/...` | HLS playback of a relayed stream | proxy to a media server that is not running |
| `/whep/...` | WebRTC playback of a relayed stream | same |
| `/playback/...` | recording playback | same |

Clips and evidence come from each vendor's own API instead.

Any other unrecognised path also returns `404`.

The MediaMTX-backed routes come back automatically if the raw-packet side
is ever switched on (`INGEST_URL` set, MediaMTX + `media-mtxd` running).
Nothing was deleted there; that code lives in `media-MTX/`. The FLV proxy
(`/api/flv/{key}`), however, was removed from the codebase entirely, not
just disabled — see `git log` on `prototype/backend/flvhub.go` if you need
the old byte-proxying behavior back.

## Cheat sheet

| I want to... | Call this |
|---|---|
| See all buses | `GET /api/fleet` |
| See all buses, live-updating (no polling) | `GET /api/fleet/stream` |
| See one bus's cameras in detail | `GET /api/bus/{busId}` |
| Watch one specific camera live (starts it if needed, re-resolves every call for FLV) | `GET /api/stream/{busId}?cam=2` |
| Play what that returned | `kind: "flv"` → mpegts.js, `directUrl` used as-is · `kind: "hls"` → hls.js · `kind: "embed"` → `<iframe>` |
| Stop a camera | `POST /api/bridge/stop?key={busId}_{cam}` — no-op for `kind: "flv"`, real for `embed`/`hls`/RTSP |
| Check the server is alive | `GET /health` |
| See which endpoints actually exist | `GET /` |
| Watch a past moment | **not available** — no recording in this deployment |

## Things to know

- Bus IDs and camera counts are **not fixed** — the server discovers them
  from real camera traffic and vendor accounts. Nothing to register.
- Streams auto-start once when the server boots (a redeploy/restart) so
  the fleet is usually already populated — after that, streaming is
  on-demand: nothing restarts a stream in the background if it stops.
- One vendor account allows **one active login at a time** (Chemito
  enforces this — a new login invalidates the previous key and every
  stream token minted from it). The server logs in once and reuses that
  key indefinitely — it does **not** proactively re-login on a timer any
  more, only when there's no cached key yet, or a vendor call actually
  fails with an auth error. That means the app itself no longer causes
  periodic drops. It still can't prevent an *external* login to the same
  vendor account (e.g. someone using the vendor's own portal with the
  same credentials) from invalidating the shared key — if that happens,
  the next call this server makes will detect the auth failure and log in
  again automatically, but whatever was open at that moment may drop.
  Keep these credentials exclusive to this deployment if at all possible.
- Viewers are **not free** for `kind: "flv"` any more. Every call to
  `/api/stream/{id}?cam=N` opens a fresh vendor channel for that one
  caller — there is no shared upstream connection fanned out to multiple
  viewers the way there used to be. N viewers on one camera costs the
  device N channels. The device's own channel ceiling still applies on
  top of that: Chemito's live-video ports hold 4 channels each, the
  account exposes 4 ports, so 16 concurrent channels is the practical
  account-wide ceiling regardless of how many buses ask.
- Nothing is relayed through this server for `kind: "flv"` any more — not
  even the bytes. The browser talks to the vendor directly once it has the
  URL; this server's only role is resolving that URL (and holding the
  shared login key needed to do so).
- `GET /` is the source of truth for which endpoints exist. It changes with
  the deployment, so a frontend can feature-detect instead of hardcoding.
- This is a dev setup: no login/auth yet beyond the shared `API_TOKEN`.
  Don't expose it to the public internet as-is.
