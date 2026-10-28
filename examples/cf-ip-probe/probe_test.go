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
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSampleOnePerBlock(t *testing.T) {
	rs, err := parseRanges(strings.Join(cloudflareV4, "\n") + "\n# comment\n2606:4700::/32\n104.16.0.0/16\n1.1.1.1\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != len(cloudflareV4)+2 {
		t.Fatalf("ranges = %d (IPv6 and comments must be skipped)", len(rs))
	}
	bs := blocks(rs)
	// Cloudflare's list is 5,956 /24s; the overlapping /16 adds none and 1.1.1.1/32 adds one.
	if len(bs) != 5957 {
		t.Fatalf("blocks = %d, want 5957", len(bs))
	}
	all := sample(rs, 0, rand.New(rand.NewPCG(1, 2)))
	if len(all) != len(bs) {
		t.Fatalf("sample(0) = %d, want one per block", len(all))
	}
	perBlock := map[netip.Prefix]int{}
	for _, ip := range all {
		in := false
		for _, p := range rs {
			in = in || p.Contains(ip)
		}
		if !in {
			t.Fatalf("%s outside the ranges", ip)
		}
		if b := ip.As4(); (b[3] == 0 || b[3] == 255) && ip.String() != "1.1.1.1" {
			t.Fatalf("%s is a .0/.255 address", ip)
		}
		q, _ := ip.Prefix(24)
		perBlock[q]++
	}
	for q, n := range perBlock {
		if n != 1 && q.String() != "1.1.1.0/24" {
			t.Fatalf("block %s sampled %d times", q, n)
		}
	}
	if some := sample(rs, 300, rand.New(rand.NewPCG(3, 4))); len(some) != 300 {
		t.Fatalf("sample(300) = %d", len(some))
	}
	if _, err := parseRanges("nonsense"); err == nil {
		t.Fatal("bad range accepted")
	}
}

func TestRankLossSpeedLatency(t *testing.T) {
	rs := []Result{{IP: "a", LatencyMs: 10, Loss: 0.25, Speed: 50}, {IP: "b", LatencyMs: 90}, {IP: "c", LatencyMs: 40}, {IP: "d", LatencyMs: 120, Speed: 9}}
	rank(rs)
	if got := rs[0].IP + rs[1].IP + rs[2].IP + rs[3].IP; got != "dcba" {
		t.Fatalf("order = %s", got)
	}
}

// The picker checks HTTPS, filters by data centre and speed, and download-
// tests only the first dn that pass.
func TestPicker(t *testing.T) {
	cands := []Result{{IP: "1"}, {IP: "2"}, {IP: "3"}, {IP: "4"}, {IP: "5"}, {IP: "6"}}
	colo := map[string]string{"1": "HKG", "2": "", "3": "LAX", "4": "HKG", "5": "HKG", "6": "NRT"}
	speed := map[string]float64{"1": 3, "3": 20, "4": 12, "5": 8, "6": 30}
	var downloaded []string
	newPicker := func(f filter, top, dn int) *picker {
		downloaded = nil
		return &picker{Top: top, Verify: true, DownloadN: dn, Filter: f,
			verify: func(_ context.Context, ip, _ string, _ int, _ time.Duration) (string, error) {
				if colo[ip] == "" {
					return "", fmt.Errorf("not an edge")
				}
				return colo[ip], nil
			},
			download: func(_ context.Context, ip string, _ int, _ string, _, _ time.Duration) (float64, string, error) {
				downloaded = append(downloaded, ip)
				return speed[ip], "", nil
			}}
	}
	ips := func(rs []Result) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.IP)
		}
		return strings.Join(s, " ")
	}
	// No limits: "2" fails HTTPS; the first two are download-tested and rank first.
	got := newPicker(filter{}, 4, 2).pick(context.Background(), cands)
	if ips(got) != "3 1 4 5" || strings.Join(downloaded, " ") != "1 3" || got[0].Colo != "LAX" {
		t.Fatalf("plain: %s (downloaded %v)", ips(got), downloaded)
	}
	// HKG only, at least 5 MB/s: "1" is too slow, so the next HKG IPs are tested.
	got = newPicker(filter{MinSpeed: 5, Colos: parseColos("hkg")}, 3, 3).pick(context.Background(), cands)
	if ips(got) != "4 5" || strings.Join(downloaded, " ") != "1 4 5" {
		t.Fatalf("hkg >= 5: %s (downloaded %v)", ips(got), downloaded)
	}
	// No download test.
	got = newPicker(filter{}, 2, 0).pick(context.Background(), cands)
	if ips(got) != "1 3" || len(downloaded) != 0 {
		t.Fatalf("-dd: %s (downloaded %v)", ips(got), downloaded)
	}
}

