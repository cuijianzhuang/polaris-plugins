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


# ---- Subscription extensions and node metrics (protocol stage 4) ----

SUB_CONTENT_TYPES = ("text/plain", "text/yaml", "application/yaml", "application/x-yaml",
                     "application/json", "application/xml", "text/xml")
METRIC_UNITS = ("ms", "s", "Mbps", "kbps", "%", "count", "bool")
METRIC_RESERVED = ("cpu", "mem", "memory", "rx", "tx", "load", "online", "status",
                   "heartbeat", "traffic", "up", "down")
_METRIC_NAME = re.compile(r"^[a-z][a-z0-9_.-]{0,39}$")
_RFC3339 = re.compile(r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$")


def _has_control(s):
    return any(ord(c) < 0x20 or ord(c) == 0x7F for c in s)


def parse_signed(header, secret, body, tolerance=TOLERANCE, now=None):
    """Verify a signed panel request and return its JSON body, or None."""
    if not verify_signature(header, secret, body, tolerance, now):
        return None
    try:
        return json.loads(_raw(body))
    except ValueError:
        return None


def check_transform(request, response):
    """Apply the panel's rules to a /polaris/transform answer.

    Returns None when the panel would accept it, otherwise the reason.
    """
    ids = {n.get("id") for n in request.get("nodes") or []}
    nodes = response.get("nodes") if isinstance(response, dict) else None
    if not isinstance(nodes, list):
        return 'answer must be {"nodes": [...]}'
    seen = set()
    for n in nodes:
        nid = n.get("id") if isinstance(n, dict) else None
        if not isinstance(nid, str) or nid not in ids:
            return "unknown node id %r" % (nid,)
        if nid in seen:
            return "node id %s listed twice" % nid
        seen.add(nid)
        name = n.get("name")
        if name is None:
            continue
        if not isinstance(name, str):
            return "name of %s must be a string" % nid
        name = name.strip()
        if name and (len(name) > 100 or _has_control(name)):
            return "name of %s must be at most 100 characters without control characters" % nid
    return None


def _parse_time(s):
    import datetime
    if not isinstance(s, str) or not _RFC3339.match(s):
        return None
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except ValueError:
        return None


def check_metric(m, now=None):
    """Apply the panel's rules to one reported metric. None = valid."""
    now = time.time() if now is None else now
    name = m.get("name") if isinstance(m, dict) else None
    if not isinstance(name, str) or not _METRIC_NAME.match(name):
        return "name must start with a lowercase letter, at most 40 characters"
    if name in METRIC_RESERVED:
        return "name %s is reserved" % name
    if m.get("unit") not in METRIC_UNITS:
        return "unit must be one of " + ", ".join(METRIC_UNITS)
    v = m.get("value")
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v != v or abs(v) > 1e12:
        return "value must be a finite number within ±1e12"
    if m["unit"] == "bool" and v not in (0, 1):
        return "a bool metric is 0 or 1"
    label = m.get("label")
    if label is not None and (not isinstance(label, str) or len(label) > 40 or _has_control(label)):
        return "label must be at most 40 characters without control characters"
    at = m.get("at")
    if at not in (None, ""):
        t = _parse_time(at)
        if t is None or t < now - 86400 or t > now + 300:
            return "at must be an RFC 3339 time within the last 24 hours"
    return None


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

    def nodes(self):
        """List nodes (scope nodes:read)."""
        return self.request("GET", "/nodes")

    def report_metrics(self, node_id, metrics):
        """Store readings on a node (scope nodes:metrics:write), at most 20 per call."""
        return self.request("POST", "/nodes/%s/metrics" % urllib.parse.quote(node_id, safe=""),
                            {"metrics": metrics})

    def external_metrics(self, node_id):
        """Read every source's metrics for a node (scope nodes:read)."""
        return self.request("GET", "/nodes/%s/metrics/external" % urllib.parse.quote(node_id, safe=""))


def _idem(key):
    return {"Idempotency-Key": key} if key else {}


import urllib.parse  # noqa: E402  (kept at the bottom so the module header stays readable)
