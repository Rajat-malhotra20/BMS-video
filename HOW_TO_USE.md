# How To Use — Fleet BMS API

This project lets bus cameras stream video to a server, and gives any app a
simple API to list buses, watch live video, and pull recent recordings.

No frontend is included — this is a pure JSON API. Build your own UI (web,
mobile, whatever) that calls it.

## 1. Start the server

```powershell
cd prototype
docker compose up -d --build
```

This starts two things:
- **MediaMTX** — receives camera video, records it, plays it back
- **Go backend** — the JSON API your app will talk to, on `http://localhost:4000`

Check it's running:

```powershell
curl http://localhost:4000/health
```

Should print `ok`.

## 2. Point cameras at the server

Each camera on a bus streams to a URL shaped like this:

```
rtmp://<server-ip>:11935/<BUS_ID>_<CAMERA_NUMBER>
```

- `BUS_ID` — the bus's ID, e.g. `DL1PC0001`. Use it exactly as-is.
- `CAMERA_NUMBER` — `1`, `2`, or `3` for that bus's cameras.

Example, bus `DL1PC0001` with 3 cameras:

```
rtmp://<server-ip>:11935/DL1PC0001_1
rtmp://<server-ip>:11935/DL1PC0001_2
rtmp://<server-ip>:11935/DL1PC0001_3
```

Any camera app that can push RTMP (or SRT/WHIP) works — no setup needed on
the server side, it just starts showing up once a camera connects.

## 3. Use the API

All requests go to `http://<server-ip>:4000`.

### See every bus and how many cameras are live

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

`cams` lists which camera numbers are currently live. An empty list means
that bus was seen recently but isn't streaming right now.

### See detail for one bus

```
GET /api/bus/DL1PC0001
```

Returns bitrate, codec, and viewer count for each of that bus's cameras.

### Get a playable video link for a bus

```
GET /api/stream/DL1PC0001
```

```json
[
  { "cam": 1, "path": "DL1PC0001_1", "ready": true,
    "whepUrl": "/whep/DL1PC0001_1/whep",
    "hlsUrl": "/live/DL1PC0001_1/index.m3u8" }
]
```

- `whepUrl` — for low-latency live video (WebRTC). Use this first.
- `hlsUrl` — fallback, works everywhere, ~2-5 second delay.

Add `?cam=2` to get just one camera instead of all of them.