// A download test that cannot reach its file must not drop IPs that passed
// the HTTPS check: they are reported without a speed, the test is given up
// after three failures, and the summary names the reason.
func TestPickerDownloadUnreachable(t *testing.T) {
	var cands []Result
	for i := 1; i <= 8; i++ {
		cands = append(cands, Result{IP: fmt.Sprintf("104.16.0.%d", i), LatencyMs: float64(i)})
	}
	attempts := 0
	newPicker := func(f filter) *picker {
		attempts = 0
		return &picker{Top: 5, Verify: true, DownloadN: 5, Filter: f,
			verify: func(_ context.Context, ip, _ string, _ int, _ time.Duration) (string, error) {
				if ip == "104.16.0.2" {
					return "", fmt.Errorf("dial tcp %s:443: i/o timeout", ip)
				}
				return "HKG", nil
			},
			download: func(_ context.Context, ip string, _ int, _ string, _, _ time.Duration) (float64, string, error) {
				attempts++
				return 0, "", fmt.Errorf("download: status 403 Forbidden")
			}}
	}
	p := newPicker(filter{})
	got := p.pick(context.Background(), cands)
	if len(got) != 5 || got[0].IP != "104.16.0.1" || got[0].Speed != 0 || attempts != 3 {
		t.Fatalf("got %+v after %d download attempts", got, attempts)
	}
	sum := p.summary()
	if !strings.Contains(sum, "HTTPS check: 5 of 6 passed") || !strings.Contains(sum, "dial tcp <ip>: i/o timeout (×1)") ||
		!strings.Contains(sum, "download test: 0 of 3 succeeded, most common failure: download: status 403 Forbidden (×3)") {
		t.Fatalf("summary = %q", sum)
	}
	// With a speed floor nothing untested may be reported.
	if got := newPicker(filter{MinSpeed: 1}).pick(context.Background(), cands); len(got) != 0 {
		t.Fatalf("-sl reported untested IPs: %+v", got)
	}
}

// A check that times out is tried once more before the IP is dropped.
func TestPickerRetriesTimeout(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	p := &picker{Top: 2, Verify: true,
		verify: func(_ context.Context, ip, _ string, _ int, _ time.Duration) (string, error) {
			mu.Lock()
			calls[ip]++
			n := calls[ip]
			mu.Unlock()
			switch {
			case ip == "1" && n == 1:
				return "", &net.OpError{Op: "dial", Err: timeoutErr{}}
			case ip == "2":
				return "", fmt.Errorf("status 403 Forbidden")
			}
			return "HKG", nil
		}}
	got := p.pick(context.Background(), []Result{{IP: "1"}, {IP: "2"}, {IP: "3"}})
	if len(got) != 2 || got[0].IP != "1" || calls["1"] != 2 || calls["2"] != 1 {
		t.Fatalf("got %+v, calls %v", got, calls)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestDownload(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "speed.example.com:"+strings.Split(r.Host, ":")[1] || r.URL.Path != "/big" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("CF-RAY", "8d1f2e3a4b5c6d7e-HKG")
		fmt.Fprint(w, big)
	}))
	defer srv.Close()
	rootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer func() { rootCAs = nil }()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	speed, colo, err := download(context.Background(), "127.0.0.1", port, "https://speed.example.com/big", 2*time.Second, time.Second)
	if err != nil || speed <= 0 || colo != "HKG" {
		t.Fatalf("download: %v MB/s %q %v", speed, colo, err)
	}
	if _, _, err := download(context.Background(), "127.0.0.1", port, "https://speed.example.com/missing", time.Second, time.Second); err == nil {
		t.Fatal("a 404 counted as a download")
	}
}

