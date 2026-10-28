package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cloudflareV4 is https://www.cloudflare.com/ips-v4 (use -ranges-url or -f
// to supply the current list instead).
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
	Loss      float64 `json:"loss"`            // 0–1
	Speed     float64 `json:"speed,omitempty"` // MB/s, when download-tested
	Colo      string  `json:"colo,omitempty"`  // CDN data centre, e.g. HKG
}

// parseRanges reads IPv4 CIDRs (or single addresses), one per line; blank
// lines and # comments are skipped.
func parseRanges(text string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "/") {
			line += "/32"
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

// blocks splits the ranges into /24 blocks (a smaller range is one block),
// without duplicates when ranges overlap.
func blocks(ranges []netip.Prefix) []netip.Prefix {
	seen := map[netip.Prefix]bool{}
	var out []netip.Prefix
	for _, p := range ranges {
		if p.Bits() >= 24 {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			continue
		}
		b := p.Addr().As4()
		start := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8
		for i := uint32(0); i < 1<<(24-p.Bits()); i++ {
			v := start + i<<8
			q := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), 0}), 24)
			if !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
	}
	return out
}

// sample picks one random address in each of n random /24 blocks (n <= 0:
// every block), the way CloudflareSpeedTest does. .0 and .255 are skipped.
func sample(ranges []netip.Prefix, n int, rng *rand.Rand) []netip.Addr {
	bs := blocks(ranges)
	rng.Shuffle(len(bs), func(i, j int) { bs[i], bs[j] = bs[j], bs[i] })
	if n > 0 && n < len(bs) {
		bs = bs[:n]
	}
	out := make([]netip.Addr, 0, len(bs))
	for _, p := range bs {
		b := p.Addr().As4()
		size := uint32(1) << (32 - p.Bits())
		base := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		v := base
		for tries := 0; tries < 8; tries++ {
			v = base + rng.Uint32N(size)
			if last := byte(v); last != 0 && last != 255 {
				break
			}
		}
		out = append(out, netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}))
	}
	return out
}

// tcpTest connects to ip:port tries times; latency is the average of the
// successful connects.
func tcpTest(ctx context.Context, ip netip.Addr, port, tries int, timeout time.Duration) Result {
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
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

// rank sorts the way the panel ranks reports: loss, then download speed
// (fastest first; untested IPs count as 0), then latency.
func rank(rs []Result) {
	slices.SortStableFunc(rs, func(a, b Result) int {
		return cmp.Or(cmp.Compare(a.Loss, b.Loss), cmp.Compare(b.Speed, a.Speed), cmp.Compare(a.LatencyMs, b.LatencyMs))
	})
}

// filter holds the -tl/-tll/-tlr/-sl/-colo limits.
type filter struct {
	MaxLatency, MinLatency, MaxLoss, MinSpeed float64
	Colos                                     map[string]bool // empty: any
}

func (f filter) latencyOK(r Result) bool {
	return r.LatencyMs <= f.MaxLatency && r.LatencyMs >= f.MinLatency && r.Loss <= f.MaxLoss
}

func (f filter) coloOK(colo string) bool {
	return len(f.Colos) == 0 || f.Colos[strings.ToUpper(colo)]
}

// parseColos reads "HKG,lax sjc" into a set.
func parseColos(s string) map[string]bool {
	out := map[string]bool{}
	for _, c := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out[strings.ToUpper(c)] = true
	}
	return out
}

// rootCAs verifies the edge's certificate (nil = the system pool; tests
// replace it).
var rootCAs *x509.CertPool

// viaIP is an HTTP client that sends every request to ip:port, with TLS
// checked against the URL's host name.
func viaIP(ip string, port int, host string, timeout time.Duration) (*http.Client, func()) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, net.JoinHostPort(ip, strconv.Itoa(port)))
		},
		TLSClientConfig:     &tls.Config{ServerName: host, RootCAs: rootCAs},
		TLSHandshakeTimeout: timeout,
		DisableCompression:  true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tr.CloseIdleConnections
}

