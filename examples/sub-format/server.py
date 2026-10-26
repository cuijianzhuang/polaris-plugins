"""Polaris "sub-format" example plugin (Python 3.8+, standard library only).

  - adds a subscription format: /sub/<token>?format=csv
  - rewrites every user's node list: flag emoji in front of the name, nodes
    sorted by region, and nodes whose name contains DROP_KEYWORD removed

    PORT=9200 python3 server.py
"""
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "sdk", "python"))
import polaris_plugin as sdk  # noqa: E402

PORT = int(os.environ.get("PORT", "9200"))
STATE_FILE = os.environ.get("STATE_FILE", "state.json")
INSTALL_KEY = os.environ.get("INSTALL_KEY", "")  # optional: require X-Polaris-Install-Key
DROP_KEYWORD = os.environ.get("DROP_KEYWORD", "")

with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "polaris-plugin.json"), encoding="utf-8") as f:
    MANIFEST = json.load(f)

state = {}
try:
    with open(STATE_FILE, encoding="utf-8") as f:
        state = json.load(f)
except (OSError, ValueError):
    pass


def save():
    with open(STATE_FILE, "w", encoding="utf-8") as f:
        json.dump(state, f)
    os.chmod(STATE_FILE, 0o600)


def flag(region):
    """ISO country code -> flag emoji ("JP" -> 🇯🇵)."""
    if len(region or "") != 2 or not region.isalpha():
        return ""
    return "".join(chr(0x1F1E6 + ord(c) - ord("A")) for c in region.upper())


def csv_cell(v):
    s = str(v)
    if any(c in s for c in ',"\n\r') or s[:1] in "=+-@":  # also defuse spreadsheet formulas
        s = '"' + s.replace('"', '""') + '"'
    return s


def render_csv(req):
    # Nodes carry UUIDs and passwords. This format deliberately leaves them
    # out; a real client format would use them but must never log or keep them.
    rows = ["name,region,protocol,server,port"]
    for n in req.get("nodes") or []:
        rows.append(",".join(csv_cell(n.get(k, "")) for k in ("Name", "Region", "Protocol", "Server", "Port")))
    return "\n".join(rows) + "\n"


def transform(req):
    out = []
    for n in req.get("nodes") or []:
        if DROP_KEYWORD and DROP_KEYWORD in n.get("name", ""):
            continue
        f = flag(n.get("region"))
        name = n.get("name", "")
        pick = {"id": n["id"]}
        if f and not name.startswith(f):
            pick["name"] = (f + " " + name)[:100]
        out.append((n.get("region") or "~", pick))
    out.sort(key=lambda x: x[0])  # stable: keeps the panel's order inside a region
    return {"nodes": [p for _, p in out]}


class Handler(BaseHTTPRequestHandler):
    def send(self, code, body, ctype="application/json"):
        raw = body if isinstance(body, bytes) else (body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype + "; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n > 8 << 20:
            raise ValueError("too large")
        return self.rfile.read(n)

    def do_GET(self):
        if self.path == "/polaris-plugin.json":
            return self.send(200, MANIFEST)
        if self.path == "/polaris/health":
            return self.send(200 if state.get("plugin_secret") else 503, {"ok": bool(state.get("plugin_secret"))})
        self.send(404, {"error": "not found"})

    def do_POST(self):
        try:
            raw = self.body()
        except ValueError:
            return self.send(413, {"error": "too large"})
        sig = self.headers.get("X-Polaris-Signature")
        if self.path == "/polaris/install":
            if INSTALL_KEY and self.headers.get("X-Polaris-Install-Key") != INSTALL_KEY:
                return self.send(403, {"error": "bad install key"})
            try:
                secret = json.loads(raw).get("plugin_secret", "")
            except ValueError:
                return self.send(400, {"error": "bad json"})
            b = sdk.parse_signed(sig, secret, raw)
            if b is None:
                return self.send(401, {"error": "bad signature"})
            state.clear()
            state.update({k: b.get(k, "") for k in ("api_base", "token", "plugin_secret")})
            save()
            return self.send(200, {"ok": True})

        b = sdk.parse_signed(sig, state.get("plugin_secret", ""), raw)
        if b is None:
            return self.send(401, {"error": "bad signature"})
        if self.path == "/polaris/render":
            if b.get("format") != "csv":
                return self.send(404, {"error": "unknown format"})
            return self.send(200, render_csv(b), "text/plain")
        if self.path == "/polaris/transform":
            return self.send(200, transform(b))
        if self.path == "/polaris/config":
            return self.send(200, {"ok": True})
        if self.path == "/polaris/uninstall":
            state.clear()
            save()
            return self.send(200, {"ok": True})
        self.send(404, {"error": "not found"})

    def log_message(self, fmt, *args):  # keep request bodies (credentials) out of logs
        sys.stderr.write("%s %s\n" % (self.command, self.path))


if __name__ == "__main__":
    print("sub-format plugin on :%d" % PORT)
    ThreadingHTTPServer(("", PORT), Handler).serve_forever()