func TestCSVImport(t *testing.T) {
	path := t.TempDir() + "/result.csv"
	os.WriteFile(path, []byte("\ufeffIP 地址,已发送,已接收,丢包率,平均延迟,下载速度(MB/s),地区码\n"+
		"104.16.1.1,4,4,0.00,180.12,3.50,LAX\n"+
		"104.16.1.2,4,4,0.00,160.00,12.80,hkg\n"+
		"104.16.1.3,4,3,0.25,90.00,30.00,HKG\n"+
		"172.64.1.1,4,4,0.00,95.00,0.00,N/A\n"), 0o600)
	rs, err := readCSV(path)
	if err != nil || len(rs) != 4 {
		t.Fatalf("read: %d %v", len(rs), err)
	}
	if rs[1].Colo != "HKG" || rs[3].Colo != "" || rs[2].Loss != 0.25 {
		t.Fatalf("parsed: %+v", rs)
	}
	got := fromCSV(rs, filter{MaxLatency: 9999, MaxLoss: 1}, 3)
	if len(got) != 3 || got[0].IP != "104.16.1.2" || got[1].IP != "104.16.1.1" || got[2].IP != "172.64.1.1" {
		t.Fatalf("ranked: %+v", got)
	}
	got = fromCSV(rs, filter{MaxLatency: 170, MaxLoss: 0, MinSpeed: 1, Colos: parseColos("HKG")}, 10)
	if len(got) != 1 || got[0].IP != "104.16.1.2" {
		t.Fatalf("filtered: %+v", got)
	}
	os.WriteFile(path, []byte("hello\n"), 0o600)
	if _, err := readCSV(path); err == nil {
		t.Fatal("a file with no results was accepted")
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
		Source  string   `json:"source"`
		Carrier string   `json:"carrier"`
		IPs     []Result `json:"ips"`
	}
	var path, auth string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"stored":2}`))
	}))
	defer panel.Close()
	c := config{API: panel.URL + "/admin/api/v1/", Token: "plt_x", Source: "上海电信", Carrier: "ct"}
	rs := []Result{{IP: "104.16.0.1", LatencyMs: 38.5}, {IP: "104.16.0.2", LatencyMs: 50, Loss: 0.25, Colo: "HKG"}}
	if err := report(context.Background(), c, "cf1", rs); err != nil {
		t.Fatal(err)
	}
	if path != "/admin/api/v1/cdn-profiles/cf1/preferred-ips" || auth != "Bearer plt_x" || got.Source != "上海电信" || got.Carrier != "ct" || len(got.IPs) != 2 || got.IPs[1].Loss != 0.25 {
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
	for _, bad := range [][]string{
		{"-dd", "-sl", "5"},                      // speed limit without a download test
		{"-dd", "-verify=false", "-colo", "HKG"}, // nothing tells the data centre
		{"-tlr", "2"},
		{"-url", "ftp://x/y"},
		{"-workers", "5000"},
		{"-carrier", "att"},
	} {
		if _, err := parseFlags(append([]string{"-source", "x", "-dry-run", "-once"}, bad...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	c, err = parseFlags([]string{"-source", "x", "-dry-run", "-once", "-dd"})
	if err != nil || c.DownloadN != 0 {
		t.Fatalf("-dd: %+v %v", c, err)
	}
	// CloudflareSpeedTest result import needs no test settings.
	if _, err := parseFlags([]string{"-source", "x", "-dry-run", "-once", "-csv", "r.csv", "-dd", "-verify=false", "-colo", "HKG", "-sl", "3"}); err != nil {
		t.Fatalf("-csv: %v", err)
	}
}
