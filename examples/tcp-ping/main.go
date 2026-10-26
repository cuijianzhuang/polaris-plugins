// Polaris "tcp-ping" example plugin (Go, standard library + the Go SDK).
//
// Every interval it connects over TCP from this machine to each node's first
// enabled inbound port and reports two external metrics per node:
//
//	tcp.latency   (ms)    when the connection succeeded
//	tcp.reachable (bool)  1 or 0
//
// The panel shows them in the node's "external metrics" card, labelled with
// this plugin. They never change the node's own status.
//
//	PORT=9300 go run .
package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	polaris "github.com/cuijianzhuang/polaris-plugins/sdk/go"
)

type state struct {
	APIBase      string            `json:"api_base"`
	Token        string            `json:"token"`
	PluginSecret string            `json:"plugin_secret"`
	Settings     map[string]string `json:"settings"`
}

var (
	mu       sync.Mutex
	st       state
	manifest []byte
	stateFn  = envOr("STATE_FILE", "state.json")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func load() {
	if b, err := os.ReadFile(stateFn); err == nil {
		json.Unmarshal(b, &st)
	}
}

func save() {
	b, _ := json.Marshal(st)
	if err := os.WriteFile(stateFn, b, 0o600); err != nil {
		log.Print(err)
	}
}

func current() state {
	mu.Lock()
	defer mu.Unlock()
	return st
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func readBody(r *http.Request) ([]byte, error) { return io.ReadAll(io.LimitReader(r.Body, 1<<20)) }

// settings arrive as strings; numbers are sent as text too.
func parseSettings(raw json.RawMessage) map[string]string {
	var in map[string]any
	json.Unmarshal(raw, &in)
	out := map[string]string{}
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(x)
		}
	}
	return out
}

func handler() http.Handler {
	installKey := os.Getenv("INSTALL_KEY")
	m := http.NewServeMux()
	m.HandleFunc("/polaris-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(manifest)
	})
	m.HandleFunc("/polaris/health", func(w http.ResponseWriter, r *http.Request) {
		if current().Token == "" {
			reply(w, 503, map[string]bool{"ok": false})
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("/polaris/install", func(w http.ResponseWriter, r *http.Request) {
		if installKey != "" && r.Header.Get("X-Polaris-Install-Key") != installKey {
			reply(w, 403, map[string]string{"error": "bad install key"})
			return
		}
		raw, _ := readBody(r)
		var b struct {
			state
			Settings json.RawMessage `json:"settings"`
		}
		if json.Unmarshal(raw, &b) != nil || polaris.ParseSigned(r.Header.Get("X-Polaris-Signature"), b.PluginSecret, raw, time.Now(), &b) != nil {
			reply(w, 401, map[string]string{"error": "bad signature"})
			return
		}
		mu.Lock()
		st = state{APIBase: b.APIBase, Token: b.Token, PluginSecret: b.PluginSecret, Settings: parseSettings(b.Settings)}
		save()
		s := st
		mu.Unlock()
		reply(w, 200, map[string]bool{"ok": true})
		go round(s) // first readings right away instead of after one interval
	})
	// signed: config pushes and uninstall use the stored plugin secret.
	signed := func(fn func(raw []byte)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, _ := readBody(r)
			if !polaris.VerifySignature(r.Header.Get("X-Polaris-Signature"), current().PluginSecret, raw, time.Now()) {
				reply(w, 401, map[string]string{"error": "bad signature"})
				return
			}
			fn(raw)
			reply(w, 200, map[string]bool{"ok": true})
		}
	}
	m.HandleFunc("/polaris/config", signed(func(raw []byte) {
		var b struct {
			Settings json.RawMessage `json:"settings"`
		}
		json.Unmarshal(raw, &b)
		mu.Lock()
		st.Settings = parseSettings(b.Settings)
		save()
		mu.Unlock()
	}))
	m.HandleFunc("/polaris/uninstall", signed(func([]byte) {
		mu.Lock()
		st = state{}
		save()
		mu.Unlock()
	}))
	return m
}

type node struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	PublicIPv4 string `json:"publicIpv4"`
}

type inbound struct {
	NodeID      string `json:"nodeId"`
	Port        int    `json:"port"`
	Enabled     bool   `json:"enabled"`
	ServiceOnly bool   `json:"serviceOnly"`
}

// targets maps node id -> host:port to dial.
func targets(c *polaris.Client) (map[string]string, error) {
	var ns []node
	var ins []inbound
	if err := c.Nodes(&ns); err != nil {
		return nil, err
	}
	if err := c.Do("GET", "/inbounds", nil, &ins, nil); err != nil {
		return nil, err
	}
	port := map[string]int{}
	for _, in := range ins {
		if in.Enabled && !in.ServiceOnly && in.Port > 0 && port[in.NodeID] == 0 {
			port[in.NodeID] = in.Port
		}
	}
	out := map[string]string{}
	for _, n := range ns {
		host := n.Address
		if host == "" {
			host = n.PublicIPv4
		}
		if host != "" && port[n.ID] > 0 {
			out[n.ID] = net.JoinHostPort(host, strconv.Itoa(port[n.ID]))
		}
	}
	return out, nil
}

func probe(addr string) (time.Duration, bool) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return 0, false
	}
	conn.Close()
	return time.Since(start), true
}

func round(s state) {
	c := polaris.NewClient(s.APIBase, s.Token)
	ts, err := targets(c)
	if err != nil {
		log.Printf("list nodes: %v", err)
		return
	}
	site := s.Settings["site"]
	if site == "" {
		site = "插件"
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for id, addr := range ts {
		wg.Add(1)
		go func(id, addr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, ok := probe(addr)
			ms := []polaris.Metric{{Name: "tcp.reachable", Label: site + " 可达", Unit: "bool"}}
			if ok {
				ms[0].Value = 1
				ms = append(ms, polaris.Metric{Name: "tcp.latency", Label: site + " TCP 延迟", Unit: "ms", Value: float64(d.Microseconds()) / 1000})
			}
			for _, m := range ms {
				if err := polaris.CheckMetric(m, time.Now()); err != nil {
					log.Printf("metric %s: %v", m.Name, err)
					return
				}
			}
			if err := c.ReportMetrics(id, ms, nil); err != nil {
				log.Printf("report %s: %v", id, err)
			}
		}(id, addr)
	}
	wg.Wait()
}

func loop() {
	for {
		s := current()
		interval := 60
		if n, err := strconv.Atoi(s.Settings["interval"]); err == nil && n > 0 {
			interval = n
		}
		interval = max(interval, 30)
		if s.Token != "" {
			round(s)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

func main() {
	exe, _ := os.Executable()
	var err error
	if manifest, err = os.ReadFile("polaris-plugin.json"); err != nil {
		if manifest, err = os.ReadFile(filepath.Join(filepath.Dir(exe), "polaris-plugin.json")); err != nil {
			log.Fatal("polaris-plugin.json not found")
		}
	}
	load()
	go loop()
	addr := ":" + envOr("PORT", "9300")
	log.Printf("tcp-ping plugin on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler()))
}
