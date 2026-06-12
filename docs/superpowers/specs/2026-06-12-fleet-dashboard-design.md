# Fleet Dashboard — Design Spec

**Date:** 2026-06-12
**Status:** Approved (pending user review of this document)
**Location of new app:** `fleet-stack/dashboard`

## Goal

A production-quality, visually polished web dashboard for monitoring a bus fleet of up to 3000 buses, each with 3 cameras (stream paths named `bus_<busId>_<camNo>`, e.g. `bus_1_1`). The UI must stay smooth (no lag, no jank) at full fleet scale. Operators view live video only for the selected bus; all other buses show status only.

## Decisions made during brainstorming

| Topic | Decision |
|---|---|
| Viewing model | Fleet list (status only) → bus detail plays that bus's 3 cameras |
| List previews | No video/snapshots in list — status dots + camera counts only |
| Playback protocol | WebRTC (WHEP) primary, HLS (hls.js) automatic fallback |
| Recording | 10-minute rolling buffer per camera via MediaMTX native recording |
| Bus roster | Derived from MediaMTX active/recent paths only (no DB, no roster file) |
| Stack | React + Vite + TypeScript, Tailwind CSS, TanStack Query + TanStack Virtual |

## Architecture

```
buses ──RTMP/SRT──▶ MediaMTX ──┬─ WHEP :8889  (WebRTC playback, primary)
                               ├─ HLS  :8888  (fallback playback)
                               ├─ Recording: fMP4 segments, recordDeleteAfter: 10m
                               ├─ API  :9997  (path list)
                               └─ Playback :9996 (recording list + serve)
                                        ▲
Go backend (extended) ── proxies /whep /live /mtx-api /playback
                      ── new /api/fleet aggregation endpoint
                      ── serves React production build (static files)
                                        ▲
React + Vite + TS app (fleet-stack/dashboard)
```

### Components

1. **MediaMTX config changes** (`prototype/mediamtx_conf/mediamtx.yml`)
   - `record: yes`, fMP4 segments, `recordDeleteAfter: 10m`
   - Enable playback server on port 9996
2. **Go backend changes** (`prototype/backend/main.go`)
   - New proxy route: `/playback/` → `http://mediamtx:9996`
   - New endpoint `GET /api/fleet`: calls MediaMTX `paths/list` (paginated), parses
     `bus_<id>_<cam>` names, returns compact summary:
     `{"buses": [{"id": "1", "cams": [1,2], "lastSeen": ...}], "totals": {...}}`
     (~50 KB at full fleet vs 1–2 MB raw). Caches result for 2s so many concurrent
     dashboard clients don't hammer MediaMTX.
   - `GET /api/bus/{id}`: per-camera detail (bitrate, uptime, viewers, codec) for the
     selected bus only.
   - Serve `fleet-stack/dashboard/dist` as the SPA root.
3. **React app** (`fleet-stack/dashboard`)
   - `FleetList` — virtualized list (TanStack Virtual), search, status filter chips
   - `BusDetail` — 3 camera tiles, Live/Recordings tabs, per-cam stats
   - `VideoTile` — WHEP player with HLS fallback, state machine overlay
   - `RecordingsPanel` — 10-min timeline per camera, segment playback + download
   - `useFleet` / `useBusDetail` — TanStack Query polling hooks (5s interval)
   - `parsePath` — pure function `"bus_12_3"` → `{busId: "12", cam: 3}`; malformed
     names ignored

### Data flow

1. Frontend polls `/api/fleet` every 5 s → grouped bus summaries → virtual list renders
   only visible rows (~25 DOM rows for 3000 buses).
2. Operator selects a bus → `/api/bus/{id}` polled (5 s) for stats; 3 `VideoTile`
   components mount and play via WHEP (`/whep/bus_<id>_<cam>`).
3. Tile click → expanded layout (selected tile large, others small but still playing).
4. Recordings tab → `GET /playback/list?path=bus_<id>_<cam>` → timeline of segments in
   last 10 min → click plays fMP4 in `<video>`; download button per segment.

