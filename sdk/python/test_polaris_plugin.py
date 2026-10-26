import json
import os
import unittest

import polaris_plugin as sdk

V = json.load(open(os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "vectors.json")))


class Tests(unittest.TestCase):
    def test_sign_vector(self):
        self.assertEqual(sdk.sign(V["secret"], V["timestamp"], V["body"]), V["signature"])

    def test_verify_signature(self):
        ok = lambda h=V["header"], s=V["secret"], b=V["body"], now=V["now"]: sdk.verify_signature(h, s, b, now=now)
        self.assertTrue(ok())
        self.assertFalse(ok(s="other"))
        self.assertFalse(ok(b=V["body"] + " "))
        self.assertFalse(ok(now=V["now"] + 1000))
        self.assertFalse(ok(h="garbage"))
        self.assertFalse(ok(h=""))

    def test_header_roundtrip(self):
        self.assertTrue(sdk.verify_signature(sdk.signature_header("k", b"body"), "k", b"body"))

    def test_identity(self):
        j, o = V["jwt"], dict(audience="hello", scope="user", now=V["now"])
        self.assertEqual(sdk.verify_identity(j["valid"], V["secret"], **o)["sub"], "u1")
        for bad in ("wrongSecret", "expired", "wrongAudience", "wrongIssuer"):
            self.assertIsNone(sdk.verify_identity(j[bad], V["secret"], **o), bad)
        self.assertIsNone(sdk.verify_identity(j["valid"], V["secret"], **{**o, "scope": "admin"}))
        self.assertIsNone(sdk.verify_identity("a.b", V["secret"], **o))

    def test_client(self):
        seen = {}

        class R:
            def __enter__(s): return s
            def __exit__(s, *a): return False
            def read(s): return b'{"id":"u1"}'

        def opener(req, timeout):
            seen["req"] = req
            return R()

        c = sdk.PanelClient("http://p/api/v1/", "plt_x", opener=opener)
        self.assertEqual(c.renew("u 1", 30, idempotency_key="k1")["id"], "u1")
        r = seen["req"]
        self.assertEqual(r.full_url, "http://p/api/v1/users/u%201/renew")
        self.assertEqual(r.get_header("Authorization"), "Bearer plt_x")
        self.assertEqual(r.get_header("Idempotency-key"), "k1")
        self.assertEqual(json.loads(r.data), {"days": 30, "resetTraffic": False})


if __name__ == "__main__":
    unittest.main()


EXT = json.load(open(os.path.join(os.path.dirname(__file__), "..", "..", "testdata", "extensions.json"), encoding="utf-8"))


class ExtensionTests(unittest.TestCase):
    def test_check_transform(self):
        for c in EXT["transform"]["cases"]:
            err = sdk.check_transform(EXT["transform"]["request"], c["response"])
            self.assertEqual(err is None, c["ok"], "%s: %s" % (c["name"], err))

    def test_check_metric(self):
        now = sdk._parse_time(EXT["metrics"]["now"])
        for c in EXT["metrics"]["cases"]:
            err = sdk.check_metric(c["metric"], now)
            self.assertEqual(err is None, c["ok"], "%s: %s" % (c["metric"], err))

    def test_parse_signed(self):
        self.assertEqual(sdk.parse_signed(V["header"], V["secret"], V["body"], now=V["now"])["id"], "evt_1")
        self.assertIsNone(sdk.parse_signed(V["header"], "x", V["body"], now=V["now"]))
        self.assertIsNone(sdk.parse_signed(sdk.signature_header("k", "nope"), "k", "nope"))

    def test_report_metrics(self):
        seen = {}

        class Resp:
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def read(self):
                return b'{"stored":1}'

        def opener(req, timeout):
            seen["req"] = req
            return Resp()

        c = sdk.PanelClient("http://p/api/v1", "t", opener=opener)
        c.report_metrics("n/1", [{"name": "latency", "unit": "ms", "value": 3}])
        self.assertEqual(seen["req"].full_url, "http://p/api/v1/nodes/n%2F1/metrics")
        self.assertEqual(json.loads(seen["req"].data), {"metrics": [{"name": "latency", "unit": "ms", "value": 3}]})
