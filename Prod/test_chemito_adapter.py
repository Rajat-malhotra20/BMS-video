"""Run: cd Prod && python test_chemito_adapter.py — no network, fake vendor."""

from domain import StreamRequest, VendorError
from vendorclients import chemitoapi as raw
from vendors import chemitoapi_adapter as mod

PORTS = [22060, 22061, 22062, 22063]
REAL_CLIENT = raw.Client  # demo() swaps the module's Client for FakeClient


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

    port_calls = 0

    def live_ports(self):
        FakeClient.port_calls += 1
        if self.key in FakeClient.dead_keys:
            raise raw.APIError(204)
        return PORTS

    def live_video_url(self, terid, chl, audio, st, port):
        if self.key in FakeClient.dead_keys:
            raise raw.APIError(204)
        if terid == FakeClient.fail_terid:
            raise raw.APIError(401)
        return f"https://gw:{port}/live.flv?devid={terid}&chl={chl}"


def expire_cooling(a):
    """Pretends VENDOR_LINGER has passed for every released slot, on every account."""
    for acct in a._accounts:
        for k, (t, p) in list(acct._cooling.items()):
            acct._cooling[k] = (t - mod.VENDOR_LINGER - 1, p)


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
    assert not a._accounts[0].holds("BAD_1")
    FakeClient.fail_terid = None

    # Expired key -> exactly one re-login, then success.
    FakeClient.dead_keys.add(a._accounts[0]._key)
    before = FakeClient.logins
    assert a.resolve_live_source(req("B9", 1)).url
    assert FakeClient.logins == before + 1
    print("ok")


def demo_accounts():
    """Account 1 full -> next cam goes to account 2, then 3; a cam stays on its
    account; freed room on account 1 is used first again; all full -> 503."""
    mod.rawclient.Client = FakeClient
    a = mod.Adapter("https://gw", "u1", "p", [("u2", "p"), ("u3", "p")])
    one, two, three = a._accounts
    on = lambda key: [acct.username for acct in a._accounts if key in acct._active]

    for c in range(1, 17):
        a.resolve_live_source(req("A", c))
    assert one._counts() == (16, 0) and two._counts() == (0, 0)
    a.resolve_live_source(req("B", 1))                     # 17th channel
    assert on("B_1") == ["u2"]
    for c in range(2, 18):
        a.resolve_live_source(req("B", c))                 # fills account 2 (16), B_17 -> account 3
    assert two._counts() == (16, 0) and on("B_17") == ["u3"]
    assert len({acct._key for acct in a._accounts}) == 3   # one login per account

    a.release("A_1"); expire_cooling(a)                    # room on account 1 again
    a.resolve_live_source(req("B", 1))                     # renewal stays on account 2
    assert on("B_1") == ["u2"]
    a.resolve_live_source(req("C", 1))                     # a new cam takes account 1's free slot
    assert on("C_1") == ["u1"]

    a.release("B_17")                                      # stop-bus reaches account 3 too
    assert three._counts() == (0, 1)
    a.resolve_live_source(req("B", 17))                    # back to it within the cooldown: same account
    assert on("B_17") == ["u3"]

    for c in range(1, 16):
        a.resolve_live_source(req("D", c))                 # account 3: 15 more = 16
    try:
        a.resolve_live_source(req("X", 1))
        raise AssertionError("every account full should hit capacity_limit")
    except VendorError as e:
        assert e.code == "capacity_limit"
    assert on("X_1") == []
    print("ok accounts")


def demo_ports_cache():
    """Ports fetched once per account, not per camera; full accounts are skipped
    with no vendor call; a failed link drops that account's cache."""
    mod.rawclient.Client = FakeClient
    a = mod.Adapter("https://gw", "u1", "p", [("u2", "p"), ("u3", "p")])
    FakeClient.port_calls = 0
    for c in range(1, 33):
        a.resolve_live_source(req("A", c))   # fills accounts 1 and 2
    assert FakeClient.port_calls == 2, FakeClient.port_calls
    for c in range(1, 11):
        a.resolve_live_source(req("B", c))   # account 3; 1 and 2 skipped from memory
    assert FakeClient.port_calls == 3, FakeClient.port_calls

    FakeClient.fail_terid = "BAD"
    try:
        a.resolve_live_source(req("BAD", 1))
    except VendorError:
        pass
    FakeClient.fail_terid = None
    a.resolve_live_source(req("B", 11))
    assert FakeClient.port_calls == 4, FakeClient.port_calls  # account 3 refetched once
    print("ok ports cache")


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

    # A camera's own Stop releases just that camera; stop-bus the rest.
    assert app.stop_bus("BUS1", cam=1) == ["BUS1_1"]
    assert [c for c, _ in app.cams_for_bus("BUS1")] == [2]
    assert app.stop_bus("BUS1") == ["BUS1_2"] and app.cams_for_bus("BUS1") == []
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


def demo_history_paths():
    """§8.3/8.4/8.5 request paths and params, exactly as the vendor doc spells them."""
    from urllib.parse import parse_qs, urlsplit
    seen = []
    c = REAL_CLIENT("https://gw:22056")
    c.key = "K"
    c._env = lambda url, *a, **k: seen.append(url) or {"data": [{"url": "u"}], "sn": "S"}
    assert c.server_flv("T", "2026-10-01 09:00:00", 2) == ("u", "S")
    assert c.record_flv("T", "2026-10-01 09:00:00", 2, 1) == ("u", "S")
    c.record_hls("T", "2026-10-01 09:00:00", "2026-10-01 09:01:00", 2, 0)
    got = [(urlsplit(u).path, {k: v[0] for k, v in parse_qs(urlsplit(u).query).items()}) for u in seen]
    assert got == [
        ("/api/v1/basic/server/video", {"key": "K", "terid": "T", "starttime": "2026-10-01 09:00:00", "chl": "2"}),
        ("/api/v1/basic/record/flv/video", {"key": "K", "terid": "T", "starttime": "2026-10-01 09:00:00", "chl": "2", "st": "1"}),
        ("/api/v1/basic/record/video", {"key": "K", "terid": "T", "starttime": "2026-10-01 09:00:00",
                                        "endtime": "2026-10-01 09:01:00", "chl": "2", "st": "0"}),
    ], got
    print("ok history paths")


if __name__ == "__main__":
    demo()
    demo_accounts()
    demo_ports_cache()
    demo_sub_main()
    demo_bus_busy()
    demo_history_paths()
