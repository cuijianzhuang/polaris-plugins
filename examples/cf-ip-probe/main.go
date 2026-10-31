// cf-ip-probe is a speed-test point for Polaris CDN profiles.
//
// Run it on a machine where your users are (a home router, a Raspberry Pi,
// a domestic VPS; ideally one per carrier). Every round it tests one IP in
// each Cloudflare /24 for TCP latency and loss, checks the best ones really
// serve HTTP(S) (and which data centre answers), download-tests the first
// few, and reports the top N to the panel. It can also report a
// CloudflareSpeedTest result.csv instead of testing (-csv).
//
//	POST {api}/cdn-profiles/{profile}/preferred-ips   (token scope cdn:ips:write)
//
//	cf-ip-probe -api https://panel.example.com/admin/api/v1 -token plt_… \
//	  -profile <id> -source 上海电信
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

type config struct {
	API, Token, Source, Carrier, Region, Host, RangesURL, RangesFile, URL, CSV, Colos, Scheme string
	Profiles                                                                                  []string
	Port, Sample, Tries, Workers, Top, DownloadN                                              int
	Timeout, Interval, DownloadFor                                                            time.Duration
	MaxLatency, MinLatency, MaxLoss, MinSpeed                                                 float64
	Verify, NoDownload, Once, DryRun, Version, Debug, Candidates                              bool
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func parseFlags(args []string) (config, error) {
	var c config
	var profiles string
	fs := flag.NewFlagSet("cf-ip-probe", flag.ContinueOnError)
	fs.StringVar(&c.API, "api", envOr("POLARIS_API", ""), "panel API base, e.g. https://panel.example.com/admin/api/v1 (env POLARIS_API)")
	fs.StringVar(&c.Token, "token", envOr("POLARIS_TOKEN", ""), "API token with scope cdn:ips:write (env POLARIS_TOKEN)")
	fs.StringVar(&profiles, "profile", envOr("POLARIS_CDN_PROFILE", ""), "CDN profile id(s), comma separated (env POLARIS_CDN_PROFILE)")
	fs.StringVar(&c.Source, "source", envOr("PROBE_SOURCE", ""), "name of this speed-test point, e.g. 上海电信 (env PROBE_SOURCE)")
	fs.StringVar(&c.Carrier, "carrier", envOr("PROBE_CARRIER", ""), "this point's carrier: ct (Telecom), cu (Unicom), cm (Mobile); empty lets the panel read it from -source (env PROBE_CARRIER)")
	fs.StringVar(&c.Region, "region", envOr("PROBE_REGION", ""), "province/region of this measurement point, e.g. CN-31 or 上海 (env PROBE_REGION)")
	fs.StringVar(&c.Host, "host", envOr("PROBE_HOST", "www.cloudflare.com"), "a site on Cloudflare used to check that an IP serves the selected protocol; your CDN domain works best (env PROBE_HOST)")
	fs.StringVar(&c.RangesURL, "ranges-url", "", "fetch IPv4 ranges from this URL (e.g. https://www.cloudflare.com/ips-v4) instead of the built-in list")
	fs.StringVar(&c.RangesFile, "f", "", "read IPv4 ranges from this file (one CIDR or IP per line), like CloudflareSpeedTest's ip.txt")
	fs.StringVar(&c.Scheme, "scheme", "https", "client protocol to test: http or https")
	fs.IntVar(&c.Port, "port", 0, "actual test port (default HTTPS:443, HTTP:80)")
	fs.IntVar(&c.Sample, "sample", 0, "/24 blocks to test per round, one random IP each; 0 = every block")
	fs.IntVar(&c.Tries, "tries", 4, "TCP connects per IP")
	fs.IntVar(&c.Workers, "workers", 200, "concurrent latency tests (lower it on a router)")
	fs.IntVar(&c.Top, "top", 10, "IPs to report")
	fs.DurationVar(&c.Timeout, "timeout", time.Second, "per-connect timeout")
	fs.Float64Var(&c.MaxLatency, "tl", 9999, "drop IPs with average latency above this (ms)")
	fs.Float64Var(&c.MinLatency, "tll", 0, "drop IPs with average latency below this (ms)")
	fs.Float64Var(&c.MaxLoss, "tlr", 1, "drop IPs with loss above this (0–1; 0 = no loss allowed)")
	fs.StringVar(&c.Colos, "colo", "", "keep only these data centres, e.g. HKG,NRT,LAX")
	fs.BoolVar(&c.NoDownload, "dd", false, "skip the download test (rank by latency only)")
	fs.IntVar(&c.DownloadN, "dn", 5, "IPs to download-test, best first")
	fs.DurationVar(&c.DownloadFor, "dt", 5*time.Second, "download test length per IP")
	fs.Float64Var(&c.MinSpeed, "sl", 0, "drop IPs slower than this (MB/s); when set, only download-tested IPs are reported")
	fs.StringVar(&c.URL, "url", envOr("PROBE_URL", "https://speed.cloudflare.com/__down?bytes=50000000"), "download test file; a large file on your own Cloudflare site is most reliable (env PROBE_URL)")
	fs.StringVar(&c.CSV, "csv", "", "report a CloudflareSpeedTest result.csv instead of testing (re-read every round)")
	fs.DurationVar(&c.Interval, "interval", time.Hour, "time between rounds")
	fs.BoolVar(&c.Verify, "verify", true, "check each IP serves the selected protocol through /cdn-cgi/trace (gives the data centre)")
	fs.BoolVar(&c.Once, "once", false, "run one round and exit")
	fs.BoolVar(&c.DryRun, "dry-run", false, "print the result instead of reporting it")
	fs.BoolVar(&c.Version, "version", false, "print the version and exit")
	fs.BoolVar(&c.Debug, "debug", false, "log why each IP failed the HTTP(S) check or download test")
	fs.BoolVar(&c.Candidates, "candidates", true, "also test the extra IPs the panel offers (third-party reverse proxies found with Shodan, when turned on in the panel)")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.Version {
		return c, nil
	}
	for _, p := range strings.Split(profiles, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.Profiles = append(c.Profiles, p)
		}
	}
	if c.NoDownload {
		c.DownloadN = 0
	}
	switch c.Carrier = strings.ToLower(strings.TrimSpace(c.Carrier)); c.Carrier {
	case "", "ct", "cu", "cm":
	default:
		return c, fmt.Errorf("-carrier must be ct, cu, cm or empty, not %q", c.Carrier)
	}
	switch {
	case c.Source == "":
		return c, errors.New("-source is required, e.g. -source 上海电信")
	case c.DryRun:
	case c.API == "" || c.Token == "" || len(c.Profiles) == 0:
		return c, errors.New("-api, -token and -profile are required (or use -dry-run)")
	}
	c.Scheme = strings.ToLower(c.Scheme)
	if c.Scheme != "http" && c.Scheme != "https" {
		return c, errors.New("-scheme must be http or https")
	}
	portSet, urlSet := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
		if f.Name == "url" {
			urlSet = true
		}
	})
	if !portSet {
		c.Port = 443
		if c.Scheme == "http" {
			c.Port = 80
		}
	}
	if !urlSet && os.Getenv("PROBE_URL") == "" {
		c.URL = c.Scheme + "://speed.cloudflare.com/__down?bytes=50000000"
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, errors.New("-port must be between 1 and 65535")
	}
	if c.Tries < 1 || c.Top < 1 || c.Top > 200 || c.Workers < 1 || c.Workers > 1000 || c.Sample < 0 || c.DownloadN < 0 {
		return c, errors.New("need tries >= 1, 1 <= top <= 200, 1 <= workers <= 1000, sample >= 0, dn >= 0")
	}
	if c.MaxLoss < 0 || c.MaxLoss > 1 || c.MinLatency > c.MaxLatency || c.MinSpeed < 0 {
		return c, errors.New("need 0 <= tlr <= 1, tll <= tl, sl >= 0")
	}
	if c.CSV == "" {
		if c.Colos != "" && !c.Verify && c.DownloadN == 0 {
			return c, errors.New("-colo needs the HTTP(S) check (-verify) or the download test to learn each IP's data centre")
		}
		if c.MinSpeed > 0 && c.DownloadN == 0 {
			return c, errors.New("-sl needs the download test (drop -dd or raise -dn)")
		}
		if c.DownloadN > 0 {
			if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.Scheme != c.Scheme {
				return c, fmt.Errorf("-url %q must use the selected -scheme", c.URL)
			}
			if c.DownloadFor < time.Second {
				return c, errors.New("-dt must be at least 1s")
			}
		}
	}
	if c.Interval < 10*time.Minute && !c.Once {
		return c, errors.New("-interval must be at least 10m")
	}
	return c, nil
}

