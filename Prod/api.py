"""HTTP layer: routes, JSON responses, auth/CORS. Ported from
prototype/backend/{api,main,auth,bridge_unified}.go, trimmed to exactly what
BMS-Frontend/src/api/mediaClient.ts actually calls:

    GET  /api/fleet            bus list
    GET  /api/fleet/stream     same data over SSE (CctvMonitoring's live view)
    GET  /api/stream/{bus_id}  direct playable URL per cam, starts on demand
    POST /api/bridge/stop-bus  frees a bus's vendor capacity slots on switch

Recordings (Chemito doc §8/§9, times "YYYY-MM-DD HH:MM:SS" device-local):

    GET  /api/playback/{bus}/files?start&end&cam=1,2   recorded segments
    GET  /api/playback/{bus}?cam=N&start[&sub=1]       FLV playback from start -> {directUrl, sn}
    POST /api/playback/control?action=pause|resume|seek|stop&sn[&start]
    POST /api/download/{bus}?cam=1,2&start&end[&sub=1] -> {taskId, date}
    GET  /api/download/task/{taskId}?date              -> {state, percent, done, files[{name,url}]}
    GET  /api/download/task/{taskId}/file?name         file bytes (proxied, vendor key stays server-side)
    POST /api/panic/{bus}?time[&cam=1,2]               playback of every cam from press-10s (idempotent)

Everything else the Go API exposed — bus detail, bridge start, the
sumithlive debug routes, the "/" listing — is dropped on purpose; the
frontend never calls them. Confirmed by reading mediaClient.ts/VideoPlayer.tsx/
CctvMonitoring.tsx directly rather than assuming.

Also drops the MediaMTX reverse-proxy and the FLV byte-hub — Prod hands the
frontend the vendor's own URL directly (see vendors/*_adapter.py) instead of
proxying bytes through this process, so there is nothing to relay or fan out.
VideoPlayer.tsx already has a same-origin proxy path for a CORS-broken
vendor host (CORS_BROKEN_HOST_PROXY / nginx `/itms-media`), which is exactly
what a raw Chemito URL needs — nothing to add here for that.
"""

import json
import shutil
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, quote, urlparse

import auth
import fleet
from domain import StreamRequest, VendorError

CAMS_PER_BUS_FALLBACK = 4
# Chemito's live-video ports hold 4 channels each and the account exposes 4
# of them, so 16 concurrent channels is the account-wide ceiling
# (PMIDTC_CIPLAPIS.xlsx §4). Asking for more cannot be served no matter
# which bus asks.
CAMS_PER_BUS_CEILING = 16

# Panic button window around the press, and how long its playback sessions
# stay open: the 20s window plus slack for a slow first frame (cams have
# been measured at up to ~75s, see VideoPlayer.tsx).
PANIC_BEFORE = 10
PANIC_AFTER = 10
PANIC_PLAY_SECONDS = 120