## UI design

Dark control-room theme. Single page, two panes.

```
┌──────────────────────────────────────────────────────────────┐
│ ◉ FLEET BMS     online 2841/2980 buses   8463/8940 cams  ⟳5s │
├──────────────┬───────────────────────────────────────────────┤
│ [search id…] │  BUS_0042                       ● 3/3 LIVE    │
│ [All|On|Off] │  ┌─────────┐ ┌─────────┐ ┌─────────┐         │
│ ● bus_1  3/3 │  │  CAM 1  │ │  CAM 2  │ │  CAM 3  │         │
│ ● bus_2  2/3 │  │ ▶ live  │ │ ▶ live  │ │ offline │         │
│ ◐ bus_3  1/3 │  └─────────┘ └─────────┘ └─────────┘         │
│ ● bus_42 ◀── │  [● Live] [⏪ Last 10 min]                    │
│   … virtual  │  per-cam: bitrate, uptime, codec, viewers    │
│   scroll     │                                               │
└──────────────┴───────────────────────────────────────────────┘
```

- **Header:** KPI counts (buses online, cams online), poll freshness indicator,
  animated count transitions, no layout shift.
- **Left pane:** instant client-side search by bus id; filter chips All / Online /
  Partial / Offline-recent; optional "problems first" sort. Row = status dot
  (green = all cams up, amber = partial, red = recently seen, now offline) + bus id +
  `n/3` cam count.
- **Right pane, Live tab:** 3 tiles, all playing by default (3 decoders is safe).
  Click tile → expand large; others shrink (still playing). Per-tile state overlay:
  connecting / live / reconnecting / offline.
- **Right pane, Recordings tab:** per-camera strip of available segments over last
  10 min; click to play; download button.
- **Empty/edge states:** no selection → fleet summary panel; bus drops mid-watch →
  "signal lost, retrying" overlay with auto-resume.

## Performance requirements & tactics

- Virtualized list: only visible rows in DOM; search/filter over in-memory array.
- Max 3 live video decoders at any time (selected bus). Players fully destroyed on
  bus switch — no decoder/socket leak.
- `/api/fleet` keeps poll payload ~50 KB regardless of fleet size; server-side cache
  (2 s) protects MediaMTX from many concurrent dashboard clients.
- Memoized rows; poll diffing so unchanged rows don't re-render; stats updates never
  re-render the `<video>` element.
- Target: list scroll at 60 fps with 3000 buses; bus switch → first frame < 1 s
  (WHEP); UI interactions never blocked by polling.

## Playback strategy

- **Primary:** WHEP (WebRTC) — sub-second latency.
- **Fallback:** if ICE fails or no frame within 3 s → hls.js low-latency mode.
- Reconnect with exponential backoff on drop; auto-resume when the path reappears in
  the fleet poll.
- Browsers cannot play RTMP/RTSP natively — those remain ingest-only protocols.

## Error handling

- Fleet poll failure → "data stale since hh:mm:ss" banner, keep last data, retry with
  backoff.
- Per-tile errors isolated — one bad camera never affects other tiles or the page.
- Malformed stream path names ignored by parser (logged to console in dev).
- All failure states visible in UI; no silent failures.

## Testing

- **Vitest unit tests:** `parsePath` (valid, malformed, edge ids), fleet grouping,
  status derivation (green/amber/red).
- **Go tests:** `/api/fleet` aggregation handler (mock MediaMTX response, pagination).
- **Manual smoke:** publish N test streams via FFmpeg loop, verify list rendering,
  WHEP playback, HLS fallback (block UDP), recording timeline.

## Out of scope (flagged)

- Server horizontal scaling: 3000 buses ≈ 9 Gbps ingest — exceeds a single VPS at
  full scale; multi-node MediaMTX is a later infra project. Frontend is unaffected
  (it consumes the same API shape).
- Map view, alerts/notifications, auth — future phases. (MediaMTX currently allows
  anonymous access; must be locked down before production exposure.)
- Long-term recording retention (only 10-min rolling buffer in scope).
