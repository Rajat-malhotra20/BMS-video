"""Adapts vendorclients.sumithlive to the vendors.Adapter contract. Ported
from prototype/backend/vendors/sumithlive/adapter.go — reference shape for
any HTTP+direct-URL vendor; chemitoapi_adapter mirrors it.
"""

import threading
import time

from domain import Camera, LiveSource, SourceKind, StreamRequest, VendorError
from vendorclients import sumithlive as rawclient

TOKEN_TTL = 10 * 60  # vendor documents no expiry; conservative refresh


class Adapter:
    def __init__(self, base_url: str, username: str, password: str, default_project_id: str = ""):
        self.base_url = base_url
        self.username = username
        self.password = password
        self.default_project_id = default_project_id

        self._token_lock = threading.Lock()
        self._token = ""
        self._token_at = 0.0

    def name(self) -> str:
        return "sumithlive"

    def _access_token(self):
        """Returns (token, reused). One token for the whole process rather
        than one per call — opening a grid of cameras would otherwise fire
        one login per tile at the same instant."""
        with self._token_lock:
            if self._token and (time.monotonic() - self._token_at) < TOKEN_TTL:
                return self._token, True
            client = rawclient.Client(self.base_url)
            try:
                token = client.get_access_token(self.username, self.password)
            except Exception as e:
                raise VendorError("sumithlive", "login", cause=e) from e
            self._token, self._token_at = token, time.monotonic()
            return token, False

    def _forget_token(self) -> None:
        with self._token_lock:
            self._token = ""

    def resolve_live_source(self, req: StreamRequest) -> LiveSource:
        """Logs in and calls getLiveStreamingLink (this actually tells the
        device to start streaming, not a passive lookup). The documented
        result is jspLink, an embeddable page — but that page just renders a
        fixed 4-camera grid by fetching direct per-camera HLS URLs. Try the
        real HLS URL first; if the probe fails, fall back to the embed page.
        """
        client = rawclient.Client(self.base_url)
        token, reused = self._access_token()

        plate_no = req.vendor_params.get("plateNo", "")
        channel = req.cam or 1
        project_id = _atoi_or(req.vendor_params.get("projectId", ""), _atoi_or(self.default_project_id, 0))

        try:
            link = client.get_live_streaming_link(token, plate_no, channel, project_id)
        except Exception as e:
            # Only retry when the token came from the cache: a freshly
            # minted one that fails failed for a real reason (device
            # offline, bad plate).
            if not reused:
                raise VendorError("sumithlive", "resolve live streaming link", cause=e) from e
            self._forget_token()
            try:
                token, _ = self._access_token()
                link = client.get_live_streaming_link(token, plate_no, channel, project_id)
            except Exception as e2:
                raise VendorError("sumithlive", "resolve live streaming link", cause=e2) from e2

        try:
            device_id = rawclient.decode_device_id(link)
            hls_url = client.hls_url(device_id, channel)
            if client.probe_live(hls_url):
                return LiveSource(kind=SourceKind.HLS, url=hls_url)
        except Exception:
            pass  # fall through to the embed page

        return LiveSource(kind=SourceKind.EMBED, embed_url=link)

    def list_cameras(self, vendor_params: dict) -> list:
        client = rawclient.Client(self.base_url)
        try:
            vehicles = client.list_vehicles(self.username, self.password)
        except Exception as e:
            raise VendorError("sumithlive", "list vehicles", cause=e) from e

        cams = []
        for v in vehicles:
            lat = _atof_or(v.get("latitude", ""), 0.0)
            lng = _atof_or(v.get("longitude", ""), 0.0)
            plate = v.get("plate_no", "")
            cams.append(Camera(
                vendor_id=plate,
                label=plate,
                online=(lat != 0 or lng != 0),
                vendor_params={"plateNo": plate, "projectId": self.default_project_id},
            ))
        return cams


def _atoi_or(s: str, default: int) -> int:
    try:
        return int(s) if s else default
    except ValueError:
        return default


def _atof_or(s: str, default: float) -> float:
    try:
        return float(s) if s else default
    except ValueError:
        return default
