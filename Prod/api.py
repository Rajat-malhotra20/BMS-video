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
    POST /api/panic/{bus}?start&end[&cam=1,2]          playback of every cam for that window (idempotent)
    POST /api/panic/{bus}?time[&cam=1,2]               same, window = press -10s..+10s

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

# Panic button window around the press (when only ?time is given), and how
# long its playback sessions stay open past the window's own length: slack
# for a slow first frame (cams have been measured at up to ~75s, see
# VideoPlayer.tsx).
PANIC_BEFORE = 10
PANIC_AFTER = 10
PANIC_PLAY_SLACK = 100


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

    def refresh_stream(self, bus: str, cam: int, main: bool = True):
        """What GET /api/stream/{id} calls for each cam — ALWAYS re-resolves
        against the vendor, even if the cam is already active: Chemito's
        live-video URL is single-use/short-lived (confirmed live — a cached
        one is already dead for the next connection attempt), so every
        request needs a fresh URL, never a stored one. Re-resolving a held
        cam renews its slot rather than taking a new one. Returns None only
        when the bus isn't configured for bridging at all."""
        vendor, params = self.vendor_for(bus)
        if vendor is None:
            return None
        return self.stream.start_stream(StreamRequest(bus=bus, cam=cam, vendor=vendor, main=main, audio=main, vendor_params=params or {}))

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

    def stop_bus(self, bus_id: str) -> list:
        """Stops every currently-active cam of bus_id, releasing each one's
        vendor capacity slot (e.g. chemitoapi's per-account channel limit —
        see vendors/chemitoapi_adapter.Adapter.release; Chemito itself keeps
        the channel busy a while longer, which that adapter accounts for)
        instead of leaving it to that adapter's TTL fallback. Called when the
        frontend switches away from a bus."""
        stopped = []
        for _, e in self.cams_for_bus(bus_id):
            if self.stream.stop_stream(e.key):
                stopped.append(e.key)
        return stopped

    def ensure_all_cams(self, bus: str, main: bool = True) -> list:
        """A fresh URL for every channel of bus. A channel that fails to
        start is skipped, never fatal — one dead camera must not blank the
        others. Only when none started and the account's channel limit is
        why does it raise (capacity_limit -> 503 "try again shortly"), so
        the page can tell "busy" apart from "this bus has no cameras"."""
        counts = self.stream.channel_counts()
        n = min(counts.get(bus, CAMS_PER_BUS_FALLBACK) or CAMS_PER_BUS_FALLBACK, CAMS_PER_BUS_CEILING)
        busy = []

        def start_one(cam):
            try:
                result = self.refresh_stream(bus, cam, main)
            except Exception as e:
                print(f"stream live: {bus}_{cam}: {e}")
                if isinstance(e, VendorError) and e.code == "capacity_limit":
                    busy.append(e)
                return None
            if result is None:
                return None
            return {"cam": cam, "path": result.key, "ready": True, "kind": result.kind,
                    "directUrl": result.embed_url or result.url}

        with ThreadPoolExecutor(max_workers=n) as pool:
            have = [si for si in pool.map(start_one, range(1, n + 1)) if si is not None]

        if not have and busy:
            raise busy[0]
        return have

    # ---- recordings (history playback + download) ----

    def recording_adapter(self, bus: str):
        """(adapter, vendor_params) for a bus whose vendor serves recordings.
        Raises VendorError not_implemented for vendors that don't (sumithlive)."""
        vendor, params = self.vendor_for(bus)
        if vendor is None:
            raise VendorError("bms", f'bus "{bus}"', code="unknown_bus", details={"bus": bus})
        adapter = self.stream.registry.get(vendor)
        if not hasattr(adapter, "playback"):
            raise VendorError(vendor, "recordings", code="not_implemented")
        return adapter, params or {}

    def chemito(self):
        return self.stream.registry.get("chemitoapi")

    # ---- panic playback: a given window, nothing saved ----

    def panic_playback(self, bus: str, start: str, end: str, cams: list = None, alarm_time: str = None) -> dict:
        """Starts device playback of every camera (or `cams`) from `start`.
        Playback runs in real time, so it reaches `end` (end - start) seconds
        after it starts; the sessions are stopped then, plus
        PANIC_PLAY_SLACK for a slow first frame, so nothing past the window
        keeps the bus streaming over 4G.

        Idempotent while the sessions are open: the alarm system, every open
        browser and a driver pressing again within PANIC_AFTER (start and end
        each within that of an open window on the same bus) all get the same
        sessions. A call after they're stopped starts fresh ones (a replay)."""
        s = datetime.strptime(start, TIME_FORMAT)
        e = datetime.strptime(end, TIME_FORMAT)
        if e <= s:
            raise ValueError("end must be after start")
        adapter, params = self.recording_adapter(bus)
        play_seconds = (e - s).total_seconds() + PANIC_PLAY_SLACK
        with self._panic_lock:
            now = time.monotonic()
            self._panic = {k: v for k, v in self._panic.items() if v["_expires"] > now}
            for (b, es, ee), existing in self._panic.items():
                if (b == bus and abs((s - es).total_seconds()) <= PANIC_AFTER
                        and abs((e - ee).total_seconds()) <= PANIC_AFTER):
                    break
            else:
                existing = None
                entry = {
                    "bus": bus, "time": alarm_time, "start": start, "end": end,
                    "cams": [], "errors": {},
                    "_expires": now + play_seconds, "_ready": threading.Event(),
                }
                self._panic[(bus, s, e)] = entry
        if existing is not None:
            existing["_ready"].wait(30)  # a concurrent first call may still be opening sessions
            return existing

        if not cams:
            n = min(self.stream.channel_counts().get(bus) or CAMS_PER_BUS_FALLBACK, CAMS_PER_BUS_CEILING)
            cams = list(range(1, n + 1))

        def open_one(cam):
            try:
                return adapter.playback(params, cam, start)
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
                self._panic.pop((bus, s, e), None)
            raise VendorError("chemitoapi", f"panic playback {bus} {start}..{end}: {entry['errors']}",
                              code="no_recording",
                              details={"bus": bus, "start": start, "end": end, "cams": entry["errors"]})

        def stop_all():
            for p in entry["cams"]:
                try:
                    adapter.playback_control("stop", p["sn"])
                except Exception as e:
                    print(f"panic playback stop {bus} cam {p['cam']}: {e}")

        # ponytail: in-memory timer — a restart before it fires leaves those sessions to the vendor's own timeout.
        threading.Timer(play_seconds, stop_all).start()
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


