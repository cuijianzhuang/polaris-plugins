package polaris

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type vectors struct {
	Secret, Timestamp, Body, Signature, Header string
	Now                                        int64
	JWT                                        map[string]json.RawMessage `json:"jwt"`
}

func load(t *testing.T) vectors {
	b, err := os.ReadFile("../../testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func tok(t *testing.T, v vectors, name string) string {
	var s string
	if err := json.Unmarshal(v.JWT[name], &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignAndVerify(t *testing.T) {
	v := load(t)
	now := time.Unix(v.Now, 0)
	if got := Sign(v.Secret, 1700000000, []byte(v.Body)); got != v.Signature {
		t.Fatalf("sign = %s", got)
	}
	if !VerifySignature(v.Header, v.Secret, []byte(v.Body), now) {
		t.Fatal("valid signature rejected")
	}
	for name, ok := range map[string]bool{
		"wrong secret": VerifySignature(v.Header, "other", []byte(v.Body), now),
		"tampered":     VerifySignature(v.Header, v.Secret, []byte(v.Body+" "), now),
		"stale":        VerifySignature(v.Header, v.Secret, []byte(v.Body), now.Add(time.Hour)),
		"garbage":      VerifySignature("garbage", v.Secret, []byte(v.Body), now),
		"empty":        VerifySignature("", v.Secret, []byte(v.Body), now),
	} {
		if ok {
			t.Errorf("%s accepted", name)
		}
	}
	if !VerifySignature(SignatureHeader("k", []byte("b"), time.Now()), "k", []byte("b"), time.Now()) {
		t.Error("round trip failed")
	}
}

func TestVerifyIdentity(t *testing.T) {
	v := load(t)
	now := time.Unix(v.Now, 0)
	c, ok := VerifyIdentity(tok(t, v, "valid"), v.Secret, "hello", "user", now)
	if !ok || c.Subject != "u1" || c.Name != "alice" {
		t.Fatalf("valid token: %+v %v", c, ok)
	}
	for _, name := range []string{"wrongSecret", "expired", "wrongAudience", "wrongIssuer"} {
		if _, ok := VerifyIdentity(tok(t, v, name), v.Secret, "hello", "user", now); ok {
			t.Errorf("%s accepted", name)
		}
	}
	if _, ok := VerifyIdentity(tok(t, v, "valid"), v.Secret, "hello", "admin", now); ok {
		t.Error("wrong scope accepted")
	}
}

func TestClientRenew(t *testing.T) {
	var gotAuth, gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotKey, gotPath = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.URL.EscapedPath()
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"id":"u 1"}`))
	}))
	defer srv.Close()
	var u struct{ ID string }
	if err := NewClient(srv.URL+"/api/v1/", "plt_x").Renew("u 1", 30, false, "k1", &u); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer plt_x" || gotKey != "k1" || gotPath != "/api/v1/users/u%201/renew" || gotBody["days"] != float64(30) || u.ID != "u 1" {
		t.Fatalf("auth=%q key=%q path=%q body=%v user=%+v", gotAuth, gotKey, gotPath, gotBody, u)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 403) }))
	defer srv2.Close()
	err := NewClient(srv2.URL, "t").Config(nil)
	if e, ok := err.(*Error); !ok || e.Status != 403 {
		t.Fatalf("want *Error 403, got %v", err)
	}
}