class App:
    """Holds everything a request handler needs — one instance shared across
    every connection (ThreadingHTTPServer spins up a thread per request, not
    per app state)."""

    def __init__(self, stream_service, buses: dict, api_token: str, cors_origin: str):
        self.stream = stream_service
        self.buses = buses
        self.api_token = api_token
        self.cors_origin = cors_origin
        self.tracker = fleet.FleetTracker()
        self._panic_lock = threading.Lock()
        self._panic = {}  # (bus, alarm datetime) -> clip entry

    # ---- bus/vendor resolution ----

    def vendor_for(self, bus: str):
        """First config/buses.json (a manual pin), then the vendor roster —
        so an auto-discovered bus can still be started on demand. Returns
        (vendor, vendor_params) or (None, None)."""
        cfg = self.buses.get(bus)
        if cfg is not None:
            return cfg.vendor, cfg.vendor_params
        return self.stream.roster_vendor(bus)

    def ensure_stream(self, bus: str, cam: int):
        """What GET /api/stream/{id}?cam=N calls when that bus+cam isn't
        already active. Returns None when the bus isn't configured for
        bridging at all, or when it's already active (caller keeps the
        existing entry)."""
        key = f"{bus}_{cam}"
        if self.stream.is_active(key):
            return None
        vendor, params = self.vendor_for(bus)
        if vendor is None:
            return None
        return self.stream.start_stream(StreamRequest(bus=bus, cam=cam, vendor=vendor, main=True, audio=True, vendor_params=params or {}))

    def refresh_stream(self, bus: str, cam: int):
        """What GET /api/stream/{id}?cam=N calls — unlike ensure_stream,
        this ALWAYS re-resolves against the vendor, even if the cam is
        already active. It exists for the frontend's stale-URL watcher
        (VideoPlayer.tsx's onStale): Chemito's live-video URL is single-use/
        short-lived (confirmed live — a cached one is already dead for the
        next connection attempt), so a reconnect after a drop needs a fresh
        URL, not the cached one is_active would otherwise serve. Returns
        None only when the bus isn't configured for bridging at all."""
        vendor, params = self.vendor_for(bus)
        if vendor is None:
            return None
        return self.stream.start_stream(StreamRequest(bus=bus, cam=cam, vendor=vendor, main=True, audio=True, vendor_params=params or {}))

    # ---- fleet ----

    def fleet_summary(self) -> dict:
        summary = self.tracker.build(self.stream.active_direct_keys())
        summary = fleet.merge_roster(summary, self.stream.vendor_roster())

        counts = self.stream.channel_counts()
        for b in summary.buses:
            if b.id in counts:
                b.cams_available = counts[b.id]
        return summary.to_json()

    # ---- stream-live ----

    def cams_for_bus(self, bus_id: str) -> list:
        out = []
        for e in self.stream.active_direct_keys():
            b, cam, ok = fleet.parse_bus_path(e.key)
            if ok and b == bus_id:
                out.append((cam, e))
        out.sort(key=lambda t: t[0])
        return out

    def stream_info(self, cam: int, e) -> dict:
        return {"cam": cam, "path": e.key, "ready": True, "kind": e.kind, "directUrl": e.embed_url or e.url}

    def stop_bus(self, bus_id: str) -> list:
        """Stops every currently-active cam of bus_id, releasing each one's
        vendor capacity slot immediately (e.g. chemitoapi's per-account
        channel limit — see vendors/chemitoapi_adapter.Adapter.release)
        instead of leaving it to that adapter's TTL fallback. Called when the
        frontend switches away from a bus, so the next bus's cams aren't
        stuck waiting on a slot this one is done with."""
        stopped = []
        for _, e in self.cams_for_bus(bus_id):
            if self.stream.stop_stream(e.key):
                stopped.append(e.key)
        return stopped

    def ensure_all_cams(self, bus: str, have: list) -> list:
        """Brings up every channel of bus not live yet. A channel that fails
        to start is skipped, never fatal — one dead camera must not blank
        the others."""
        counts = self.stream.channel_counts()
        n = min(counts.get(bus, CAMS_PER_BUS_FALLBACK) or CAMS_PER_BUS_FALLBACK, CAMS_PER_BUS_CEILING)

        seen = {si["cam"] for si in have}
        pending = [cam for cam in range(1, n + 1) if cam not in seen]
        if not pending:
            return have

        def start_one(cam):
            try:
                result = self.ensure_stream(bus, cam)
            except Exception as e:
                print(f"stream live: {bus}_{cam}: {e}")
                return None
            if result is None:
                return None
            return {"cam": cam, "path": result.key, "ready": True, "kind": result.kind,
                    "directUrl": result.embed_url or result.url}

        with ThreadPoolExecutor(max_workers=max(1, len(pending))) as pool:
            for si in pool.map(start_one, pending):
                if si is not None:
                    have.append(si)

        have.sort(key=lambda si: si["cam"])
        return have

    # ---- recordings (history playback + download) ----

    def recording_adapter(self, bus: str):
        """(adapter, vendor_params) for a bus whose vendor serves recordings.
        Raises VendorError not_implemented for vendors that don't (sumithlive)."""
        vendor, params = self.vendor_for(bus)
        if vendor is None:
            raise VendorError("bms", f'bus "{bus}"', code="device_offline")
        adapter = self.stream.registry.get(vendor)
        if not hasattr(adapter, "playback"):
            raise VendorError(vendor, "recordings", code="not_implemented")
        return adapter, params or {}

    def chemito(self):
        return self.stream.registry.get("chemitoapi")

    # ---- panic playback: from press -PANIC_BEFORE, nothing saved ----

    def panic_playback(self, bus: str, alarm_time: str, cams: list = None) -> dict:
        """Starts device playback of every camera (or `cams`) from
        PANIC_BEFORE seconds before the press. Playback runs in real time, so
        it reaches press+PANIC_AFTER 20s after it starts — by then the bus
        has recorded it; no waiting needed.

        Idempotent while the sessions are open: the alarm system, every open
        browser and a driver pressing again within PANIC_AFTER all get the
        same sessions. They're stopped after PANIC_PLAY_SECONDS so an
        unwatched alarm doesn't keep the bus streaming over 4G; a call after
        that starts fresh ones (a replay)."""
        t = datetime.strptime(alarm_time, TIME_FORMAT)
        adapter, params = self.recording_adapter(bus)
        with self._panic_lock:
            now = time.monotonic()
            self._panic = {k: v for k, v in self._panic.items() if v["_expires"] > now}
            for (b, et), existing in self._panic.items():
                if b == bus and abs((t - et).total_seconds()) <= PANIC_AFTER:
                    break
            else:
                existing = None
                entry = {
                    "bus": bus, "time": alarm_time,
                    "start": (t - timedelta(seconds=PANIC_BEFORE)).strftime(TIME_FORMAT),
                    "end": (t + timedelta(seconds=PANIC_AFTER)).strftime(TIME_FORMAT),
                    "cams": [], "errors": {},
                    "_expires": now + PANIC_PLAY_SECONDS, "_ready": threading.Event(),
                }
                self._panic[(bus, t)] = entry
        if existing is not None:
            existing["_ready"].wait(30)  # a concurrent first call may still be opening sessions
            return existing

        if not cams:
            n = min(self.stream.channel_counts().get(bus) or CAMS_PER_BUS_FALLBACK, CAMS_PER_BUS_CEILING)
            cams = list(range(1, n + 1))

        def open_one(cam):
            try:
                return adapter.playback(params, cam, entry["start"])
            except Exception as e:
                entry["errors"][cam] = str(e)
                return None

        try:
            with ThreadPoolExecutor(max_workers=len(cams)) as pool:
                entry["cams"] = [p for p in pool.map(open_one, cams) if p]
        finally:
            entry["_ready"].set()

        if not entry["cams"]:
            with self._panic_lock:
                self._panic.pop((bus, t), None)
            raise VendorError("chemitoapi", f"panic playback {bus} @ {alarm_time}: {entry['errors']}",
                              code="no_recording")

        def stop_all():
            for p in entry["cams"]:
                try:
                    adapter.playback_control("stop", p["sn"])
                except Exception as e:
                    print(f"panic playback stop {bus} cam {p['cam']}: {e}")

        # ponytail: in-memory timer — a restart before it fires leaves those sessions to the vendor's own timeout.
        threading.Timer(PANIC_PLAY_SECONDS, stop_all).start()
        return entry


