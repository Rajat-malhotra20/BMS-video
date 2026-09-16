"""Adapts vendorclients.chemitoapi to the vendors.Adapter contract. Ported
from prototype/backend/vendors/chemitoapi/adapter.go.

Difference from the Go version, per Prod's "always give a direct URL"
requirement: no flv hub / byte proxy sits in front of the stream any more.
ResolveLiveSource hands back the vendor's own LiveVideoURL as-is (like
sumithlive's HLS URL already does) instead of a resolver the hub re-runs
on every reconnect. The frontend plays that URL directly (e.g. with
mpegts.js) and re-fetches GET /api/stream/{id} for a fresh one if it drops
— the token is single-use/short-lived either way, hub or no hub.
"""

import threading
import time

from domain import LiveSource, SourceKind, StreamRequest, VendorError
from vendorclients import chemitoapi as rawclient

# The account's real 16-channel ceiling (4 ports x 4 channels — the doc's
# §4: "4-way video on one port and 16-way video at most at the same time").
MAX_ACTIVE_CHANNELS = 16

# Crash-safety backstop for a slot never explicitly released (e.g. this
# process restarting mid-stream). Real release happens from
# StreamService.stop_stream calling release() the moment a viewer stops.
ACTIVE_SLOT_TTL = 30 * 60

# Auth-error codes from Appendix 1 that a re-login can fix. Anything else
# (device offline, no data) must not trigger one — a needless login
# invalidates the key every other viewer is holding.
AUTH_ERROR_CODES = {203, 204, 209, 210}


class Adapter:
    def __init__(self, base_url: str, username: str, password: str):
        self.base_url = base_url
        self.username = username
        self.password = password

        self._key_lock = threading.Lock()
        self._key = ""

        self._port_lock = threading.Lock()
        self._port_by_channel = {}

        self._active_lock = threading.Lock()
        self._active = {}  # channel_key -> last_renewed (monotonic)

    def name(self) -> str:
        return "chemitoapi"

    # ---- capacity slot (soft rate limit against the vendor's real ceiling) ----

    def _acquire_slot(self, channel_key: str) -> None:
        with self._active_lock:
            now = time.monotonic()
            for k, last in list(self._active.items()):
                if now - last > ACTIVE_SLOT_TTL:
                    del self._active[k]

            if channel_key in self._active:
                self._active[channel_key] = now
                return

            if len(self._active) >= MAX_ACTIVE_CHANNELS:
                raise VendorError("chemitoapi", "resolve live video url", code="capacity_limit", retryable=True)

            self._active[channel_key] = now

    def release(self, channel_key: str) -> None:
        """Frees channel_key's capacity slot immediately — called by
        StreamService once a viewer explicitly stops. Safe for a key that
        never held a slot."""
        with self._active_lock:
            self._active.pop(channel_key, None)

    def _active_count(self) -> int:
        with self._active_lock:
            return len(self._active)

    # ---- port selection: least-loaded, sticky per channel ----

    def _port_for(self, ports: list, channel_key: str) -> int:
        with self._port_lock:
            if channel_key in self._port_by_channel:
                p = self._port_by_channel[channel_key]
                if p in ports:
                    return p  # still offered by the account — keep it stable

            load = {}
            for p in self._port_by_channel.values():
                load[p] = load.get(p, 0) + 1
            best = min(ports, key=lambda p: load.get(p, 0))
            self._port_by_channel[channel_key] = best
            return best

    # ---- shared login session ----

    def _session(self) -> rawclient.Client:
        """One key for the whole process, not one per call: the server keeps
        a single active session per account, and a second login invalidates
        every stream token minted from the first (confirmed live 2026-08-26:
        6 concurrent logins left 1/6 playing, one shared key left 6/6)."""
        with self._key_lock:
            client = rawclient.Client(self.base_url)
            if self._key:
                client.use_key(self._key)
                return client
            try:
                self._key = client.login(self.username, self.password)
            except Exception as e:
                raise VendorError("chemitoapi", "login", cause=e) from e
            return client

    def _forget_key(self) -> None:
        with self._key_lock:
            self._key = ""

    def _resolve_url(self, terid: str, channel: int, audio: bool, stream_type: int) -> str:
        def attempt():
            client = self._session()
            try:
                ports = client.live_ports()
            except Exception as e:
                raise VendorError("chemitoapi", "list live ports", cause=e) from e
            if not ports:
                raise VendorError("chemitoapi", "resolve live source", code="no_live_ports", retryable=True)

            print(f"chemitoapi: {terid}_{channel}: account offers {len(ports)} live port(s); "
                  f"{self._active_count()} channel(s) currently held by this process")

            port = self._port_for(ports, f"{terid}_{channel}")
            try:
                return client.live_video_url(terid, channel, audio, stream_type, port)
            except Exception as e:
                raise VendorError("chemitoapi", "resolve live video url", cause=e) from e

        try:
            return attempt()
        except rawclient.APIError as e:
            if e.code in AUTH_ERROR_CODES:
                self._forget_key()
                return attempt()
            raise

    def resolve_live_source(self, req: StreamRequest) -> LiveSource:
        terid = req.vendor_params.get("terid", "")
        channel = req.cam or 1
        stream_type = rawclient.LIVE_STREAM_MAIN if req.main else rawclient.LIVE_STREAM_SUB

        # slotKey MUST be req.bus-based (not terid) — this is the exact
        # string StreamService.stop_stream releases by, since it only ever
        # knows channels as {bus}_{cam}, never by terid.
        slot_key = f"{req.bus}_{channel}"
        self._acquire_slot(slot_key)

        url = self._resolve_url(terid, channel, req.audio, stream_type)
        return LiveSource(kind=SourceKind.FLV, url=url, has_audio=True)

    def list_cameras(self, vendor_params: dict) -> list:
        from domain import Camera

        client = self._session()
        try:
            devices = client.list_devices()
        except Exception as e:
            raise VendorError("chemitoapi", "list devices", cause=e) from e

        cams = []
        for d in devices:
            vendor_id = d.get("carlicence") or d.get("terid", "")
            cams.append(Camera(
                vendor_id=vendor_id,
                label=vendor_id,
                vendor_params={"terid": d.get("terid", "")},
                channels=d.get("channelcount", 0),
            ))
        return cams
