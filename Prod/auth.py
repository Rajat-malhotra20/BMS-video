"""Shared bearer-token gate. Ported from prototype/backend/auth.go.

The token travels either as "Authorization: Bearer <token>" or as a
?token= query parameter — a browser playing HLS through a plain <video src>
can't attach a header, so a header-only scheme would make those cameras
unplayable.
"""

import hmac


def check_token(want_token: str, auth_header: str, query_token: str) -> bool:
    """An empty want_token means auth is off (caller should warn at boot)."""
    if not want_token:
        return True
    got = auth_header[len("Bearer "):] if auth_header.startswith("Bearer ") else ""
    if not got:
        got = query_token or ""
    # Constant-time compare: a length/short-circuit compare leaks the token
    # a character at a time to anyone who can measure it.
    return hmac.compare_digest(got, want_token)
