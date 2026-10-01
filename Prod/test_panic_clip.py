"""Run: cd Prod && python test_panic_clip.py — no network, fake vendor, timers captured not run."""

import api
from domain import VendorError
from services.stream_service import StreamService
from vendors.registry import Registry


class Cam:
    vendor_id, label, online, channels, vendor_params = "BUS1", "BUS1", True, 3, {"terid": "T1"}


class Fake:
    def __init__(self):
        self.opened, self.stopped, self.dead_cams = [], [], set()

    def name(self): return "chemitoapi"
    def list_cameras(self, _): return [Cam()]

    def playback(self, params, cam, start, main=True):
        if cam in self.dead_cams:
            raise RuntimeError("no recording")
        self.opened.append((params["terid"], cam, start))
        return {"cam": cam, "kind": "flv", "directUrl": f"u{cam}", "sn": f"sn{len(self.opened)}"}

    def playback_control(self, action, sn, start=""):
        self.stopped.append((action, sn))


timers = []


class CapturedTimer:
    def __init__(self, delay, fn, args=()): self.delay, self.fn = delay, fn
    def start(self): timers.append(self)


def demo():
    api.threading.Timer = CapturedTimer
    f = Fake()
    app = api.App(StreamService(Registry(f)), {}, "", "*")

    e = app.panic_playback("BUS1", "2026-10-01 09:00:05")
    assert (e["start"], e["end"]) == ("2026-10-01 08:59:55", "2026-10-01 09:00:15"), e
    assert f.opened == [("T1", c, "2026-10-01 08:59:55") for c in (1, 2, 3)], f.opened
    assert [p["cam"] for p in e["cams"]] == [1, 2, 3]

    # Alarm system + browsers + repeated press within 10s -> same sessions.
    assert app.panic_playback("BUS1", "2026-10-01 09:00:12") is e and len(f.opened) == 3

    # Backstop stop closes every session.
    assert len(timers) == 1 and timers[0].delay == api.PANIC_PLAY_SECONDS
    timers[0].fn()
    assert f.stopped == [("stop", "sn1"), ("stop", "sn2"), ("stop", "sn3")], f.stopped

    # After expiry, a new call is a fresh replay.
    for v in app._panic.values():
        v["_expires"] = 0
    assert app.panic_playback("BUS1", "2026-10-01 09:00:05") is not e and len(f.opened) == 6

    # One dead cam is skipped; all dead -> error and nothing cached.
    f.dead_cams = {2}
    e3 = app.panic_playback("BUS1", "2026-10-01 10:00:00")
    assert [p["cam"] for p in e3["cams"]] == [1, 3] and 2 in e3["errors"]
    f.dead_cams = {1, 2, 3}
    try:
        app.panic_playback("BUS1", "2026-10-01 11:00:00")
        raise AssertionError("expected no_recording")
    except VendorError as err:
        assert err.code == "no_recording"
    assert not any(b == "BUS1" and t.hour == 11 for b, t in app._panic)
    print("ok")


if __name__ == "__main__":
    demo()
