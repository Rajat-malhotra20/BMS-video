"""Go client for Chemito's "CM Server Application" HTTP API
(docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx). Ported from
prototype/backend/vendorclients/chemitoapi/client.go. Live video (§7),
history video (§8) and video download (§9) are implemented — GPS/alarm
interfaces are documented but unused. Live API help: <base>/help/api/default.html.
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

    def _do(self, url: str, method: str = "GET", body: bytes = None, headers: dict = None):
        return self._env(url, method, body, headers).get("data")

    def _post(self, path: str, payload: dict) -> dict:
        """JSON POST with the key in the body (every POST in §8/§9 takes it there)."""
        body = json.dumps({"key": self.key, **payload}).encode()
        return self._env(f"{self.base_url}{path}", "POST", body, {"Content-Type": "application/json"})

    def _get(self, path: str, params: dict):
        q = urllib.parse.urlencode({"key": self.key, **params})
        return self._do(f"{self.base_url}{path}?{q}")

    def _env(self, url: str, method: str = "GET", body: bytes = None, headers: dict = None) -> dict:
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
        return env

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

    # ---- §8 history video (times are "YYYY-MM-DD HH:MM:SS"; st here is 1=main, 0=sub — the reverse of live) ----

    def record_files(self, terid: str, start: str, end: str, channels: list, stream_type: int) -> list:
        """GET /api/v1/basic/record/filelist — recorded segments on the device."""
        return self._get("/api/v1/basic/record/filelist", {
            "terid": terid, "starttime": start, "endtime": end,
            "chl": ",".join(str(c) for c in channels), "st": stream_type, "pbt": 0,
        }) or []

    def _flv(self, path: str, params: dict) -> tuple:
        env = self._env(f"{self.base_url}{path}?" + urllib.parse.urlencode({"key": self.key, **params}))
        data = env.get("data") or []
        url = (data[0] if isinstance(data, list) and data else data or {}).get("url", "")
        return url, env.get("sn", "")

    def record_flv(self, terid: str, start: str, channel: int, stream_type: int) -> tuple:
        """GET /api/v1/basic/record/flv/video (doc §8.4) — playback from the
        bus's own recorder as HTTP-FLV starting at `start`, streamed by the
        bus over 4G. Returns (url, sn); sn drives pause/resume/seek/stop."""
        return self._flv("/api/v1/basic/record/flv/video",
                         {"terid": terid, "starttime": start, "chl": channel, "st": stream_type})

    def server_flv(self, terid: str, start: str, channel: int) -> tuple:
        """GET /api/v1/basic/server/video (doc §8.5) — like record_flv, but
        from the copy stored on Chemito's server, so the bus needn't stream
        anything. Same (url, sn) and flv pause/resume/seek/stop (§8.6-8.9)."""
        return self._flv("/api/v1/basic/server/video", {"terid": terid, "starttime": start, "chl": channel})

    def record_hls(self, terid: str, start: str, end: str, channel: int, stream_type: int) -> str:
        """GET /api/v1/basic/record/video (doc §8.3) — HLS (m3u8) of exactly
        start..end on one channel from the bus's recorder, no session to stop."""
        data = self._get("/api/v1/basic/record/video", {
            "terid": terid, "starttime": start, "endtime": end, "chl": channel, "st": stream_type,
        })
        return (data[0] if isinstance(data, list) and data else data or {}).get("url", "")

    def flv_control(self, action: str, sn: str, start: str = "") -> None:
        """POST /api/v1/basic/flv/{pause,resume,seek,stop}."""
        payload = {"sn": sn}
        if action == "seek":
            payload["starttime"] = start
        self._post(f"/api/v1/basic/flv/{action}", payload)

    # ---- §9 video download (server pulls the recording off the device, then serves the file) ----

    def add_download_task(self, terid: str, start: str, end: str, channels: list, name: str, stream_type: int) -> int:
        """POST /api/v1/basic/record/task — tasktype 1 = video recording,
        netmode 7 = any network, kept 7 days."""
        env = self._post("/api/v1/basic/record/task", {
            "terid": terid, "starttime": start, "endtime": end, "chl": channels,
            "name": name[:15], "effective": 7, "netmode": 7, "tasktype": 1, "stream": stream_type,
        })
        return (env.get("data") or {}).get("taskid")

    def download_task_state(self, taskid: int, date: str) -> dict:
        """POST /api/v1/basic/record/taskstate — {percent, state, taskid}; state 3 = completed."""
        env = self._post("/api/v1/basic/record/taskstate", {"parms": [{"taskid": taskid, "date": date}]})
        rows = env.get("data") or []
        return rows[0] if rows else {}

    def download_task_files(self, taskid: int) -> list:
        """GET /api/v1/basic/record/taskfilelist — [{dir (base64), name}]."""
        return self._get("/api/v1/basic/record/taskfilelist", {"taskid": taskid, "tasktype": 1}) or []

    def open_download(self, dir_b64: str, name: str):
        """GET /api/v1/basic/record/download — raw file bytes. Returns the open
        response; caller streams it and closes it."""
        q = urllib.parse.urlencode({"key": self.key, "dir": dir_b64, "name": name})
        try:
            return urllib.request.urlopen(f"{self.base_url}/api/v1/basic/record/download?{q}", timeout=self.timeout)
        except urllib.error.URLError as e:
            raise RuntimeError(f"chemitoapi: download failed: {e}") from e
