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

# Doc §7: "A port can configure up to 4 channels" / "4-way video on one port
# and 16-way video at most". The account ceiling is therefore
# len(live_ports) x 4, enforced per port — a 5th channel on a full port comes
# back from the vendor as HTTP 408 or an instant EOF, indistinguishable from
# a dead camera.
PORT_CHANNEL_LIMIT = 4

# Crash-safety backstop for a slot never explicitly released (tab closed
# without stop-bus reaching us). The frontend re-resolves every live cam
# every ~150s (VideoPlayer.tsx FLV_URL_MAX_AGE_MS), which renews the slot,
# so anything silent for 5 minutes is gone.
ACTIVE_SLOT_TTL = 5 * 60

# Chemito has no "close live stream" call and keeps a channel busy for a
# while after the viewer disconnects (seen live: a bus opened right after
# another got 408/500 on every cam). A released slot therefore keeps
# counting against its port for this long — handing that port out sooner
# only turns an honest 503 into a 408 that looks like a dead camera.
# ponytail: 120s is a starting estimate; tune from "cooling" in the pod logs.
VENDOR_LINGER = 120

# An account's live ports practically never change; caching them means a
# full account is skipped without a vendor round trip.
PORTS_CACHE_TTL = 5 * 60

# Auth-error codes from Appendix 1 that a re-login can fix. Anything else
# (device offline, no data) must not trigger one — a needless login
# invalidates the key every other viewer is holding.
AUTH_ERROR_CODES = {203, 204, 209, 210}

# Record API's st is 1=main, 0=sub (live's is the reverse).
RECORD_STREAM_MAIN = 1
RECORD_STREAM_SUB = 0


class _Account:
    """One Chemito login: its own session key, its own len(ports) x 4 live
    slots and their cooldown. The vendor counts both per account, so each
    extra account is an independent set of 16 channels."""

    def __init__(self, base_url: str, username: str, password: str):
        self.base_url = base_url
        self.username = username
        self.password = password

        self._key_lock = threading.Lock()
        self._key = ""

        self._active_lock = threading.Lock()
        self._active = {}  # slot_key ({bus}_{cam}) -> (last_renewed monotonic, port)
        self._cooling = {}  # slot_key -> (released monotonic, port): stopped, vendor still holding it
        self._ports, self._ports_at = [], 0.0

    def live_ports(self) -> list:
        """Cached for PORTS_CACHE_TTL; forget_ports() forces a refetch."""
        if self._ports and time.monotonic() - self._ports_at < PORTS_CACHE_TTL:
            return self._ports
        ports = self._call("live ports", lambda c: c.live_ports())
        if not ports:
            raise VendorError("chemitoapi", "resolve live source", code="no_live_ports", retryable=True)
        self._ports, self._ports_at = ports, time.monotonic()
        return ports

    def forget_ports(self) -> None:
        self._ports_at = 0.0

    # ---- port slots: least-loaded, sticky per channel, capped per port ----

    def _claim_port(self, slot_key: str, ports: list) -> int:
        with self._active_lock:
            now = time.monotonic()
            for k, (last, _) in list(self._active.items()):
                if now - last > ACTIVE_SLOT_TTL:
                    del self._active[k]
            for k, (released, _) in list(self._cooling.items()):
                if now - released > VENDOR_LINGER:
                    del self._cooling[k]

            # Same channel again (renewal, or back to a bus just left): keep
            # its port — the vendor's lingering session is this very channel.
            held = self._active.get(slot_key) or self._cooling.get(slot_key)
            if held and held[1] in ports:
                self._cooling.pop(slot_key, None)
                self._active[slot_key] = (now, held[1])
                return held[1]

            load = {p: 0 for p in ports}
            for slots in (self._active, self._cooling):
                for k, (_, p) in slots.items():
                    if k != slot_key and p in load:
                        load[p] += 1
            best = min(ports, key=load.get)
            if load[best] >= PORT_CHANNEL_LIMIT:
                raise VendorError("chemitoapi", "resolve live video url", code="capacity_limit", retryable=True)
            self._active[slot_key] = (now, best)
            return best

    def release(self, channel_key: str) -> None:
        """Called by StreamService once a viewer explicitly stops. The slot
        keeps counting for VENDOR_LINGER (the vendor is still holding the
        channel), then frees. Safe for a key that never held a slot."""
        with self._active_lock:
            held = self._active.pop(channel_key, None)
            if held is not None:
                self._cooling[channel_key] = (time.monotonic(), held[1])

    def _drop(self, channel_key: str) -> None:
        """Frees immediately — for a resolve that failed, so no vendor
        session was opened on this attempt."""
        with self._active_lock:
            self._active.pop(channel_key, None)

    def _counts(self) -> tuple:
        with self._active_lock:
            return len(self._active), len(self._cooling)

    def holds(self, channel_key: str) -> bool:
        """Has this channel live, or still cooling (going back to it reuses it)."""
        with self._active_lock:
            return channel_key in self._active or channel_key in self._cooling

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

    def _call(self, op: str, fn):
        """Runs fn(client), re-logging in once if the vendor says the key is
        dead. Every vendor failure comes out as a VendorError."""
        for retry in (True, False):
            client = self._session()
            try:
                return fn(client)
            except rawclient.APIError as e:
                if retry and e.code in AUTH_ERROR_CODES:
                    with self._key_lock:
                        if self._key == client.key:  # another thread may have re-logged in already
                            self._key = ""
                    continue
                raise VendorError("chemitoapi", op, cause=e) from e
            except VendorError:
                raise
            except Exception as e:
                raise VendorError("chemitoapi", op, cause=e) from e