Play `hlsUrl` directly in any `<video>` tag with [hls.js](https://github.com/video-dev/hls.js),
or use a WHEP client library for `whepUrl`.

### Get a recording from the last hour

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
**last 1 hour** of video is kept; older windows return nothing.

Add `?cam=2` to get just one camera instead of all of them.

## 4. Quick cheat sheet

| I want to... | Call this |
|---|---|
| See all buses | `GET /api/fleet` |
| See one bus's cameras in detail | `GET /api/bus/{busId}` |
| Watch a bus live | `GET /api/stream/{busId}` |
| Watch one specific camera live | `GET /api/stream/{busId}?cam=2` |
| Watch a past moment | `GET /api/stream/{busId}/recording?from=...&to=...` |
| Start a vendor-managed bus (see section 6) | `POST /api/bridge/start` |
| Check the server is alive | `GET /health` |

## 5. Things to know

- Bus IDs and camera counts are **not fixed** — the server figures out who's
  online by watching who's currently streaming. Nothing to register.
- Recordings only cover the **last hour**. Anything older is gone.
- This is a dev setup: no login/auth yet. Don't expose it to the public
  internet as-is — see `prototype/REMOTE_BUSES_SETUP.md` for production
  hardening notes.
- Full endpoint list is also always available by hitting the API root:
  `GET /` on `http://localhost:4000`.

## 6. Bringing a vendor camera device into the fleet

Cameras don't all speak plain RTMP — some are managed by a vendor platform
instead. The frontend never needs to know which: register the bus once in
a config file, then call one endpoint with just the bus id and camera
number, same as section 3.

### Quick cheat sheet

| I want to... | Call this |
|---|---|
| Start watching a vendor-managed bus | `POST /api/bridge/start` |
| See what's currently bridged | `GET /api/bridge` |
| Stop a bridged camera | `POST /api/bridge/stop?key={bus}_{cam}` |

```
POST /api/bridge/start
Content-Type: application/json

{ "bus": "DL1PC0001", "cam": 1, "main": true, "audio": false }
```

Response — shape depends on the vendor behind that bus, but the frontend
doesn't need to branch on which one; `kind` says how to play it:

```json
{ "key": "DL1PC0001_1", "kind": "rtsp", "rtspOut": "rtsp://localhost:8554/DL1PC0001_1" }
```
```json
{ "key": "DL1PC0001_1", "kind": "embed", "embedUrl": "https://trakzee2.uffizio.com/jsp/..." }
```
- `kind: "rtsp"` → play `rtspOut` same as section 3 (it shows up in
  `GET /api/fleet` / `GET /api/stream/{busId}` too).
- `kind: "embed"` → load `embedUrl` in an iframe; no MediaMTX path is
  created for this one.

This needs two config files the frontend never sees:
`config/vendors.json` (each vendor's account credentials) and
`config/buses.json` (which vendor + device id each bus uses) — see
`config/*.json.example` for the shape. A bus not listed in `buses.json`
returns `404`. Requires the `ffmpeg` binary on `PATH` at runtime.

### Admin/debug: calling one vendor directly

Everything below (6a-6c) is what `/api/bridge/start` calls internally,
kept around for debugging one vendor in isolation and for looking up the
device ids that go into `config/buses.json` (e.g.
`GET /api/bridge/n9m/devices` to find a `dsno`). The frontend shouldn't
need any of this.

### 6a. Castmaster-managed CCTV (HTTP)

If the bus's cameras are registered on a Castmaster NVR/CCTV server, call:

```
POST /api/bridge/castmaster/start
Content-Type: application/json

{
  "baseUrl": "https://cmmipl.org:22056",
  "username": "admin",
  "password": "...",
  "terid": "<vendor device serial number>",
  "channel": 1,
  "main": true,
  "audio": false,
  "bus": "DL1PC0001",
  "cam": 1
}
```

This logs into Castmaster, resolves the live FLV URL for that device
channel, and starts a supervised `ffmpeg` remux into
`rtsp://.../DL1PC0001_1`. Response:

```json
{ "key": "DL1PC0001_1", "path": "DL1PC0001_1", "rtspOut": "rtsp://localhost:8554/DL1PC0001_1" }
```

The bus now shows up in `GET /api/fleet` and `GET /api/stream/DL1PC0001`
like any other camera.

### 6b. Chemito N9M devices (direct TCP)

N9M devices connect straight to this server instead of to a third-party
platform. Two extra TCP listeners run alongside the HTTP API (env vars
`N9M_SIGNAL_ADDR` / `N9M_MEDIA_ADDR`, default `:9500` signaling / `:9501`
media) — point the device's server IP/port configuration at those.

Check which devices are currently connected:

```
GET /api/bridge/n9m/devices
```

```json
[{ "dsno": "00600052B8", "devName": "Dik3339", "carNum": "AP36TS1234", "connectedAt": "..." }]
```

Once a device shows up, start its live feed:

```
POST /api/bridge/n9m/start
Content-Type: application/json

{
  "dsno": "00600052B8",
  "bus": "DL1PC0001",
  "cam": 1,
  "channel": 1,
  "main": true,
  "audio": false,
  "format": "h264"
}
```

This asks the connected device to open a media stream, reads the raw
elementary-stream frames off it, and republishes them via `ffmpeg` into
`rtsp://.../DL1PC0001_1` — same response shape and same fleet/stream
visibility as Castmaster above.

### 6c. Stopping / listing bridge jobs

```
GET  /api/bridge                 # status of every active bridge job
POST /api/bridge/stop?key=DL1PC0001_1
```

`key` is the `{bus}_{cam}` value returned by either start call above.

### 6d. Using the vendor protocol packages directly (Go)

If you're writing Go code against this repo instead of calling the HTTP
bridge, each vendor protocol has its own package under
`prototype/backend/vendorclients/`:

| Package | Protocol | What it's for |
|---|---|---|
| `castmaster` | Castmaster HTTP CCTV API | Login, live/playback video URLs, alarms, evidence-center, VOIP talk-token |
| `n9m` + `n9mserver` | Chemito N9M (JSON-over-TCP) | Device status/control, alarm decoding, and the TCP listener that accepts inbound device connections |
| `sumith` | Sumith CCU-SCU (pipe-delimited ASCII) | OBU login/location/health/alarm reporting, route/duty tracking, CCU→OBU device config, SMS command channel |
| `t808` | OBU-to-Server binary protocol | Register/auth, location report (with alarm/status bitflags), attendance, operation requests, driving-plan schedules, CAN bus upload |

Example — decode an inbound Sumith frame:

```go
import "mediamtx-console/vendorclients/sumith"

codec := sumith.NewCodec()
frame, err := codec.ParseVerify(rawLine) // validates checksum
if err != nil { /* handle */ }
switch frame.Token {
case "LGN":
    login, err := sumith.DecodeLogin(frame)
case "NR", "NM":
    loc, err := sumith.DecodeLocation(frame)
}
```

Example — build a t808 location report to send:

```go
import "mediamtx-console/vendorclients/t808"

body, _ := t808.BuildLocationReport(t808.LocationReport{
    AlarmFlags: t808.AlarmOverSpeed,
    Status:     t808.StatusACCOn | t808.StatusPositioned,
    Latitude:   19973921, Longitude: 73805160, // degrees * 1e6
    Elevation: 610, Speed: 0, Direction: 247,
    Time: t808.BCDDateTime{Year: 25, Month: 7, Day: 28, Hour: 12, Minute: 0, Second: 0},
})
raw, _ := t808.Build(t808.Frame{MsgID: t808.MsgIDLocationReport, Phone: "008291915608", Serial: 1, Body: body})
```

Every package has a `_test.go` file alongside it showing a working
build/parse round trip for each message type — the fastest way to see the
exact shape a given message expects.

**Known gaps, called out in code comments where they matter:**
- `t808`: the source doc never documents byte-stuffing/escaping for 0x7E
  inside a frame, or a subpackage/fragmentation bit layout. `Build`/`Parse`
  don't apply escaping by default; `EscapeJT808`/`UnescapeJT808` are
  provided as an opt-in if a real device turns out to need it.
- `sumith`: the checksum algorithm is ambiguous in the source doc; this
  implementation defaults to CRC-32/IEEE rendered as 8 hex digits,
  overridable via `Codec.Checksum`.
