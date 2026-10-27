package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// cloudflareV4 is https://www.cloudflare.com/ips-v4 (use -ranges-url to
// fetch the current list instead).
var cloudflareV4 = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}

// Result is one tested IP.
type Result struct {
	IP        string  `json:"ip"`
	LatencyMs float64 `json:"latencyMs"`
	Loss      float64 `json:"loss"` // 0–1
	Colo      string  `json:"-"`    // Cloudflare data centre, from /cdn-cgi/trace
}

// parseRanges reads IPv4 CIDRs, one per line; blank lines and # comments
// are skipped.
func parseRanges(text string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("range %q: %w", line, err)
		}
		if !p.Addr().Is4() {
			continue // IPv6 is not sampled
		}
		out = append(out, p.Masked())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no IPv4 ranges")
	}
	return out, nil
}

// sample picks about n distinct addresses spread over the ranges in
// proportion to their size, at least one per range. Network and broadcast
// style addresses (.0 / .255) are skipped.
func sample(ranges []netip.Prefix, n int, rng *rand.Rand) []netip.Addr {
	var total float64
	for _, p := range ranges {
		total += float64(uint64(1) << (32 - p.Bits()))
	}
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, p := range ranges {
		size := uint64(1) << (32 - p.Bits())
		want := max(1, int(float64(n)*float64(size)/total+0.5))
		base := p.Addr().As4()
		start := uint64(base[0])<<24 | uint64(base[1])<<16 | uint64(base[2])<<8 | uint64(base[3])
		for tries := 0; want > 0 && tries < want*20; tries++ {
			v := start + rng.Uint64N(size)
			a := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
			last := byte(v)
			if last == 0 || last == 255 || seen[a] {
				continue
			}
			seen[a] = true
			out = append(out, a)
			want--
		}
	}
	return out
}

// tcpTest connects to ip:port tries times; latency is the average of the
// successful connects.
func tcpTest(ctx context.Context, ip netip.Addr, port, tries int, timeout time.Duration) Result {
	addr := net.JoinHostPort(ip.String(), fmt.Sprint(port))
	var ok int
	var sum time.Duration
	d := net.Dialer{Timeout: timeout}
	for i := 0; i < tries; i++ {
		start := time.Now()
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			continue
		}
		sum += time.Since(start)
		c.Close()
		ok++
	}
	r := Result{IP: ip.String(), Loss: float64(tries-ok) / float64(tries)}
	if ok > 0 {
		r.LatencyMs = float64(sum.Microseconds()) / 1000 / float64(ok)
	}
	return r
}

// testAll runs tcpTest over every address with limited concurrency and
// drops addresses that never answered.
func testAll(ctx context.Context, ips []netip.Addr, port, tries, workers int, timeout time.Duration) []Result {
	var mu sync.Mutex
	var out []Result
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, ip := range ips {
		wg.Add(1)
		go func(ip netip.Addr) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := tcpTest(ctx, ip, port, tries, timeout)
			if r.Loss < 1 {
				mu.Lock()
				out = append(out, r)
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	rank(out)
	return out
}

// rank sorts by loss, then latency, the way the panel ranks reports.
func rank(rs []Result) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Loss != rs[j].Loss {
			return rs[i].Loss < rs[j].Loss
		}
		return rs[i].LatencyMs < rs[j].LatencyMs
	})
}

// rootCAs verifies the edge's certificate (nil = the system pool; tests
// replace it).
var rootCAs *x509.CertPool

// verify fetches https://host/cdn-cgi/trace through the IP: a working
// Cloudflare edge answers with its data centre ("colo="). An IP that only
// accepts TCP but does not serve the site is dropped.
func verify(ctx context.Context, ip, host string, port int, timeout time.Duration) (string, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, net.JoinHostPort(ip, fmt.Sprint(port)))
		},
		TLSClientConfig:     &tls.Config{ServerName: host, RootCAs: rootCAs},
		TLSHandshakeTimeout: timeout,
	}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 3 * timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s:%d/cdn-cgi/trace", host, port), nil)
	if err != nil {
		return "", err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %s", resp.Status)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, "colo="); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", fmt.Errorf("not a Cloudflare edge (no colo in trace)")
}

// verifyTop checks candidates in rank order until want of them pass.
func verifyTop(ctx context.Context, rs []Result, want int, host string, port int, timeout time.Duration) []Result {
	var out []Result
	for _, r := range rs {
		if len(out) >= want {
			break
		}
		colo, err := verify(ctx, r.IP, host, port, timeout)
		if err != nil {
			continue
		}
		r.Colo = colo
		out = append(out, r)
	}
	return out
}
