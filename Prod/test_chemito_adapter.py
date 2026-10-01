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

    # Releasing one bus frees its slots for the next bus.
    for c in range(1, 5):
        a.release(f"B0_{c}")
    assert all(a.resolve_live_source(req("B9", c)).url for c in range(1, 5))

    # A failed resolve doesn't keep a slot.
    for c in range(1, 5):
        a.release(f"B9_{c}")
    FakeClient.fail_terid = "BAD"
    try:
        a.resolve_live_source(req("BAD", 1))
    except VendorError:
        pass
    assert "BAD_1" not in a._active

    # Expired key -> exactly one re-login, then success.
    FakeClient.dead_keys.add(a._key)
    before = FakeClient.logins
    assert a.resolve_live_source(req("B9", 1)).url
    assert FakeClient.logins == before + 1
    print("ok")


if __name__ == "__main__":
    demo()
