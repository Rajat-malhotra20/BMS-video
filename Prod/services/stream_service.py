"""Vendor-agnostic orchestration between the HTTP layer and the vendor
adapters. Ported from prototype/backend/services/streamservice.go, minus
the MediaMTX/ingest (RTSP remux) side: Prod only serves vendors that hand
back a directly playable URL, so there is no supervised ffmpeg job, no
IngestClient, and no StartIngest/StartAny split — every stream is a direct
one.
"""

import threading
import time
from dataclasses import dataclass, field

from domain import ErrAlreadyRunning, SourceKind, StreamRequest, StreamResult

ROSTER_CACHE_TTL = 30  # seconds
CHANNEL_COUNTS_CACHE_TTL = 30 * 60  # seconds


@dataclass
class RosterEntry:
    """One camera a vendor account reports knowing about, whether or not
    anyone has called start_stream for it."""
    key: str  # {bus}_{cam}, cam always 1 (this listing has no per-camera breakdown)
    vendor: str
    label: str = ""
    online: bool = False
    channels: int = 0
    vendor_params: dict = field(default_factory=dict)


@dataclass
class DirectEntry:
    """One {bus}_{cam} key currently live via a vendor result, so a later
    GET /api/stream/{id} can hand back a playable link without re-triggering
    the vendor's start-streaming side effect."""
    key: str
    kind: str
    embed_url: str = ""
    url: str = ""
    has_audio: bool = False


class StreamService:
    def __init__(self, registry):
        self.registry = registry

        self._direct_lock = threading.Lock()
        self._direct_active: dict[str, DirectEntry] = {}

        self._roster_lock = threading.Lock()
        self._roster_cache = None
        self._roster_cached_at = 0.0

        self._counts_lock = threading.Lock()
        self._counts_cache = None
        self._counts_cached_at = 0.0

    def vendor_roster(self) -> list:
        """Sweeps every registered vendor's list_cameras. Best-effort: a
        vendor that errors just contributes no entries, so one vendor's
        hiccup doesn't blank the whole fleet."""
        with self._roster_lock:
            if self._roster_cache is not None and (time.monotonic() - self._roster_cached_at) < ROSTER_CACHE_TTL:
                return self._roster_cache

        entries = []
        for adapter in self.registry.all():
            try:
                cams = adapter.list_cameras(None)
            except Exception as e:
                print(f"vendor roster: {adapter.name()}: list_cameras: {e}")
                continue
            for c in cams:
                label = "" if c.label == c.vendor_id else c.label
                entries.append(RosterEntry(
                    key=f"{c.vendor_id}_1",
                    vendor=adapter.name(),
                    label=label,
                    online=c.online,
                    channels=c.channels,
                    vendor_params=c.vendor_params,
                ))

        with self._roster_lock:
            self._roster_cache = entries
            self._roster_cached_at = time.monotonic()
        return entries

    def channel_counts(self) -> dict:
        """Per bus id, how many camera slots a vendor reports that bus
        having — purely informational, cached far longer than the roster
        since a device's channel count rarely changes."""
        with self._counts_lock:
            if self._counts_cache is not None and (time.monotonic() - self._counts_cached_at) < CHANNEL_COUNTS_CACHE_TTL:
                return self._counts_cache

        counts = {}
        for entry in self.vendor_roster():
            if entry.channels > 0:
                counts[entry.key.removesuffix("_1")] = entry.channels

        with self._counts_lock:
            self._counts_cache = counts
            self._counts_cached_at = time.monotonic()
        return counts

    def roster_vendor(self, bus: str):
        """Looks up which vendor + vendor_params a bus id resolves to,
        straight from the vendor roster — lets a bus with no buses.json
        entry still be started on demand. Returns (vendor, params) or
        (None, None)."""
        for entry in self.vendor_roster():
            if entry.key.removesuffix("_1") == bus:
                return entry.vendor, entry.vendor_params
        return None, None

    def start_stream(self, req: StreamRequest) -> StreamResult:
        adapter = self.registry.get(req.vendor)
        key = f"{req.bus}_{req.cam}"

        src = adapter.resolve_live_source(req)

        if src.kind == SourceKind.EMBED:
            # Deliberately NOT cached as active: this is what sumithlive
            # falls back to when its HLS probe fails, and camera
            # availability flips within seconds — caching it would mask a
            # channel that's actually live again by the next request.
            return StreamResult(key=key, kind=src.kind, embed_url=src.embed_url)

        # HLS or FLV: both a direct, playable URL worth caching as "active"
        # so a later GET /api/stream/{id} skips re-triggering the vendor.
        with self._direct_lock:
            self._direct_active[key] = DirectEntry(
                key=key, kind=src.kind, url=src.url, has_audio=src.has_audio,
            )
        return StreamResult(key=key, kind=src.kind, url=src.url)

    def is_active(self, key: str) -> bool:
        with self._direct_lock:
            return key in self._direct_active

    def start_any(self, req: StreamRequest) -> StreamResult:
        key = f"{req.bus}_{req.cam}"
        if self.is_active(key):
            raise ErrAlreadyRunning(key)
        return self.start_stream(req)

    def stop_stream(self, key: str) -> bool:
        """Drops a tracked direct session and releases the owning adapter's
        capacity slot (e.g. chemitoapi's per-account channel limit), if it
        has one."""
        with self._direct_lock:
            was_active = self._direct_active.pop(key, None) is not None

        for adapter in self.registry.all():
            release = getattr(adapter, "release", None)
            if callable(release):
                release(key)

        return was_active

    def active_direct_keys(self) -> list:
        """For GET /api/fleet to merge alongside buses that a vendor account
        reports knowing about."""
        with self._direct_lock:
            return list(self._direct_active.values())

    def direct_entry_for(self, key: str):
        with self._direct_lock:
            return self._direct_active.get(key)

    def list_cameras(self, vendor: str, vendor_params: dict) -> list:
        return self.registry.get(vendor).list_cameras(vendor_params)