func (c config) filter() filter {
	return filter{MaxLatency: c.MaxLatency, MinLatency: c.MinLatency, MaxLoss: c.MaxLoss, MinSpeed: c.MinSpeed, Colos: parseColos(c.Colos)}
}

func ranges(ctx context.Context, c config) ([]netip.Prefix, error) {
	if c.RangesFile != "" {
		b, err := os.ReadFile(c.RangesFile)
		if err != nil {
			return nil, err
		}
		return parseRanges(string(b))
	}
	if c.RangesURL == "" {
		return parseRanges(strings.Join(cloudflareV4, "\n"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.RangesURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ranges: %s", resp.Status)
	}
	return parseRanges(string(b))
}

// round measures once and returns the IPs to report, best first.
func round(ctx context.Context, c config, rng *rand.Rand) ([]Result, error) {
	f := c.filter()
	if c.CSV != "" {
		rs, err := readCSV(c.CSV)
		if err != nil {
			return nil, err
		}
		out := fromCSV(rs, f, c.Top)
		log.Printf("read %d results from %s, %d pass the limits", len(rs), c.CSV, len(out))
		return out, nil
	}
	rs, err := ranges(ctx, c)
	if err != nil {
		return nil, err
	}
	ips := sample(rs, c.Sample, rng)
	if c.Candidates && c.CSV == "" {
		extra := candidates(ctx, c)
		ips = addMissing(ips, extra)
		if len(extra) > 0 {
			log.Printf("panel offered %d extra candidate IPs", len(extra))
		}
	}
	tested := testAll(ctx, ips, c.Port, c.Tries, c.Workers, c.Timeout)
	var cands []Result
	for _, r := range tested {
		if f.latencyOK(r) {
			cands = append(cands, r)
		}
	}
	log.Printf("tested %d IPs, %d answered, %d within the latency/loss limits", len(ips), len(tested), len(cands))
	p := &picker{Scheme: c.Scheme, Top: c.Top, Host: c.Host, URL: c.URL, Port: c.Port, Verify: c.Verify, DownloadN: c.DownloadN,
		DownloadFor: c.DownloadFor, Timeout: 2 * c.Timeout, Filter: f, Debug: c.Debug}
	out := p.pick(ctx, cands)
	if s := p.summary(); s != "" {
		log.Print(s)
	}
	return out, nil
}

// maxCandidates caps what the panel may add to a round.
const maxCandidates = 500

// candidates fetches the extra IPs the panel offers for the profiles (for
// example third-party reverse proxies). A panel without the endpoint, or
// any error, just means none: the round goes on with Cloudflare's ranges.
func candidates(ctx context.Context, c config) []netip.Addr {
	if c.API == "" || c.Token == "" {
		return nil
	}
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, p := range c.Profiles {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.API, "/")+"/cdn-profiles/"+url.PathEscape(p)+"/ip-candidates", nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			if c.Debug {
				log.Printf("candidates for profile %s: %v", p, err)
			}
			continue
		}
		var body struct {
			IPs []string `json:"ips"`
		}
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
		} else if c.Debug {
			log.Printf("candidates for profile %s: %s", p, resp.Status)
		}
		resp.Body.Close()
		cancel()
		for _, s := range body.IPs {
			a, err := netip.ParseAddr(strings.TrimSpace(s))
			if err != nil || !a.Unmap().Is4() || seen[a.Unmap()] || len(out) >= maxCandidates {
				continue
			}
			seen[a.Unmap()] = true
			out = append(out, a.Unmap())
		}
	}
	return out
}

