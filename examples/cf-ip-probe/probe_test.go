package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestSampleStaysInRanges(t *testing.T) {
	rs, err := parseRanges(strings.Join(cloudflareV4, "\n") + "\n# comment\n2606:4700::/32\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != len(cloudflareV4) {
		t.Fatalf("ranges = %d (IPv6 and comments must be skipped)", len(rs))
	}
	ips := sample(rs, 600, rand.New(rand.NewPCG(1, 2)))
	if len(ips) < 550 || len(ips) > 650 {
		t.Fatalf("sampled %d, want about 600", len(ips))
	}
	seen := map[netip.Addr]bool{}
	hit := map[netip.Prefix]bool{}
	for _, ip := range ips {
		if seen[ip] {
			t.Fatalf("duplicate %s", ip)
		}
		seen[ip] = true
		in := false
		for _, p := range rs {
			if p.Contains(ip) {
				in, hit[p] = true, true
			}
		}
		if !in {
			t.Fatalf("%s outside the ranges", ip)
		}
		if b := ip.As4(); b[3] == 0 || b[3] == 255 {
			t.Fatalf("%s is a .0/.255 address", ip)
		}
	}
	if len(hit) != len(rs) {
		t.Fatalf("only %d of %d ranges sampled", len(hit), len(rs))
	}
	if _, err := parseRanges("nonsense"); err == nil {
		t.Fatal("bad range accepted")
	}
}

func TestRankLossThenLatency(t *testing.T) {
	rs := []Result{{IP: "a", LatencyMs: 10, Loss: 0.25}, {IP: "b", LatencyMs: 90}, {IP: "c", LatencyMs: 40}}
	rank(rs)
	if got := rs[0].IP + rs[1].IP + rs[2].IP; got != "cba" {
		t.Fatalf("order = %s", got)
	}
}

func TestTCPTest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	lo := netip.MustParseAddr("127.0.0.1")
	if r := tcpTest(context.Background(), lo, port, 3, time.Second); r.Loss != 0 || r.LatencyMs <= 0 {
		t.Fatalf("open port: %+v", r)
	}
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	cport := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	if r := tcpTest(context.Background(), lo, cport, 2, time.Second); r.Loss != 1 {
		t.Fatalf("closed port: %+v", r)
	}
	if got := testAll(context.Background(), []netip.Addr{lo}, cport, 2, 4, time.Second); len(got) != 0 {
		t.Fatalf("unanswered IPs kept: %+v", got)
	}
}

func TestVerifyEdge(t *testing.T) {
	edge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cdn-cgi/trace" && strings.HasPrefix(r.Host, "example.com") {
			fmt.Fprint(w, "fl=1\nh=example.com\ncolo=NRT\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer edge.Close()
	notEdge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello") }))
	defer notEdge.Close()
	rootCAs = edge.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer func() { rootCAs = nil }()
	port := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }

	colo, err := verify(context.Background(), "127.0.0.1", "example.com", port(edge), time.Second)
	if err != nil || colo != "NRT" {
		t.Fatalf("edge: %q %v", colo, err)
	}
	rootCAs = notEdge.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	if _, err := verify(context.Background(), "127.0.0.1", "example.com", port(notEdge), time.Second); err == nil {
		t.Fatal("a server without /cdn-cgi/trace passed")
	}
	if _, err := verify(context.Background(), "127.0.0.1", "wrong-name.test", port(notEdge), time.Second); err == nil {
		t.Fatal("certificate name mismatch passed")
	}
}

func TestReport(t *testing.T) {
	var got struct {
		Source string   `json:"source"`
		IPs    []Result `json:"ips"`
	}
	var path, auth string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"stored":2}`))
	}))
	defer panel.Close()
	c := config{API: panel.URL + "/admin/api/v1/", Token: "plt_x", Source: "上海电信"}
	rs := []Result{{IP: "104.16.0.1", LatencyMs: 38.5}, {IP: "104.16.0.2", LatencyMs: 50, Loss: 0.25, Colo: "HKG"}}
	if err := report(context.Background(), c, "cf1", rs); err != nil {
		t.Fatal(err)
	}
	if path != "/admin/api/v1/cdn-profiles/cf1/preferred-ips" || auth != "Bearer plt_x" || got.Source != "上海电信" || len(got.IPs) != 2 || got.IPs[1].Loss != 0.25 {
		t.Fatalf("path=%s auth=%s body=%+v", path, auth, got)
	}

	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"API 令牌缺少权限 cdn:ips:write"}`))
	}))
	defer refuse.Close()
	c.API = refuse.URL
	if err := report(context.Background(), c, "cf1", rs); err == nil || !strings.Contains(err.Error(), "cdn:ips:write") {
		t.Fatalf("refusal not surfaced: %v", err)
	}
}

func TestFlags(t *testing.T) {
	if _, err := parseFlags([]string{"-api", "x", "-token", "t", "-profile", "p"}); err == nil {
		t.Fatal("missing -source accepted")
	}
	c, err := parseFlags([]string{"-api", "x", "-token", "t", "-profile", "a, b", "-source", "上海电信", "-once"})
	if err != nil || len(c.Profiles) != 2 || c.Profiles[1] != "b" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := parseFlags([]string{"-source", "x", "-dry-run", "-interval", "1m"}); err == nil {
		t.Fatal("a 1-minute interval accepted")
	}
}
