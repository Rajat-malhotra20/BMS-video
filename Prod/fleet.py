"""Fleet summary: which buses/cams are live right now. Ported from
prototype/backend/fleet.go, minus the MediaMTX ingest path — every "path"
Prod knows about is a direct vendor session (see services.stream_service).
"""

import re
import threading
import time
from dataclasses import dataclass, field

# Two digits: a channel number isn't capped at 9 (Chemito's DVRs report 9
# today). No leading zero, so "_01" still doesn't parse.
BUS_PATH_RE = re.compile(r"^(?:.*/)?([A-Za-z0-9]+)_([1-9][0-9]?)$")

RECENTLY_SEEN_WINDOW = 10 * 60  # seconds


def parse_bus_path(name: str):
    """Returns (bus_id, cam, ok)."""
    m = BUS_PATH_RE.match(name)
    if not m:
        return None, None, False
    return m.group(1), int(m.group(2)), True


@dataclass
class FleetBus:
    id: str
    label: str = ""
    cams: list = field(default_factory=list)
    last_seen: int = 0
    cams_available: int = 0

    def to_json(self) -> dict:
        out = {"id": self.id, "cams": self.cams, "lastSeen": self.last_seen}
        if self.label:
            out["label"] = self.label
        if self.cams_available:
            out["camsAvailable"] = self.cams_available
        return out


@dataclass
class FleetSummary:
    buses: list = field(default_factory=list)
    buses_online: int = 0
    buses_seen: int = 0
    cams_online: int = 0
    updated_at: int = 0

    def to_json(self) -> dict:
        return {
            "buses": [b.to_json() for b in self.buses],
            "totals": {
                "busesOnline": self.buses_online,
                "busesSeen": self.buses_seen,
                "camsOnline": self.cams_online,
            },
            "updatedAt": self.updated_at,
        }


class FleetTracker:
    """Remembers when each bus was last seen so a recently-offline bus stays
    visible in the list for RECENTLY_SEEN_WINDOW."""

    def __init__(self):
        self._lock = threading.Lock()
        self._last_seen: dict[str, float] = {}

    def build(self, direct_keys: list) -> FleetSummary:
        now = time.time()
        with self._lock:
            online: dict[str, list] = {}
            for entry in direct_keys:
                bus_id, cam, ok = parse_bus_path(entry.key)
                if not ok:
                    continue
                online.setdefault(bus_id, []).append(cam)
                self._last_seen[bus_id] = now

            buses = []
            cams_online = 0
            for bus_id in list(self._last_seen.keys()):
                seen = self._last_seen[bus_id]
                if now - seen > RECENTLY_SEEN_WINDOW:
                    del self._last_seen[bus_id]
                    continue
                cams = sorted(online.get(bus_id, []))
                cams_online += len(cams)
                buses.append(FleetBus(id=bus_id, cams=cams, last_seen=int(seen)))

            buses.sort(key=lambda b: (int(b.id) if b.id.isdigit() else float("inf"), b.id))

            return FleetSummary(
                buses=buses,
                buses_online=len(online),
                buses_seen=len(buses),
                cams_online=cams_online,
                updated_at=int(now),
            )


def merge_roster(summary: FleetSummary, roster: list) -> FleetSummary:
    """Adds every vendor-roster bus the tracker didn't already include, so a
    bus the vendor reports knowing about always shows up — even one that's
    never streamed. Cams is left empty: a roster entry's online signal
    confirms the vehicle is live, not that a camera works on it."""
    have = {b.id for b in summary.buses}
    for entry in roster:
        bus_id, _, ok = parse_bus_path(entry.key)
        if not ok or bus_id in have:
            continue
        have.add(bus_id)
        if entry.online:
            summary.buses_online += 1
        summary.buses_seen += 1
        summary.buses.append(FleetBus(id=bus_id, label=entry.label, cams=[], last_seen=int(time.time())))

    summary.buses.sort(key=lambda b: (int(b.id) if b.id.isdigit() else float("inf"), b.id))
    return summary
