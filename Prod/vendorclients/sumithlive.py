"""Sumith/Trakzee "Live Camera Streaming" HTTP API (trakzee2.uffizio.com).
Ported from prototype/backend/vendorclients/sumithlive/client.go. The
documented call (getLiveStreamingLink) only returns a jspLink page URL, but
that page just renders per-camera HLS URLs — reverse-engineered, not in the
vendor's published PDF; see decode_device_id/hls_url/probe_live below.
"""

import base64
import json
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_BASE_URL = "https://trakzee2.uffizio.com"
# NOT documented anywhere, and not necessarily the same for every tenant —
# reverse-engineered from one real account's network trace.
DEFAULT_HLS_BASE = "https://rtmpvideo.uffizio.com/hls"

PROBE_TIMEOUT = 5.0


class Client:
    def __init__(self, base_url: str = "", hls_base: str = "", timeout: float = 15.0):
        self.base_url = (base_url or DEFAULT_BASE_URL).rstrip("/")
        self.hls_base = (hls_base or DEFAULT_HLS_BASE).rstrip("/")
        self.timeout = timeout

    def _post(self, token: str, auth_code: str, body: dict) -> dict:
        raw = json.dumps(body).encode()
        url = f"{self.base_url}/webservice?token={token}"
        headers = {"Content-Type": "application/json"}
        if auth_code:
            headers["auth-code"] = auth_code
        req = urllib.request.Request(url, data=raw, method="POST", headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = resp.read()
        except urllib.error.URLError as e:
            raise RuntimeError(f"sumithlive: request {token}: {e}") from e
        try:
            return json.loads(data)
        except json.JSONDecodeError as e:
            raise RuntimeError(f"sumithlive: decode {token} response: {e}") from e

    def get_access_token(self, username: str, password: str) -> str:
        """POST /webservice?token=generateAccessToken."""
        out = self._post("generateAccessToken", "", {"username": username, "password": password})
        token = (out.get("data") or {}).get("token", "")
        if out.get("result") != 1 or not token:
            raise RuntimeError(f"sumithlive: generateAccessToken did not return a token (result={out.get('result')})")
        return token

    def get_live_streaming_link(self, auth_token: str, plate_no: str, channel_id: int, project_id: int) -> str:
        """POST /webservice?token=getLiveStreamingLink."""
        out = self._post(
            "getLiveStreamingLink",
            auth_token,
            {"plate_no": plate_no, "channel_id": channel_id, "project_id": project_id},
        )
        link = out.get("jspLink", "")
        if out.get("result") != "success" or not link:
            raise RuntimeError(f"sumithlive: getLiveStreamingLink failed: {out.get('message', '')}")
        return link

    def list_vehicles(self, username: str, password: str) -> list:
        """GET /webservice?token=getUserWiseLiveData — the wider Uffizio
        Tracking API's plate-discovery call (not in the Sumith-branded PDF)."""
        q = urllib.parse.urlencode({"token": "getUserWiseLiveData", "user": username, "pass": password})
        try:
            with urllib.request.urlopen(f"{self.base_url}/webservice?{q}", timeout=self.timeout) as resp:
                data = resp.read()
        except urllib.error.URLError as e:
            raise RuntimeError(f"sumithlive: getUserWiseLiveData: {e}") from e
        try:
            return json.loads(data)
        except json.JSONDecodeError as e:
            raise RuntimeError(f"sumithlive: decode getUserWiseLiveData response: {e}") from e

    def hls_url(self, device_id: str, channel: int) -> str:
        """Direct per-camera HLS playlist URL for one channel (1-4 observed)."""
        return f"{self.hls_base}/{device_id}_cam{channel}.m3u8"

    def probe_live(self, hls_url: str) -> bool:
        """Reports whether hls_url is genuinely playable right now — not just
        that the .m3u8 itself responds. A dead camera's playlist can keep
        returning 200 indefinitely (a stale cached manifest) while every
        segment it references 404s, so this fetches the playlist body, finds
        the last referenced segment, and confirms that is fetchable too.
        """
        try:
            req = urllib.request.Request(hls_url)
            with urllib.request.urlopen(req, timeout=PROBE_TIMEOUT) as resp:
                if resp.status not in (200, 304):
                    return False
                body = resp.read(64 * 1024).decode(errors="replace")
        except (urllib.error.URLError, TimeoutError):
            return False

        last_segment = ""
        for line in body.splitlines():
            line = line.strip()
            if line and not line.startswith("#"):
                last_segment = line
        if not last_segment:
            return False

        seg_url = last_segment
        if not seg_url.startswith("http://") and not seg_url.startswith("https://"):
            seg_url = f"{self.hls_base}/{last_segment}"

        try:
            seg_req = urllib.request.Request(seg_url, method="HEAD")
            with urllib.request.urlopen(seg_req, timeout=PROBE_TIMEOUT) as seg_resp:
                return seg_resp.status == 200
        except (urllib.error.URLError, TimeoutError):
            return False


def decode_device_id(jsp_link: str) -> str:
    """Extracts the device IMEI from a jspLink's base64 "param" query value
    — a tilde-delimited string whose first field is the IMEI used in HLS URLs.
    """
    parsed = urllib.parse.urlparse(jsp_link)
    params = urllib.parse.parse_qs(parsed.query)
    param = (params.get("param") or [""])[0]
    if not param:
        raise ValueError("sumithlive: jspLink has no param query value")
    raw = base64.b64decode(param + "=" * (-len(param) % 4)).decode(errors="replace")
    fields = raw.split("~")
    if not fields or not fields[0]:
        raise ValueError("sumithlive: jspLink param has no device id field")
    return fields[0]