# Every error body is {"error": {"status", "code", "message", "retryable",
# "detail", ...extra}}. code is stable for the frontend to switch on;
# message is safe to show a user as-is; detail is the raw server/vendor text
# for debugging. code -> (status, message, retryable):
ERRORS = {
    "bad_request": (400, "A parameter is missing or in the wrong format.", False),
    "unauthorized": (401, "Missing or wrong API token.", False),
    "not_found": (404, "No such API endpoint.", False),
    "unknown_bus": (404, "This bus is not known to the server. Use a bus id from GET /api/fleet.", False),
    "device_offline": (404, "The bus is offline, so its recorder can't be reached.", True),
    "no_recording": (404, "No recording found for that time on any camera.", False),
    "no_file": (404, "That file doesn't exist for this download task.", False),
    "internal_error": (500, "Unexpected server error.", True),
    "not_implemented": (501, "This bus's camera vendor doesn't support this.", False),
    "vendor_error": (502, "The camera vendor returned an error.", True),
    # The account's own channel ceiling: "try again shortly," not "broken."
    "capacity_busy": (503, "All live camera channels are in use. Try again shortly.", True),
}


def error_code(err: Exception) -> str:
    if isinstance(err, ValueError):
        return "bad_request"
    if not isinstance(err, VendorError):
        return "internal_error"
    if err.code == "capacity_limit":
        return "capacity_busy"
    if err.code in ERRORS:
        return err.code
    vendor_code = getattr(err.cause, "code", None)
    if vendor_code == 401:  # vendor: terminal not online
        return "device_offline"
    if vendor_code in (212, 215):  # vendor: no data / no file
        return "no_recording"
    return "vendor_error"


def error_status(err: Exception) -> int:
    return ERRORS[error_code(err)][0]


def error_body(code: str, detail: str = "", **extra) -> dict:
    status, message, retryable = ERRORS[code]
    return {"error": {"status": status, "code": code, "message": message, "retryable": retryable,
                      "detail": detail, **extra}}