// verify fetches https://host/cdn-cgi/trace through the IP: a working
// Cloudflare edge answers with its data centre ("colo="). An IP that only
// accepts TCP but does not serve the site is dropped.
func verify(ctx context.Context, ip, host string, port int, timeout time.Duration) (string, error) {
	cl, done := viaIP(ip, port, host, timeout)
	defer done()
	cl.Timeout = 3 * timeout
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

// minDownload is the least a download test must fetch to count.
const minDownload = 64 << 10

// download fetches rawURL through ip:port for up to limit and returns the
// speed in MB/s and the data centre from the CF-RAY header.
func download(ctx context.Context, ip string, port int, rawURL string, limit, timeout time.Duration) (float64, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, "", err
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	cl, done := viaIP(ip, port, u.Hostname(), timeout)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, limit+3*timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", "cf-ip-probe/"+version)
	resp, err := cl.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("download: status %s", resp.Status)
	}
	colo := ""
	if ray := resp.Header.Get("Cf-Ray"); strings.Contains(ray, "-") {
		colo = ray[strings.LastIndex(ray, "-")+1:]
	}
	buf := make([]byte, 32<<10)
	var n int64
	start := time.Now()
	for time.Since(start) < limit {
		m, err := resp.Body.Read(buf)
		n += int64(m)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			if n < minDownload {
				return 0, colo, err
			}
			break
		}
	}
	el := time.Since(start).Seconds()
	if n < minDownload || el <= 0 {
		return 0, colo, fmt.Errorf("download: only %d bytes", n)
	}
	return float64(n) / el / (1 << 20), colo, nil
}

// picker turns ranked TCP results into the IPs to report.
type picker struct {
	Top         int
	Host, URL   string
	Port        int
	Verify      bool
	DownloadN   int // IPs to download-test; 0 = none
	DownloadFor time.Duration
	Timeout     time.Duration
	Filter      filter
	Debug       bool
	verify      func(ctx context.Context, ip, host string, port int, timeout time.Duration) (string, error)
	download    func(ctx context.Context, ip string, port int, rawURL string, limit, timeout time.Duration) (float64, string, error)

	// What happened, for the round summary.
	checked, verified, attempted, downloaded int
	verifyErrs, downloadErrs                 errCount
}

// errCount groups failure reasons with the addresses taken out, so the
// summary can name the most common one.
type errCount map[string]int

var addrRe = regexp.MustCompile(`\d{1,3}(\.\d{1,3}){3}(:\d+)?`)

func (e *errCount) add(err error) {
	if *e == nil {
		*e = errCount{}
	}
	(*e)[addrRe.ReplaceAllString(err.Error(), "<ip>")]++
}

