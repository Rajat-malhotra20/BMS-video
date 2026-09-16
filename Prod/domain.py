"""Vendor-agnostic types every adapter produces and every handler/service
consumes. Mirrors prototype/backend/domain/*.go, minus RemuxInput/Upstream:
Prod only serves vendors that hand back a directly playable URL (Chemito,
Sumith), so there is no remux job and no per-connect resolver to carry.
"""

from dataclasses import dataclass, field
from typing import Optional


class SourceKind:
    EMBED = "embed"  # opaque URL; frontend iframes it directly
    HLS = "hls"       # direct, CORS-open .m3u8 playlist; frontend plays it straight
    FLV = "flv"       # vendor HTTP-FLV; frontend (mpegts.js) connects to the vendor's own URL directly


@dataclass
class LiveSource:
    """What every vendor adapter must resolve a request into. url is the raw
    vendor URL handed straight to the frontend — no proxy, no remux."""
    kind: str
    embed_url: str = ""
    url: str = ""
    has_audio: bool = False


@dataclass
class StreamRequest:
    bus: str
    vendor: str
    cam: int
    main: bool = True
    audio: bool = True
    vendor_params: dict = field(default_factory=dict)


@dataclass
class StreamResult:
    key: str
    kind: str
    embed_url: str = ""
    url: str = ""

    def to_json(self) -> dict:
        out = {"key": self.key, "kind": self.kind}
        if self.embed_url:
            out["embedUrl"] = self.embed_url
        if self.url:
            # "hlsUrl" to match the Go API's field name exactly (it carries
            # the direct URL for both hls and flv kinds — see domain.go's
            # own comment on why it kept that name after flv was added).
            out["hlsUrl"] = self.url
        return out


@dataclass
class Camera:
    vendor_id: str
    label: str = ""
    vendor_params: dict = field(default_factory=dict)
    online: bool = False
    channels: int = 0


class VendorError(Exception):
    """Normalizes a failure from any adapter so the HTTP layer can map it to
    a status code in one place instead of string-matching errors."""

    def __init__(self, vendor: str, op: str, code: str = "", retryable: bool = False, cause: Optional[BaseException] = None):
        self.vendor = vendor
        self.op = op
        self.code = code
        self.retryable = retryable
        self.cause = cause
        detail = str(cause) if cause is not None else code
        super().__init__(f"{vendor}: {op}: {detail}")


class ErrAlreadyRunning(Exception):
    def __init__(self, key: str):
        self.key = key
        super().__init__(f'stream "{key}" already running')