def make_handler(app: App):
    class Handler(BaseHTTPRequestHandler):
        server_version = "fleet-bms-prod/1.0"

        def log_message(self, fmt, *args):
            # command/path are unset when the request line itself is garbage
            # (a TLS handshake or port scan hitting this plain-HTTP port) -
            # the stdlib still logs its 400 through here.
            print(f"{getattr(self, 'command', None) or '-'} {getattr(self, 'path', '-')} - {fmt % args}")

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

        def _send_error(self, code, detail="", **extra):
            body = error_body(code, detail, **extra)
            self._send_json(body, body["error"]["status"])

        def _send_exception(self, err):
            details = err.details if isinstance(err, VendorError) else {}
            self._send_error(error_code(err), str(err), **details)

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

                self._send_error("not_found", f"{self.command} {path}")
            except ConnectionError:  # BrokenPipe / Reset / Aborted
                # Client left before we answered (tab closed, bus switched,
                # proxy timeout). Nothing to send; trying to write an error
                # to the same dead socket just raises again.
                print(f"{self.command} {self.path}: client disconnected before response")
                self.close_connection = True  # don't try to read another request off it
            except (ValueError, VendorError) as e:
                self._send_exception(e)
            except Exception as e:  # pragma: no cover - last-resort 500
                print(f"handler error: {e}")
                self._send_error("internal_error")

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
                        return self._send_error("bad_request", "bus query parameter is required")
                    stopped = app.stop_bus(bus_id)
                    return self._send_json({"bus": bus_id, "stopped": stopped})

                if path == "/api/playback/control":
                    action = (qs.get("action") or [""])[0]
                    sn = (qs.get("sn") or [""])[0]
                    if action not in ("pause", "resume", "seek", "stop") or not sn:
                        return self._send_error("bad_request", "action (pause|resume|seek|stop) and sn are required")
                    start = parse_time(qs, "start") if action == "seek" else ""
                    app.chemito().playback_control(action, sn, start)
                    return self._send_json({"ok": True})

                if path.startswith("/api/panic/"):
                    bus_id = path[len("/api/panic/"):]
                    if bus_id and "/" not in bus_id:
                        cams = parse_cams(qs, "") if qs.get("cam") else None
                        if qs.get("start") or qs.get("end"):
                            entry = app.panic_playback(bus_id, parse_time(qs, "start"), parse_time(qs, "end"), cams)
                        else:
                            alarm = parse_time(qs, "time")
                            t = datetime.strptime(alarm, TIME_FORMAT)
                            entry = app.panic_playback(
                                bus_id, (t - timedelta(seconds=PANIC_BEFORE)).strftime(TIME_FORMAT),
                                (t + timedelta(seconds=PANIC_AFTER)).strftime(TIME_FORMAT), cams, alarm_time=alarm)
                        return self._send_json({k: v for k, v in entry.items() if not k.startswith("_")})

                if path.startswith("/api/download/"):
                    bus_id = path[len("/api/download/"):]
                    if bus_id and "/" not in bus_id:
                        adapter, params = app.recording_adapter(bus_id)
                        start, end = parse_time(qs, "start"), parse_time(qs, "end")
                        sub = (qs.get("sub") or [""])[0] == "1"
                        taskid = adapter.add_download(params, bus_id, start, end, parse_cams(qs), main=not sub)
                        return self._send_json({"taskId": taskid, "date": start[:10]})

                self._send_error("not_found", f"{self.command} {path}")
            except ConnectionError:  # BrokenPipe / Reset / Aborted
                # Client left before we answered (tab closed, bus switched,
                # proxy timeout). Nothing to send; trying to write an error
                # to the same dead socket just raises again.
                print(f"{self.command} {self.path}: client disconnected before response")
                self.close_connection = True  # don't try to read another request off it
            except (ValueError, VendorError) as e:
                self._send_exception(e)
            except Exception as e:  # pragma: no cover - last-resort 500
                print(f"handler error: {e}")
                self._send_error("internal_error")

        def _unauthorized(self):
            body = json.dumps(error_body("unauthorized")).encode()
            self.send_response(401)
            self.send_header("WWW-Authenticate", 'Bearer realm="fleet-bms-prod"')
            self.send_header("Content-Type", "application/json")
            self._send_cors()
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

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
            # sub=1: the vendor's low-bitrate sub-stream, no audio - what grid
            # tiles ask for. Fullscreen omits it and gets main + audio.
            main = (qs.get("sub") or [""])[0] != "1"

            # Both forms always re-resolve — see App.refresh_stream's
            # docstring for why a stored URL must never be served.
            if cam_filter:
                want_cam = int(cam_filter)
                result = app.refresh_stream(bus_id, want_cam, main)
                if result is None:
                    return self._send_error("unknown_bus", f'bus "{bus_id}" is not configured for bridging', bus=bus_id)
                have = [{"cam": want_cam, "path": result.key, "ready": True,
                         "kind": result.kind, "directUrl": result.embed_url or result.url}]
            else:
                have = app.ensure_all_cams(bus_id, main)

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
                return self._send_error("bad_request", "name query parameter is required")
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
