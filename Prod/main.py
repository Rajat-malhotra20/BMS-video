"""Entry point. Ported from prototype/backend/main.go, minus the MediaMTX
reverse proxy and vendor-startup complexity that only mattered for the FLV
byte-hub Prod doesn't have (see api.py's module docstring). Every stream
this process serves is a direct vendor URL — Chemito's HTTP-FLV, Sumith's
HLS/embed — handed straight to the frontend, never proxied.

Run:
    cd Prod
    cp config/vendors.json.example config/vendors.json   # fill in real creds
    cp config/buses.json.example config/buses.json        # optional
    python main.py
"""

import os
import threading
import time

import settings
from api import App, serve
from domain import StreamRequest
from services.stream_service import StreamService
from vendors.chemitoapi_adapter import Adapter as ChemitoAdapter
from vendors.registry import Registry
from vendors.sumithlive_adapter import Adapter as SumithliveAdapter

# How many channels get auto-started per bus at boot, regardless of what the
# vendor's own device-count reports — a DVR's reported channel count is how
# many camera inputs it has wired, not how many it can stream simultaneously.
STARTUP_AUTO_START_CHANNELS_PER_BUS = 4


def env(key: str, fallback: str) -> str:
    return os.environ.get(key, "").strip() or fallback


def run_startup_auto_start(stream: StreamService, buses: dict):
    """Runs once at boot so cams are already live by the time anyone looks.
    Not a recurring poll: Chemito has a small real concurrent-session
    capacity, and re-triggering it for channels nobody's watching burns
    through that budget for nothing."""
    seen = set()

    def start_bus_channels(bus_id, vendor, params, n):
        for cam in range(1, n + 1):
            try:
                stream.start_stream(StreamRequest(bus=bus_id, cam=cam, vendor=vendor, main=True, audio=True, vendor_params=params))
            except Exception as e:
                print(f"startup-auto-start: {bus_id} cam {cam}: {e}")
            time.sleep(2)

    threads = []
    for entry in stream.vendor_roster():
        bus_id = entry.key.removesuffix("_1")
        seen.add(bus_id)
        n = entry.channels or STARTUP_AUTO_START_CHANNELS_PER_BUS
        n = min(n, STARTUP_AUTO_START_CHANNELS_PER_BUS)
        t = threading.Thread(target=start_bus_channels, args=(bus_id, entry.vendor, entry.vendor_params, n), daemon=True)
        t.start()
        threads.append(t)

    # buses.json is only a fallback, for vendors that can't be listed —
    # anything the roster already found is skipped.
    for bus_id, cfg in buses.items():
        if bus_id in seen:
            continue
        t = threading.Thread(target=start_bus_channels, args=(bus_id, cfg.vendor, cfg.vendor_params, STARTUP_AUTO_START_CHANNELS_PER_BUS), daemon=True)
        t.start()
        threads.append(t)

    for t in threads:
        t.join()
    print("startup-auto-start: done")


def main():
    addr = env("ADDR", "0.0.0.0")
    port = int(env("PORT", "8080"))
    cors_origin = env("CORS_ALLOWED_ORIGIN", "*")
    api_token = env("API_TOKEN", "")
    vendors_config = env("VENDORS_CONFIG", "config/vendors.json")
    buses_config = env("BUSES_CONFIG", "config/buses.json")

    if not api_token:
        print("WARNING: API_TOKEN is not set - this API is UNAUTHENTICATED. "
              "Anyone who can reach it can view every camera and open vendor sessions. "
              "Set API_TOKEN before exposing it beyond a trusted network.")

    try:
        vendor_accounts = settings.load_vendors(vendors_config)
    except Exception as e:
        print(f"bridge: {e} (no vendor accounts configured)")
        vendor_accounts = {}
    try:
        buses = settings.load_buses(buses_config)
    except Exception as e:
        print(f"bridge: {e} (no buses configured)")
        buses = {}

    chemito_acct = vendor_accounts.get("chemitoapi", settings.VendorAccount())
    sumith_acct = vendor_accounts.get("sumithlive", settings.VendorAccount())

    registry = Registry(
        ChemitoAdapter(chemito_acct.base_url, chemito_acct.username, chemito_acct.password),
        SumithliveAdapter(sumith_acct.base_url, sumith_acct.username, sumith_acct.password,
                           default_project_id=sumith_acct.extra.get("projectId", "")),
    )
    stream = StreamService(registry)
    app = App(stream, buses, api_token, cors_origin)

    threading.Thread(target=run_startup_auto_start, args=(stream, buses), daemon=True).start()

    serve(app, addr, port)


if __name__ == "__main__":
    main()
