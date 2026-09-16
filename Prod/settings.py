"""Loads the two static config files that let the frontend call the bridge
without ever naming a vendor: vendors.json (per-vendor account credentials)
and buses.json (per-bus vendor assignment). Mirrors
prototype/backend/config/*.go.
"""

import json
import os
from dataclasses import dataclass, field


@dataclass
class VendorAccount:
    base_url: str = ""
    username: str = ""
    password: str = ""
    extra: dict = field(default_factory=dict)


@dataclass
class Bus:
    vendor: str = ""
    vendor_params: dict = field(default_factory=dict)


def load_vendors(path: str) -> dict:
    """{"chemitoapi": {"baseUrl":..., "username":..., "password":...}, ...}.
    A password left empty in the file is filled from
    VENDOR_<NAME>_PASSWORD (upper-cased) so secrets don't need to live in a
    checked-in file.
    """
    with open(path, "r", encoding="utf-8") as f:
        raw = json.load(f)

    vendors = {}
    for name, acct in raw.items():
        password = acct.get("password", "")
        if not password:
            password = os.environ.get(f"VENDOR_{name.upper()}_PASSWORD", "")
        vendors[name] = VendorAccount(
            base_url=acct.get("baseUrl", ""),
            username=acct.get("username", ""),
            password=password,
            extra=acct.get("extra", {}) or {},
        )
    return vendors


def load_buses(path: str) -> dict:
    """{"DL1PC0001": {"vendor": "chemitoapi", "vendorParams": {"terid": "..."}}}."""
    with open(path, "r", encoding="utf-8") as f:
        raw = json.load(f)
    return {
        bus_id: Bus(vendor=cfg.get("vendor", ""), vendor_params=cfg.get("vendorParams", {}) or {})
        for bus_id, cfg in raw.items()
    }
