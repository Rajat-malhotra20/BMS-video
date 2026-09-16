"""HTTP layer: routes, JSON responses, auth/CORS. Ported from
prototype/backend/{api,main,auth,bridge_unified}.go, trimmed to the two
endpoints actually used: GET /api/fleet (bus list) and
GET /api/stream/{bus_id} (direct playable URL per cam). Everything else the
Go API exposed — bus detail, the bridge start/stop admin routes, the
sumithlive debug routes, the SSE fleet stream, the "/" listing — is dropped
on purpose; add back only what you actually call.

Also drops the MediaMTX reverse-proxy and the FLV byte-hub — Prod hands the
frontend the vendor's own URL directly (see vendors/*_adapter.py) instead of
proxying bytes through this process, so there is nothing to relay or fan out.
"""

import json
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

import auth
import fleet
from domain import StreamRequest, VendorError

CAMS_PER_BUS_FALLBACK = 4
# Chemito's live-video ports hold 4 channels each and the account exposes 4
# of them, so 16 concurrent channels is the account-wide ceiling
# (PMIDTC_CIPLAPIS.xlsx §4). Asking for more cannot be served no matter
# which bus asks.
CAMS_PER_BUS_CEILING = 16


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


def error_status(err: Exception) -> int:
    if isinstance(err, VendorError):
        if err.code == "capacity_limit":
            # The account's own channel ceiling is intentionally kept below
            # the vendor's real limit — this means "try again shortly," not
            # "something is broken."
            return 503
        if err.code == "device_offline":
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

                if path.startswith("/api/stream/"):
                    bus_id = path[len("/api/stream/"):]
                    if bus_id and "/" not in bus_id:
                        return self._handle_stream_live(bus_id, qs)

                self._send_error("not found", 404)
            except VendorError as e:
                self._send_error(str(e), error_status(e))
            except Exception as e:  # pragma: no cover - last-resort 500
                print(f"handler error: {e}")
                self._send_error("internal error", 500)

        def do_POST(self):
            parsed = urlparse(self.path)
            path = parsed.path

            if not self._authorized(parsed):
                return self._unauthorized()

            try:
                if path.startswith("/api/stream/") and path.endswith("/stop"):
                    bus_id = path[len("/api/stream/"):-len("/stop")]
                    if bus_id and "/" not in bus_id:
                        stopped = app.stop_bus(bus_id)
                        return self._send_json({"bus": bus_id, "stopped": stopped})

                self._send_error("not found", 404)
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

        def _handle_stream_live(self, bus_id, qs):
            cam_filter = (qs.get("cam") or [""])[0]
            have = [app.stream_info(cam, e) for cam, e in app.cams_for_bus(bus_id)]

            if cam_filter:
                want_cam = int(cam_filter)
                have = [si for si in have if si["cam"] == want_cam]
                if not have:
                    result = app.ensure_stream(bus_id, want_cam)
                    if result is not None:
                        have.append({"cam": want_cam, "path": result.key, "ready": True,
                                     "kind": result.kind, "directUrl": result.embed_url or result.url})
            else:
                have = app.ensure_all_cams(bus_id, have)

            self._send_json(have)

    return Handler


def serve(app: App, addr: str, port: int):
    server = ThreadingHTTPServer((addr, port), make_handler(app))
    print(f"fleet-bms-prod listening on {addr}:{port}")
    server.serve_forever()
