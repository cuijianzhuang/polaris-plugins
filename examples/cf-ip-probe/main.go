// cf-ip-probe is a speed-test point for Polaris CDN profiles.
//
// Run it on a machine where your users are (a home router, a Raspberry Pi,
// a domestic VPS; ideally one per carrier). Every round it samples IPs from
// Cloudflare's ranges, measures TCP latency and loss from here, checks the
// best ones really serve HTTPS, and reports the top N to the panel:
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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type config struct {
	API, Token, Source, Host, RangesURL string
	Profiles                            []string
	Port, Sample, Tries, Workers, Top   int
	Timeout, Interval                   time.Duration
	Verify, Once, DryRun                bool
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
	fs.StringVar(&c.Host, "host", envOr("PROBE_HOST", "www.cloudflare.com"), "a site on Cloudflare used to check that an IP serves HTTPS; your CDN domain works best")
	fs.StringVar(&c.RangesURL, "ranges-url", "", "fetch IPv4 ranges from this URL (e.g. https://www.cloudflare.com/ips-v4) instead of the built-in list")
	fs.IntVar(&c.Port, "port", 443, "port to test")
	fs.IntVar(&c.Sample, "sample", 600, "IPs to sample per round")
	fs.IntVar(&c.Tries, "tries", 4, "TCP connects per IP")
	fs.IntVar(&c.Workers, "workers", 64, "concurrent tests")
	fs.IntVar(&c.Top, "top", 10, "IPs to report")
	fs.DurationVar(&c.Timeout, "timeout", time.Second, "per-connect timeout")
	fs.DurationVar(&c.Interval, "interval", time.Hour, "time between rounds")
	fs.BoolVar(&c.Verify, "verify", true, "check the best IPs serve HTTPS through /cdn-cgi/trace")
	fs.BoolVar(&c.Once, "once", false, "run one round and exit")
	fs.BoolVar(&c.DryRun, "dry-run", false, "print the result instead of reporting it")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	for _, p := range strings.Split(profiles, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.Profiles = append(c.Profiles, p)
		}
	}
	switch {
	case c.Source == "":
		return c, errors.New("-source is required, e.g. -source 上海电信")
	case c.DryRun:
	case c.API == "" || c.Token == "" || len(c.Profiles) == 0:
		return c, errors.New("-api, -token and -profile are required (or use -dry-run)")
	}
	if c.Tries < 1 || c.Top < 1 || c.Top > 200 || c.Sample < c.Top || c.Workers < 1 {
		return c, errors.New("need tries >= 1, 1 <= top <= 200, sample >= top, workers >= 1")
	}
	if c.Interval < 10*time.Minute && !c.Once {
		return c, errors.New("-interval must be at least 10m")
	}
	return c, nil
}

func ranges(ctx context.Context, c config) ([]netip.Prefix, error) {
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
	rs, err := ranges(ctx, c)
	if err != nil {
		return nil, err
	}
	ips := sample(rs, c.Sample, rng)
	tested := testAll(ctx, ips, c.Port, c.Tries, c.Workers, c.Timeout)
	log.Printf("tested %d IPs, %d answered", len(ips), len(tested))
	if !c.Verify {
		return tested[:min(c.Top, len(tested))], nil
	}
	// Check the best candidates in order until enough pass.
	return verifyTop(ctx, tested[:min(len(tested), c.Top*4)], c.Top, c.Host, c.Port, 2*c.Timeout), nil
}

// report sends the result to one profile.
func report(ctx context.Context, c config, profile string, rs []Result) error {
	body, _ := json.Marshal(map[string]any{"source": c.Source, "ips": rs})
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

func runOnce(ctx context.Context, c config, rng *rand.Rand) {
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
		log.Printf("  %-15s %6.1f ms  loss %3.0f%%  %s", r.IP, r.LatencyMs, r.Loss*100, r.Colo)
	}
	if c.DryRun {
		return
	}
	for _, p := range c.Profiles {
		if err := report(ctx, c, p, rs); err != nil {
			log.Printf("report to profile %s: %v", p, err)
		} else {
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
