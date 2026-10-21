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

    e = app.panic_playback("BUS1", "2026-10-01 08:59:55", "2026-10-01 09:00:15")
    assert (e["start"], e["end"]) == ("2026-10-01 08:59:55", "2026-10-01 09:00:15"), e
    assert f.opened == [("T1", c, "2026-10-01 08:59:55") for c in (1, 2, 3)], f.opened
    assert [p["cam"] for p in e["cams"]] == [1, 2, 3]

    # Alarm system + browsers + repeated press within 10s -> same sessions.
    assert app.panic_playback("BUS1", "2026-10-01 09:00:02", "2026-10-01 09:00:22") is e and len(f.opened) == 3
    # A different window on the same bus is its own session set.
    other = app.panic_playback("BUS1", "2026-10-01 08:59:55", "2026-10-01 09:05:00")
    assert other is not e and len(f.opened) == 6

    # Sessions stop once the window has played (+ first-frame slack).
    assert [t.delay for t in timers] == [20 + api.PANIC_PLAY_SLACK, 305 + api.PANIC_PLAY_SLACK]
    timers[0].fn()
    assert f.stopped == [("stop", "sn1"), ("stop", "sn2"), ("stop", "sn3")], f.stopped

    # After expiry, a new call is a fresh replay.
    for v in app._panic.values():
        v["_expires"] = 0
    assert app.panic_playback("BUS1", "2026-10-01 08:59:55", "2026-10-01 09:00:15") is not e and len(f.opened) == 9

    # end must be after start.
    try:
        app.panic_playback("BUS1", "2026-10-01 09:00:15", "2026-10-01 09:00:15")
        raise AssertionError("expected ValueError")
    except ValueError:
        pass

    # One dead cam is skipped; all dead -> error and nothing cached.
    f.dead_cams = {2}
    e3 = app.panic_playback("BUS1", "2026-10-01 10:00:00", "2026-10-01 10:01:00")
    assert [p["cam"] for p in e3["cams"]] == [1, 3] and 2 in e3["errors"]
    f.dead_cams = {1, 2, 3}
    try:
        app.panic_playback("BUS1", "2026-10-01 11:00:00", "2026-10-01 11:01:00")
        raise AssertionError("expected no_recording")
    except VendorError as err:
        assert err.code == "no_recording"
    assert not any(b == "BUS1" and s.hour == 11 for b, s, _ in app._panic)
    print("ok")


def demo_http():
    """?start&end plays exactly that window; ?time still means press -10s..+10s."""
    import threading
    import urllib.request
    from http.server import ThreadingHTTPServer

    api.threading.Timer = CapturedTimer
    f = Fake()
    app = api.App(StreamService(Registry(f)), {}, "", "*")
    srv = ThreadingHTTPServer(("127.0.0.1", 0), api.make_handler(app))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{srv.server_port}/api/panic/BUS1"

    def post(q):
        try:
            with urllib.request.urlopen(urllib.request.Request(base + q, method="POST")) as r:
                return r.status, __import__("json").load(r)
        except urllib.error.HTTPError as err:
            return err.code, None

    status, body = post("?start=2026-10-01%2009:00:00&end=2026-10-01%2009:02:00&cam=2")
    assert status == 200 and (body["start"], body["end"], body["time"]) == ("2026-10-01 09:00:00", "2026-10-01 09:02:00", None)
    assert [p["cam"] for p in body["cams"]] == [2]
    status, body = post("?time=2026-10-01%2010:00:05")
    assert status == 200 and (body["start"], body["end"]) == ("2026-10-01 09:59:55", "2026-10-01 10:00:15")
    assert post("?start=2026-10-01%2009:00:00")[0] == 400  # end missing
    assert post("?start=2026-10-01%2009:02:00&end=2026-10-01%2009:00:00")[0] == 400
    srv.shutdown()
    print("ok http")


if __name__ == "__main__":
    demo()
    demo_http()
