package main

import (
	"context"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCSVReportRetriesOnlyFailedDestinations(t *testing.T) {
	old := csvReports
	defer func() { csvReports = old }()
	path := filepath.Join(t.TempDir(), "result.csv")
	if err := os.WriteFile(path, []byte("IP,Sent,Recv,Loss,Latency,Speed\n104.16.1.1,4,4,0,10,20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var a, b atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/a/") {
			a.Add(1)
		} else if b.Add(1) == 1 {
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	c := config{API: srv.URL, Token: "synthetic", Source: "test", Profiles: []string{"a", "b"}, CSV: path, Top: 10, MaxLoss: 1, MaxLatency: 1000}
	rng := rand.New(rand.NewPCG(1, 2))
	runOnce(context.Background(), c, rng)
	if a.Load() != 1 || b.Load() != 1 {
		t.Fatal(a.Load(), b.Load())
	}
	runOnce(context.Background(), c, rng)
	if a.Load() != 1 || b.Load() != 2 {
		t.Fatal("failed destination was skipped or successful destination refreshed", a.Load(), b.Load())
	}
	runOnce(context.Background(), c, rng)
	if a.Load() != 1 || b.Load() != 2 {
		t.Fatal("old measurements were reported again", a.Load(), b.Load())
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A newer measurement goes to both destinations, even when results match.
	at := st.ModTime().Add(time.Second)
	if err = os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	runOnce(context.Background(), c, rng)
	if a.Load() != 2 || b.Load() != 3 {
		t.Fatal("new measurement not sent to all profiles", a.Load(), b.Load())
	}
	// Restoring an older CSV must not make its measurement look fresh.
	if err = os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	runOnce(context.Background(), c, rng)
	if a.Load() != 2 || b.Load() != 3 {
		t.Fatal("older CSV refreshed profile timestamps")
	}
}