def download_links(taskid: int, files: list) -> list:
    return [{"name": f["name"], "url": f"/api/download/task/{taskid}/file?name={quote(f['name'])}"}
            for f in files]  # .mp4 plays in a browser, .264 is raw


TIME_FORMAT = "%Y-%m-%d %H:%M:%S"


def parse_time(qs, name: str) -> str:
    """Validates a "YYYY-MM-DD HH:MM:SS" query param (device local time) and
    returns it unchanged — that is the vendor's own format."""
    value = (qs.get(name) or [""])[0].strip()
    datetime.strptime(value, TIME_FORMAT)  # ValueError -> 400
    return value


def parse_cams(qs, default: str = "1") -> list:
    return [int(c) for c in ((qs.get("cam") or [default])[0]).split(",") if c.strip()]


def error_status(err: Exception) -> int:
    if isinstance(err, VendorError):
        if err.code == "capacity_limit":
            # The account's own channel ceiling is intentionally kept below
            # the vendor's real limit — this means "try again shortly," not
            # "something is broken."
            return 503
        if err.code in ("device_offline", "no_recording", "no_file"):
            return 404
        if getattr(err.cause, "code", None) in (212, 215, 401):  # vendor: no data / no file / terminal offline
            return 404
        if err.code == "not_implemented":
            return 501
        return 502
    return 502


