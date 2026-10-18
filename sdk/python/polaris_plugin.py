"""Polaris plugin SDK for Python 3.8+. Standard library only."""
import base64
import hashlib
import hmac
import json
import re
import time
import urllib.error
import urllib.request

TOLERANCE = 300  # seconds of clock skew allowed on signatures

_SIG = re.compile(r"^t=(\d+),v1=([0-9a-f]{64})$")


def _raw(body):
    return body if isinstance(body, (bytes, bytearray)) else body.encode()


def sign(secret, ts, body):
    """Hex HMAC-SHA256(secret, "<ts>.<body>")."""
    msg = str(ts).encode() + b"." + _raw(body)
    return hmac.new(secret.encode(), msg, hashlib.sha256).hexdigest()


def signature_header(secret, body, ts=None):
    ts = int(time.time()) if ts is None else ts
    return "t=%d,v1=%s" % (ts, sign(secret, ts, body))


def verify_signature(header, secret, body, tolerance=TOLERANCE, now=None):
    """Check X-Polaris-Signature against the RAW request body."""
    m = _SIG.match(header or "")
    if not m or not secret:
        return False
    now = time.time() if now is None else now
    if abs(now - int(m.group(1))) > tolerance:
        return False
    return hmac.compare_digest(sign(secret, m.group(1), body), m.group(2))


def _b64d(s):
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def verify_identity(token, secret, audience=None, scope=None, now=None):
    """Verify the ?polaris_token= identity token. Returns claims or None."""
    parts = str(token or "").split(".")
    if len(parts) != 3 or not secret:
        return None
    h, p, s = parts
    want = base64.urlsafe_b64encode(
        hmac.new(secret.encode(), (h + "." + p).encode(), hashlib.sha256).digest()
    ).rstrip(b"=").decode()
    if not hmac.compare_digest(want, s):
        return None
    try:
        head, claims = json.loads(_b64d(h)), json.loads(_b64d(p))
    except ValueError:
        return None
    now = time.time() if now is None else now
    if head.get("alg") != "HS256" or claims.get("iss") != "polaris":
        return None
    if not isinstance(claims.get("exp"), (int, float)) or claims["exp"] < now:
        return None
    if audience and claims.get("aud") != audience:
        return None
    if scope and claims.get("scope") != scope:
        return None
    return claims


class PanelError(Exception):
    def __init__(self, status, body):
        super().__init__("polaris %d: %s" % (status, body))
        self.status, self.body = status, body


class PanelClient:
    """Calls the panel API with the plugin's token."""

    def __init__(self, api_base, token, opener=None):
        self.api_base = api_base.rstrip("/")
        self.token = token
        self._open = opener or urllib.request.urlopen

    def request(self, method, path, body=None, headers=None):
        h = {"Authorization": "Bearer " + self.token, "Content-Type": "application/json"}
        h.update(headers or {})
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.api_base + path, data=data, method=method, headers=h)
        try:
            with self._open(req, timeout=10) as r:
                text = r.read().decode()
        except urllib.error.HTTPError as e:
            raise PanelError(e.code, e.read().decode(errors="replace"))
        return json.loads(text) if text else None

    def config(self):
        return self.request("GET", "/plugin/config")

    def users(self):
        return self.request("GET", "/users")

    def user(self, user_id):
        return self.request("GET", "/users/" + urllib.parse.quote(user_id, safe=""))

    def renew(self, user_id, days, reset_traffic=False, idempotency_key=None):
        """Extend an account. Pass a stable idempotency_key per order."""
        return self.request("POST", "/users/%s/renew" % urllib.parse.quote(user_id, safe=""),
                            {"days": days, "resetTraffic": reset_traffic}, _idem(idempotency_key))

    def add_traffic(self, user_id, gb, idempotency_key=None):
        return self.request("POST", "/users/%s/add-traffic" % urllib.parse.quote(user_id, safe=""),
                            {"gb": gb}, _idem(idempotency_key))


def _idem(key):
    return {"Idempotency-Key": key} if key else {}


import urllib.parse  # noqa: E402  (kept at the bottom so the module header stays readable)
