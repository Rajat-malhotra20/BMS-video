"""Go client for Chemito's "CM Server Application" HTTP API
(docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx). Ported from
prototype/backend/vendorclients/chemitoapi/client.go. Only the live-video
path is implemented — GPS/alarm interfaces are documented but unused.
"""

import json
import urllib.error
import urllib.parse
import urllib.request

# Appendix 1 of the doc, transcribed 2026-08-18 from
# docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx rows 159-190.
ERROR_CODE_DESCRIPTIONS = {
    200: "Request successful / Response successful",
    201: "Illegal request",
    202: "Server Error",
    203: "No authority",
    204: "Authorization expired",
    205: "Account has expired",
    206: "Username and password are incorrect",
    207: "Request parameter number exception",
    208: "Request format error",
    209: "Unauthorized key detected",
    210: "Authorization key error",
    211: "MD5 error",
    212: "No data",
    213: "No device",
    214: "No space",
    215: "No file",
    216: "Request parameter content does not meet the restrictions",
    217: "User logged in",
    218: "Account lockout",
    219: "Password expiration",
    220: "Low password strength",
    224: "authorization_expired",
    225: "api_unauthorized",
    226: "device_limit",
    300: "Database connection error",
    301: "Database operation exception",
    302: "Internal interface parameter number error",
    400: "Terminal search video calendar fail",
    401: "Terminal is not Online",
    402: "The terminal retrieval service is busy",
    403: "Terminal execution fail",
}


class APIError(Exception):
    """Returned when the server responds with a non-success errorcode."""

    def __init__(self, code: int, cause: str = ""):
        self.code = code
        self.cause = cause
        desc = cause or ERROR_CODE_DESCRIPTIONS.get(code, "")
        msg = f"chemitoapi: errorcode={code}" + (f": {desc}" if desc else "")
        super().__init__(msg)


LIVE_STREAM_MAIN = 0
LIVE_STREAM_SUB = 1


class Client:
    """Talks to a single Chemito CM server instance, e.g.
    "http://15.252.130.159:12056" (plain HTTP, not TLS, per the doc)."""

    def __init__(self, base_url: str, timeout: float = 15.0):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.key = ""

    def _do(self, url: str, method: str = "GET", body: bytes = None, headers: dict = None) -> dict:
        req = urllib.request.Request(url, data=body, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"chemitoapi: http {e.code}: {e.read().decode(errors='replace')}") from e
        except urllib.error.URLError as e:
            raise RuntimeError(f"chemitoapi: request failed: {e}") from e

        try:
            env = json.loads(raw)
        except json.JSONDecodeError as e:
            raise RuntimeError(f"chemitoapi: decode response: {e} (body={raw!r})") from e

        code = env.get("errorcode", 0)
        if code != 200 and code != 0:
            raise APIError(code)
        return env.get("data")

    def login(self, username: str, password: str) -> str:
        """POST /api/v1/basic/key — body {"username","password"}."""
        body = json.dumps({"username": username, "password": password}).encode()
        data = self._do(
            f"{self.base_url}/api/v1/basic/key",
            method="POST",
            body=body,
            headers={"Content-Type": "application/json"},
        )
        self.key = (data or {}).get("key", "")
        return self.key

    def use_key(self, key: str) -> None:
        """Adopts an already-issued verify key instead of logging in again.

        The server keeps ONE active session per account: a second login
        invalidates the first key, and every stream token minted from it
        dies with it — confirmed live 2026-08-26, six channels opened at
        once each with its own login left only the last one playing.
        Callers must log in once and pass the key around.
        """
        self.key = key

    def live_ports(self) -> list:
        """GET /api/v1/basic/live/port — the connectable relay ports.
        Requires the "key" query param; omitting it returns an empty list
        rather than an auth error.
        """
        q = urllib.parse.urlencode({"key": self.key})
        data = self._do(f"{self.base_url}/api/v1/basic/live/port?{q}")
        return [p["port"] for p in (data or [])]

    def list_devices(self) -> list:
        """GET /api/v1/basic/devices — every device on this account.
        Undocumented in the xlsx, confirmed live 2026-08-18.
        """
        q = urllib.parse.urlencode({"key": self.key})
        data = self._do(
            f"{self.base_url}/api/v1/basic/devices?{q}",
            headers={"Content-Type": "application/json"},
        )
        return data or []

    def live_video_url(self, terid: str, channel: int, audio: bool, stream_type: int, port: int) -> str:
        """GET /api/v1/basic/live/video — the playable FLV URL for one
        device channel (key travels as a query param here, unlike castmaster)."""
        q = urllib.parse.urlencode({
            "key": self.key,
            "terid": terid,
            "chl": channel,
            "audio": "1" if audio else "0",
            "st": stream_type,
            "port": port,
        })
        data = self._do(f"{self.base_url}/api/v1/basic/live/video?{q}")
        return (data or {}).get("url", "")