func (e errCount) top() string {
	best, n := "", 0
	for k, v := range e {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%s (×%d)", best, n)
}

// isTimeout reports a timed-out connection or handshake.
func isTimeout(err error) bool {
	var ne net.Error
	return err != nil && errors.As(err, &ne) && ne.Timeout()
}

// verifyWorkers checks candidates in parallel; the order of the result
// still follows the ranking.
const verifyWorkers = 16

// pick walks the candidates best first: HTTPS check (-verify), data centre
// (-colo), then a download test for the first DownloadN that pass (-sl
// drops slow ones and the next candidate is tried). An IP whose download
// test fails is still reported without a speed, unless -sl is set; after
// three failures in a row with no success the download test is given up for
// the round, since the test file (-url) is most likely unreachable. With -sl
// set only download-tested IPs are reported; otherwise the remaining slots
// are filled with checked IPs that were not download-tested.
func (p *picker) pick(ctx context.Context, cands []Result) []Result {
	if p.verify == nil {
		p.verify = verify
	}
	if p.download == nil {
		p.download = download
	}
	window := cands[:min(len(cands), p.Top*50)]
	type checkRes struct {
		colo string
		err  error
	}
	checks := make([]checkRes, len(window))
	if p.Verify {
		// Check ahead in parallel, a batch at a time, stopping once enough
		// candidates passed.
		for start := 0; start < len(window) && ctx.Err() == nil; start += verifyWorkers * 4 {
			end := min(len(window), start+verifyWorkers*4)
			var wg sync.WaitGroup
			sem := make(chan struct{}, verifyWorkers)
			for i := start; i < end; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					colo, err := p.verify(ctx, window[i].IP, p.Host, p.Port, p.Timeout)
					if isTimeout(err) && ctx.Err() == nil {
						// One more try: a lost packet on a long route is common
						// and says little about the IP.
						colo, err = p.verify(ctx, window[i].IP, p.Host, p.Port, p.Timeout)
					}
					checks[i] = checkRes{colo, err}
				}(i)
			}
			wg.Wait()
			ok := 0
			for i := 0; i < end; i++ {
				if checks[i].err == nil && p.Filter.coloOK(checks[i].colo) {
					ok++
				}
			}
			if ok >= p.Top*3 {
				window = window[:end]
				break
			}
		}
	}
	var out []Result
	tested, failsInRow := 0, 0
	downloads := p.DownloadN > 0
	for i, r := range window {
		if len(out) >= p.Top || ctx.Err() != nil {
			break
		}
		p.checked++
		if p.Verify {
			if checks[i].err != nil {
				p.verifyErrs.add(checks[i].err)
				if p.Debug {
					log.Printf("  check %s: %v", r.IP, checks[i].err)
				}
				continue
			}
			p.verified++
			r.Colo = checks[i].colo
			if !p.Filter.coloOK(r.Colo) {
				continue
			}
		}
		if downloads && tested < p.DownloadN && p.attempted < p.DownloadN*3 {
			p.attempted++
			speed, colo, err := p.download(ctx, r.IP, p.Port, p.URL, p.DownloadFor, p.Timeout)
			if err != nil {
				p.downloadErrs.add(err)
				if p.Debug {
					log.Printf("  download via %s: %v", r.IP, err)
				}
				if failsInRow++; failsInRow >= 3 && tested == 0 {
					log.Printf("download test keeps failing (%v); reporting without speeds this round — check -url, or turn it off with -dd", err)
					downloads = false
				}
				if p.Filter.MinSpeed > 0 {
					continue
				}
			} else {
				failsInRow = 0
				if r.Colo == "" {
					r.Colo = colo
				}
				if speed < p.Filter.MinSpeed || !p.Filter.coloOK(r.Colo) {
					continue
				}
				r.Speed = speed
				tested++
				p.downloaded++
			}
		} else if p.Filter.MinSpeed > 0 || !p.Filter.coloOK(r.Colo) {
			continue
		}
		out = append(out, r)
	}
	rank(out)
	return out
}

// summary is one log line on what the checks found.
func (p *picker) summary() string {
	var b strings.Builder
	if p.Verify {
		fmt.Fprintf(&b, "HTTPS check: %d of %d passed", p.verified, p.checked)
		if t := p.verifyErrs.top(); t != "" {
			fmt.Fprintf(&b, ", most common failure: %s", t)
		}
	}
	if p.attempted > 0 {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "download test: %d of %d succeeded", p.downloaded, p.attempted)
		if t := p.downloadErrs.top(); t != "" {
			fmt.Fprintf(&b, ", most common failure: %s", t)
		}
	}
	return b.String()
}

// readCSV reads a CloudflareSpeedTest result.csv: IP, sent, received,
// loss, average latency, download speed (MB/s)[, colo]. The header and
// rows that do not start with an IP are skipped.
func readCSV(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var out []Result
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(rec) < 5 {
			continue
		}
		ip, err := netip.ParseAddr(strings.TrimPrefix(strings.TrimSpace(rec[0]), "\ufeff"))
		if err != nil {
			continue
		}
		num := func(i int) float64 {
			if i >= len(rec) {
				return 0
			}
			v, _ := strconv.ParseFloat(strings.TrimSpace(rec[i]), 64)
			return v
		}
		r := Result{IP: ip.String(), Loss: min(max(num(3), 0), 1), LatencyMs: max(num(4), 0), Speed: max(num(5), 0)}
		if len(rec) > 6 {
			if c := strings.ToUpper(strings.TrimSpace(rec[6])); len(c) >= 2 && len(c) <= 5 && c != "N/A" {
				r.Colo = c
			}
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no results (expected CloudflareSpeedTest result.csv)", path)
	}
	return out, nil
}

// fromCSV applies the limits to imported results and keeps the top n.
func fromCSV(rs []Result, f filter, n int) []Result {
	var out []Result
	for _, r := range rs {
		if f.latencyOK(r) && r.Speed >= f.MinSpeed && f.coloOK(r.Colo) {
			out = append(out, r)
		}
	}
	rank(out)
	return out[:min(n, len(out))]
}
