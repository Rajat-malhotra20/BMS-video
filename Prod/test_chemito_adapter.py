"""Run: cd Prod && python test_chemito_adapter.py — no network, fake vendor."""

from domain import StreamRequest, VendorError
from vendorclients import chemitoapi as raw
from vendors import chemitoapi_adapter as mod

PORTS = [22060, 22061, 22062, 22063]


class FakeClient:
    logins = 0
    dead_keys = set()
    fail_terid = None

    def __init__(self, base_url):
        self.key = ""

    def login(self, u, p):
        FakeClient.logins += 1
        self.key = f"k{FakeClient.logins}"
        return self.key

    def use_key(self, key):
        self.key = key

    def live_ports(self):
        if self.key in FakeClient.dead_keys:
            raise raw.APIError(204)
        return PORTS

    def live_video_url(self, terid, chl, audio, st, port):
        if terid == FakeClient.fail_terid:
            raise raw.APIError(401)
        return f"https://gw:{port}/live.flv?devid={terid}&chl={chl}"


def expire_cooling(a):
    """Pretends VENDOR_LINGER has passed for every released slot."""
    for k, (t, p) in list(a._cooling.items()):
        a._cooling[k] = (t - mod.VENDOR_LINGER - 1, p)


def req(bus, cam):
    return StreamRequest(bus=bus, vendor="chemitoapi", cam=cam, vendor_params={"terid": bus})


def demo():
    mod.rawclient.Client = FakeClient
    a = mod.Adapter("https://gw", "u", "p")

    # 16 channels spread 4 per port; the 17th is refused.
    urls = [a.resolve_live_source(req(f"B{b}", c)).url for b in range(4) for c in range(1, 5)]
    for p in PORTS:
        assert sum(f":{p}/" in u for u in urls) == 4, urls
    try:
        a.resolve_live_source(req("B9", 1))
        raise AssertionError("17th channel should hit capacity_limit")
    except VendorError as e:
        assert e.code == "capacity_limit"

    # Re-resolving a held channel keeps its port and doesn't need a free one.
    assert a.resolve_live_source(req("B0", 1)).url == urls[0]

    # A released bus keeps its slots while the vendor still holds them...
    for c in range(1, 5):
        a.release(f"B0_{c}")
    try:
        a.resolve_live_source(req("B9", 1))
        raise AssertionError("cooling slots must still count")
    except VendorError as e:
        assert e.code == "capacity_limit"
    # ...but going back to that same bus reuses them, on the same ports.
    assert a.resolve_live_source(req("B0", 2)).url == urls[1]
    a.release("B0_2")

    # Once the vendor has let go, the next bus gets them.
    expire_cooling(a)
    assert all(a.resolve_live_source(req("B9", c)).url for c in range(1, 5))

    # A failed resolve doesn't keep a slot, not even a cooling one.
    for c in range(1, 5):
        a.release(f"B9_{c}")
    expire_cooling(a)
    FakeClient.fail_terid = "BAD"
    try:
        a.resolve_live_source(req("BAD", 1))
        raise AssertionError("expected vendor error")
    except VendorError as e:
        assert e.code != "capacity_limit"
    assert "BAD_1" not in a._active and "BAD_1" not in a._cooling
    FakeClient.fail_terid = None

    # Expired key -> exactly one re-login, then success.
    FakeClient.dead_keys.add(a._key)
    before = FakeClient.logins
    assert a.resolve_live_source(req("B9", 1)).url
    assert FakeClient.logins == before + 1
    print("ok")


def demo_sub_main():
    """Grid asks sub, fullscreen asks main: neither may be served the other's cached URL."""
    import api
    from domain import LiveSource
    from services.stream_service import StreamService
    from vendors.registry import Registry

    class Cam:
        vendor_id, label, online, channels, vendor_params = "BUS1", "BUS1", True, 2, {"terid": "T1"}

    class V:
        calls = []
        def name(self): return "chemitoapi"
        def list_cameras(self, _): return [Cam()]
        def resolve_live_source(self, req):
            V.calls.append((req.cam, req.main, req.audio))
            return LiveSource(kind="flv", url=f"u{req.cam}{'m' if req.main else 's'}")

    app = api.App(StreamService(Registry(V())), {}, "", "*")
    grid = app.ensure_all_cams("BUS1", main=False)
    assert [g["directUrl"] for g in grid] == ["u1s", "u2s"] and V.calls == [(1, False, False), (2, False, False)]
    # Vendor URLs are single-use: asking again re-resolves, never serves a stored one.
    assert app.ensure_all_cams("BUS1", main=False) == grid and len(V.calls) == 4
    assert app.refresh_stream("BUS1", 1, main=True).url == "u1m" and V.calls[-1] == (1, True, True)
    print("ok sub/main")


def demo_bus_busy():
    """Every cam refused for capacity -> 503, not an empty 200; one cam up -> 200."""
    import api
    from services.stream_service import StreamService
    from vendors.registry import Registry

    class Cam:
        vendor_id, label, online, channels, vendor_params = "BUS1", "BUS1", True, 3, {"terid": "T1"}

    class V:
        refuse = {1, 2, 3}
        def name(self): return "chemitoapi"
        def list_cameras(self, _): return [Cam()]
        def resolve_live_source(self, req):
            if req.cam in V.refuse:
                raise VendorError("chemitoapi", "resolve live video url", code="capacity_limit")
            return LiveSource(kind="flv", url=f"u{req.cam}")

    from domain import LiveSource
    app = api.App(StreamService(Registry(V())), {}, "", "*")
    try:
        app.ensure_all_cams("BUS1", main=False)
        raise AssertionError("expected capacity_limit")
    except VendorError as e:
        assert e.code == "capacity_limit" and api.error_status(e) == 503
    V.refuse = {2, 3}
    assert [g["cam"] for g in app.ensure_all_cams("BUS1", main=False)] == [1]
    print("ok busy")


if __name__ == "__main__":
    demo()
    demo_sub_main()
    demo_bus_busy()