// addMissing appends the extra addresses not already in ips.
func addMissing(ips, extra []netip.Addr) []netip.Addr {
	have := make(map[netip.Addr]bool, len(ips))
	for _, a := range ips {
		have[a] = true
	}
	for _, a := range extra {
		if !have[a] {
			have[a] = true
			ips = append(ips, a)
		}
	}
	return ips
}

// report sends the result to one profile.
func report(ctx context.Context, c config, profile string, rs []Result) error {
	port := c.Port
	if port == 0 {
		port = 443
		if c.Scheme == "http" {
			port = 80
		}
	}
	scheme := c.Scheme
	if scheme == "" {
		scheme = "https"
	}
	payload := map[string]any{"region": c.Region, "source": c.Source, "ips": rs, "port": port, "scheme": scheme}
	if c.Carrier != "" {
		payload["carrier"] = c.Carrier
	}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(c.API, "/") + "/cdn-profiles/" + profile + "/preferred-ips"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("panel answered %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// Track successful destinations separately: a partial outage must not make
// failed profiles look delivered. Completed profiles never receive the same
// measurement again just because another destination needs a retry.
var csvReports struct {
	path      string
	mod       time.Time
	delivered map[string]bool
}

func runOnce(ctx context.Context, c config, rng *rand.Rand) {
	var mod time.Time
	if c.CSV != "" {
		st, err := os.Stat(c.CSV)
		if err != nil {
			log.Printf("round failed: %v", err)
			return
		}
		mod = st.ModTime()
		if csvReports.path == c.CSV && mod.Before(csvReports.mod) {
			log.Printf("%s is older than the last measurement; skipping", c.CSV)
			return
		}
		if csvReports.path != c.CSV || !mod.Equal(csvReports.mod) {
			csvReports.path, csvReports.mod, csvReports.delivered = c.CSV, mod, map[string]bool{}
		}
		pending := false
		for _, p := range c.Profiles {
			if !csvReports.delivered[p] {
				pending = true
				break
			}
		}
		if !pending {
			log.Printf("%s has not changed and all profiles received it; skipping", c.CSV)
			return
		}
	}
	rs, err := round(ctx, c, rng)
	if err != nil {
		log.Printf("round failed: %v", err)
		return
	}
	if len(rs) == 0 {
		log.Printf("no usable IP this round; nothing reported (the panel keeps the last list)")
		return
	}
	for _, r := range rs {
		speed := "      -"
		if r.Speed > 0 {
			speed = fmt.Sprintf("%5.1f MB/s", r.Speed)
		}
		log.Printf("  %-15s %6.1f ms  loss %3.0f%%  %s  %s", r.IP, r.LatencyMs, r.Loss*100, speed, r.Colo)
	}
	if c.DryRun {
		return
	}
	for _, p := range c.Profiles {
		if c.CSV != "" && csvReports.delivered[p] {
			continue
		}
		if err := report(ctx, c, p, rs); err != nil {
			log.Printf("report to profile %s: %v", p, err)
		} else {
			if c.CSV != "" {
				csvReports.delivered[p] = true
			}
			log.Printf("reported %d IPs to profile %s", len(rs), p)
		}
	}
}

func main() {
	c, err := parseFlags(os.Args[1:])
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(2)
	}
	if c.Version {
		fmt.Println("cf-ip-probe", version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x9e3779b97f4a7c15))
	for {
		runOnce(ctx, c, rng)
		if c.Once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}