class Adapter:
    """Live cams fill the first account, then overflow onto the next with a
    free port (account 2, 3, ...) - one account's 16 channels only cover
    about two operators. Everything else (device list, recordings, panic,
    downloads) uses the first account."""

    def __init__(self, base_url: str, username: str, password: str, more_accounts=()):
        self.base_url = base_url
        self._accounts = [_Account(base_url, username, password)] + [
            _Account(base_url, u, p) for u, p in more_accounts]

    def name(self) -> str:
        return "chemitoapi"

    def _call(self, op: str, fn):
        return self._accounts[0]._call(op, fn)

    def release(self, channel_key: str) -> None:
        """Called by StreamService once a viewer explicitly stops: starts the
        channel's cooldown on whichever account holds it."""
        for acct in self._accounts:
            acct.release(channel_key)

    # ---- §7 live ----

    def resolve_live_source(self, req: StreamRequest) -> LiveSource:
        terid = req.vendor_params.get("terid", "")
        channel = req.cam or 1
        stream_type = rawclient.LIVE_STREAM_MAIN if req.main else rawclient.LIVE_STREAM_SUB

        # slot_key MUST be req.bus-based (not terid) — this is the exact
        # string StreamService.stop_stream releases by, since it only ever
        # knows channels as {bus}_{cam}, never by terid.
        slot_key = f"{req.bus}_{channel}"

        # A channel already on an account (renewal, or back to a bus just
        # left) stays there; a new one takes the first account with room.
        order = sorted(self._accounts, key=lambda a: not a.holds(slot_key))
        full = None
        for acct in order:
            ports = acct.live_ports()
            try:
                port = acct._claim_port(slot_key, ports)  # in memory: a full account costs no vendor call
            except VendorError as e:
                full = e
                continue
            held, cooling = acct._counts()
            print(f"chemitoapi: {slot_key}: {acct.username} port {port} of {ports}; "
                  f"{held} channel(s) held, {cooling} cooling")
            try:
                url = acct._call("resolve live video url",
                                 lambda c: c.live_video_url(terid, channel, req.audio, stream_type, port))
            except Exception:
                acct._drop(slot_key)  # a failed resolve must not sit on a port
                acct.forget_ports()   # in case the vendor's ports changed under the cache
                raise
            return LiveSource(kind=SourceKind.FLV, url=url, has_audio=True)
        raise full

    def list_cameras(self, vendor_params: dict) -> list:
        from domain import Camera

        devices = self._call("list devices", lambda c: c.list_devices())
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

    # ---- §8 history playback ----

    def record_files(self, vendor_params: dict, start: str, end: str, cams: list) -> list:
        terid = vendor_params.get("terid", "")
        rows = self._call("record file list",
                          lambda c: c.record_files(terid, start, end, cams, RECORD_STREAM_MAIN))
        return [{"cam": r.get("chn"), "start": r.get("starttime"), "end": r.get("endtime"),
                 "alarm": r.get("filetype") == 2} for r in rows]

    def playback(self, vendor_params: dict, cam: int, start: str, main: bool = True) -> dict:
        terid = vendor_params.get("terid", "")
        st = RECORD_STREAM_MAIN if main else RECORD_STREAM_SUB
        url, sn = self._call("record flv video", lambda c: c.record_flv(terid, start, cam, st))
        if not url:
            raise VendorError("chemitoapi", "record flv video", code="no_recording")
        return {"cam": cam, "kind": SourceKind.FLV, "directUrl": url, "sn": sn}

    def server_playback(self, vendor_params: dict, cam: int, start: str) -> dict:
        """Like playback(), from the recording stored on Chemito's server."""
        terid = vendor_params.get("terid", "")
        url, sn = self._call("server flv video", lambda c: c.server_flv(terid, start, cam))
        if not url:
            raise VendorError("chemitoapi", "server flv video", code="no_recording")
        return {"cam": cam, "kind": SourceKind.FLV, "directUrl": url, "sn": sn}

    def playback_range(self, vendor_params: dict, cam: int, start: str, end: str, main: bool = True) -> dict:
        """HLS of exactly start..end (the panic window). Unlike playback()
        there's no device session: nothing to stop, no "sn"."""
        terid = vendor_params.get("terid", "")
        st = RECORD_STREAM_MAIN if main else RECORD_STREAM_SUB
        url = self._call("record hls video", lambda c: c.record_hls(terid, start, end, cam, st))
        if not url:
            raise VendorError("chemitoapi", "record hls video", code="no_recording")
        return {"cam": cam, "kind": SourceKind.HLS, "directUrl": url}

    def playback_control(self, action: str, sn: str, start: str = "") -> None:
        self._call(f"flv {action}", lambda c: c.flv_control(action, sn, start))

    # ---- §9 download ----

    def add_download(self, vendor_params: dict, name: str, start: str, end: str, cams: list, main: bool = True) -> int:
        terid = vendor_params.get("terid", "")
        st = 1 if main else 0  # task API: 1 main (default), 0 sub
        return self._call("add download task",
                          lambda c: c.add_download_task(terid, start, end, cams, name, st))

    def download_state(self, taskid: int, date: str) -> dict:
        return self._call("download task state", lambda c: c.download_task_state(taskid, date))

    def download_files(self, taskid: int) -> list:
        return self._call("download task files", lambda c: c.download_task_files(taskid))

    def open_download(self, taskid: int, name: str):
        """Opens one finished task file by name. dir comes from the vendor's
        own file list, never from the caller — the vendor would happily
        serve any path the account can read."""
        files = self.download_files(taskid)
        match = next((f for f in files if f.get("name") == name), None)
        if match is None:
            raise VendorError("chemitoapi", "download file", code="no_file")
        return self._call("download file", lambda c: c.open_download(match["dir"], name))