def make_handler(app: App):
    class Handler(BaseHTTPRequestHandler):
        server_version = "fleet-bms-prod/1.0"

        def log_message(self, fmt, *args):
            print(f"{self.command} {self.path} - {fmt % args}")

        # ---- plumbing ----

        def _send_json(self, obj, status=200):
            body = json.dumps(obj).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self._send_cors()
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _send_error(self, message, status):
            body = (message + "\n").encode()
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self._send_cors()
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _send_cors(self):
            self.send_header("Access-Control-Allow-Origin", app.cors_origin)
            self.send_header("Vary", "Origin")
            self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")

        def _authorized(self, parsed) -> bool:
            if self.path == "/health":
                return True
            qs = parse_qs(parsed.query)
            token = (qs.get("token") or [""])[0]
            return auth.check_token(app.api_token, self.headers.get("Authorization", ""), token)

        def do_OPTIONS(self):
            self.send_response(204)
            self._send_cors()
            self.end_headers()

        def do_GET(self):
            parsed = urlparse(self.path)
            path = parsed.path
            qs = parse_qs(parsed.query)

            if path == "/health":
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; charset=utf-8")
                self.end_headers()
                self.wfile.write(b"ok")
                return

            if not self._authorized(parsed):
                return self._unauthorized()

            try:
                if path == "/api/fleet":
                    return self._send_json(app.fleet_summary())

                if path == "/api/fleet/stream":
                    return self._handle_fleet_stream()

                if path.startswith("/api/stream/"):
                    bus_id = path[len("/api/stream/"):]
                    if bus_id and "/" not in bus_id:
                        return self._handle_stream_live(bus_id, qs)

                if path.startswith("/api/playback/"):
                    rest = path[len("/api/playback/"):]
                    if rest.endswith("/files"):
                        adapter, params = app.recording_adapter(rest[:-len("/files")])
                        return self._send_json(adapter.record_files(
                            params, parse_time(qs, "start"), parse_time(qs, "end"), parse_cams(qs, "1,2,3,4")))
                    if rest and "/" not in rest:
                        adapter, params = app.recording_adapter(rest)
                        sub = (qs.get("sub") or [""])[0] == "1"
                        return self._send_json(adapter.playback(params, parse_cams(qs)[0], parse_time(qs, "start"), main=not sub))

                if path.startswith("/api/download/task/"):
                    parts = path[len("/api/download/task/"):].split("/")
                    taskid = int(parts[0])
                    if len(parts) == 1:
                        return self._handle_download_state(taskid, (qs.get("date") or [""])[0])
                    if parts[1:] == ["file"]:
                        return self._handle_download_file(taskid, (qs.get("name") or [""])[0])

                self._send_error("not found", 404)
            except ConnectionError:  # BrokenPipe / Reset / Aborted
                # Client left before we answered (tab closed, bus switched,
                # proxy timeout). Nothing to send; trying to write an error
                # to the same dead socket just raises again.
                print(f"{self.command} {self.path}: client disconnected before response")
                self.close_connection = True  # don't try to read another request off it
            except ValueError as e:
                self._send_error(f"bad request: {e}", 400)
            except VendorError as e:
                self._send_error(str(e), error_status(e))
            except Exception as e:  # pragma: no cover - last-resort 500
                print(f"handler error: {e}")
                self._send_error("internal error", 500)

        def do_POST(self):
            parsed = urlparse(self.path)
            path = parsed.path
            qs = parse_qs(parsed.query)

            if not self._authorized(parsed):
                return self._unauthorized()

            try:
                if path == "/api/bridge/stop-bus":
                    bus_id = (qs.get("bus") or [""])[0]
                    if not bus_id:
                        return self._send_error("bus query parameter is required", 400)
                    stopped = app.stop_bus(bus_id)
                    return self._send_json({"bus": bus_id, "stopped": stopped})

                if path == "/api/playback/control":
                    action = (qs.get("action") or [""])[0]
                    sn = (qs.get("sn") or [""])[0]
                    if action not in ("pause", "resume", "seek", "stop") or not sn:
                        return self._send_error("action (pause|resume|seek|stop) and sn are required", 400)
                    start = parse_time(qs, "start") if action == "seek" else ""
                    app.chemito().playback_control(action, sn, start)
                    return self._send_json({"ok": True})

                if path.startswith("/api/panic/"):
                    bus_id = path[len("/api/panic/"):]
                    if bus_id and "/" not in bus_id:
                        cams = parse_cams(qs, "") if qs.get("cam") else None
                        entry = app.panic_playback(bus_id, parse_time(qs, "time"), cams)
                        return self._send_json({k: v for k, v in entry.items() if not k.startswith("_")})

                if path.startswith("/api/download/"):
                    bus_id = path[len("/api/download/"):]
                    if bus_id and "/" not in bus_id:
                        adapter, params = app.recording_adapter(bus_id)
                        start, end = parse_time(qs, "start"), parse_time(qs, "end")
                        sub = (qs.get("sub") or [""])[0] == "1"
                        taskid = adapter.add_download(params, bus_id, start, end, parse_cams(qs), main=not sub)
                        return self._send_json({"taskId": taskid, "date": start[:10]})

                self._send_error("not found", 404)
            except ConnectionError:  # BrokenPipe / Reset / Aborted
                # Client left before we answered (tab closed, bus switched,
                # proxy timeout). Nothing to send; trying to write an error
                # to the same dead socket just raises again.
                print(f"{self.command} {self.path}: client disconnected before response")
                self.close_connection = True  # don't try to read another request off it
            except ValueError as e:
                self._send_error(f"bad request: {e}", 400)
            except VendorError as e:
                self._send_error(str(e), error_status(e))
            except Exception as e:  # pragma: no cover - last-resort 500
                print(f"handler error: {e}")
                self._send_error("internal error", 500)

        def _unauthorized(self):
            self.send_response(401)
            self.send_header("WWW-Authenticate", 'Bearer realm="fleet-bms-prod"')
            self._send_cors()
            self.end_headers()
            self.wfile.write(b"unauthorized\n")

        # ---- handlers ----

        def _handle_fleet_stream(self):
            """CctvMonitoring.tsx opens this once as a plain EventSource and
            keeps it open for the life of the page — same fleet_summary()
            data as GET /api/fleet, pushed as it changes instead of polled.
            A frame is only sent when the summary actually differs from the
            last one, so an idle fleet doesn't spam empty updates."""
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "keep-alive")
            self._send_cors()
            self.end_headers()

            last_sent = None
            try:
                while True:
                    body = json.dumps(app.fleet_summary())
                    if body != last_sent:
                        self.wfile.write(f"data: {body}\n\n".encode())
                        self.wfile.flush()
                        last_sent = body
                    time.sleep(2)
            except (BrokenPipeError, ConnectionResetError):
                return

        def _handle_stream_live(self, bus_id, qs):
            cam_filter = (qs.get("cam") or [""])[0]

            if cam_filter:
                # A specific cam is an explicit ask for a CURRENT url, not
                # "what's already running" — always re-resolve. This is what
                # the frontend's stale-URL watcher calls; see
                # App.refresh_stream's docstring for why serving the cached
                # entry here would defeat the point of that watcher.
                want_cam = int(cam_filter)
                result = app.refresh_stream(bus_id, want_cam)
                if result is None:
                    return self._send_error(f'bus "{bus_id}" is not configured for bridging', 404)
                have = [{"cam": want_cam, "path": result.key, "ready": True,
                         "kind": result.kind, "directUrl": result.embed_url or result.url}]
            else:
                have = [app.stream_info(cam, e) for cam, e in app.cams_for_bus(bus_id)]
                have = app.ensure_all_cams(bus_id, have)

            self._send_json(have)

        def _handle_download_state(self, taskid, date):
            """Poll until done. state 3 = completed (see doc §9 stateSix);
            files then carry a ready-to-use url (add ?token= like any call)."""
            datetime.strptime(date, "%Y-%m-%d")  # the "date" returned by POST /api/download/{bus}
            adapter = app.chemito()
            st = adapter.download_state(taskid, date)
            out = {"taskId": taskid, "state": st.get("state"), "percent": st.get("percent", 0),
                   "done": st.get("state") == 3, "files": []}
            if out["done"]:
                out["files"] = download_links(taskid, adapter.download_files(taskid))
            self._send_json(out)

        def _handle_download_file(self, taskid, name):
            """Streams the finished file through this process: the vendor
            URL carries the account's shared key, which must never reach a
            browser."""
            if not name:
                return self._send_error("name query parameter is required", 400)
            resp = app.chemito().open_download(taskid, name)
            try:
                self.send_response(200)
                self.send_header("Content-Type", resp.headers.get("Content-Type", "video/mp4"))
                if resp.headers.get("Content-Length"):
                    self.send_header("Content-Length", resp.headers["Content-Length"])
                self.send_header("Content-Disposition", f'attachment; filename="{name}"')
                self._send_cors()
                self.end_headers()
                shutil.copyfileobj(resp, self.wfile, 64 * 1024)
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                resp.close()

    return Handler


def serve(app: App, addr: str, port: int):
    server = ThreadingHTTPServer((addr, port), make_handler(app))
    print(f"fleet-bms-prod listening on {addr}:{port}")
    server.serve_forever()
