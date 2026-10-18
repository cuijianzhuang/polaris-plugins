package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(t *testing.T) Manifest {
	t.Helper()
	raw, err := os.ReadFile("../examples/traffic-summary/polaris-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestManifestRejectsUnsupportedAndAmbiguousInput(t *testing.T) {
	m := sample(t)
	raw, _ := json.Marshal(m)
	for _, bad := range []string{`{"id":"first","id":"second"}`, string(raw) + `{}`, strings.Replace(string(raw), `"manifestVersion":1`, `"manifestVersion":2`, 1), strings.Replace(string(raw), `"runtime":"external"`, `"runtime":"exec"`, 1), strings.Replace(string(raw), `"pages":[]`, `"pages":[{"id":"x","path":"//evil"}]`, 1), strings.Replace(string(raw), `"traffic:read"`, `"events"`, 1), strings.Replace(string(raw), `"version":"1.0.0"`, `"version":"01.0.0"`, 1), strings.Replace(string(raw), `"author":`, `"unknown":`, 1), strings.Repeat("[", 18) + strings.Repeat("]", 18), strings.Repeat(" ", MaxManifest+1)} {
		if _, err := DecodeManifest([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid manifest: %.100s", bad)
		}
	}
}
func TestSettingsAndSignature(t *testing.T) {
	m := sample(t)
	plain, secrets, err := m.SplitSettings(map[string]any{"label": "demo", "service_token": "hidden"}, nil, nil)
	if err != nil || plain["service_token"] != nil || secrets["service_token"] != "hidden" {
		t.Fatal("secret separation", err)
	}
	_, kept, err := m.SplitSettings(map[string]any{"label": "next", "service_token": ""}, secrets, nil)
	if err != nil || kept["service_token"] != "hidden" {
		t.Fatal("secret preservation", err)
	}
	_, cleared, err := m.SplitSettings(map[string]any{"label": "next"}, secrets, []string{"service_token"})
	if err != nil || cleared["service_token"] != "" {
		t.Fatal("secret clearing", err)
	}
	for _, bad := range []map[string]any{{}, {"label": true}, {"label": "ok", "undeclared": "value"}} {
		if _, _, err := m.SplitSettings(bad, nil, nil); err == nil {
			t.Fatal("accepted invalid settings")
		}
	}
	now := time.Now()
	body := []byte("hello")
	header := Signature("secret", body, now)
	if !VerifySignature("secret", header, body, now) || VerifySignature("wrong", header, body, now) || VerifySignature("secret", header, []byte("other"), now) || VerifySignature("secret", header, body, now.Add(6*time.Minute)) {
		t.Fatal("signature verification")
	}
}
func TestDurablePairingHealthAndScopedClient(t *testing.T) {
	code := strings.Repeat("p", 32)
	path := filepath.Join(t.TempDir(), "data", "installation.json")
	service, err := NewService(sample(t), code, path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	token := "plt_" + strings.Repeat("t", 40)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Path != "/admin/api/v1/stats/overview" {
			t.Error("incorrect panel authorization/path")
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"total":3}`)
	}))
	defer panel.Close()
	credentials := Credentials{Installation: Installation{HostAPI, panel.URL, panel.URL + "/admin/api/v1", "abcdefghijklmnop", strings.Repeat("n", 32), token}, ControlSecret: strings.Repeat("s", 40)}
	request := func(path string, body any, authorization, signature string) int {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", authorization)
		req.Header.Set("X-Polaris-Signature", signature)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := request("/polaris/install", credentials, "", ""); code != 401 {
		t.Fatal("anonymous pairing", code)
	}
	if code := request("/polaris/install", credentials, "Bearer "+code, ""); code != 200 {
		t.Fatal("authorized pairing", code)
	}
	if code := request("/polaris/install", credentials, "Bearer "+code, ""); code != 200 {
		t.Fatal("idempotent pairing", code)
	}
	restored, err := NewService(sample(t), code, path)
	if err != nil || restored.Credentials() != credentials {
		t.Fatal("state not durable", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("credentials permissions")
	}
	health := HealthRequest{credentials.InstallID, strings.Repeat("h", 32)}
	raw, _ := json.Marshal(health)
	if status := request("/polaris/health", health, "", Signature(credentials.ControlSecret, raw, time.Now())); status != 200 {
		t.Fatal("signed health", status)
	}
	if status := request("/polaris/health", health, "", Signature("wrong", raw, time.Now())); status != 401 {
		t.Fatal("invalid signature", status)
	}
	other := credentials
	other.InstallID = "ponmlkjihgfedcba"
	if status := request("/polaris/install", other, "Bearer "+code, ""); status != 409 {
		t.Fatal("anonymous rebind", status)
	}
	other = credentials
	other.Token = "plt_other"
	if status := request("/polaris/install", other, "Bearer "+code, ""); status != 409 {
		t.Fatal("nonce conflict", status)
	}
	var result map[string]int
	if err := restored.Get(context.Background(), "/stats/overview", &result); err != nil || result["total"] != 3 {
		t.Fatal("scoped client", err)
	}
	if err := restored.Get(context.Background(), "//evil", &result); err == nil {
		t.Fatal("accepted absolute path")
	}
}
