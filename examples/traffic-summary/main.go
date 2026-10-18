// Traffic summary is a runnable external plugin with no third-party dependencies.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cuijianzhuang/polaris-plugins/plugin"
)

func main() {
	code := os.Getenv("POLARIS_PLUGIN_PAIRING_CODE")
	state := os.Getenv("POLARIS_PLUGIN_STATE")
	if state == "" {
		state = "./data/installation.json"
	}
	raw, err := os.ReadFile("examples/traffic-summary/polaris-plugin.json")
	if err != nil {
		log.Fatal("run this command from the repository root")
	}
	manifest, err := plugin.DecodeManifest(raw)
	if err != nil {
		log.Fatal(err)
	}
	service, err := plugin.NewService(manifest, code, state)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", service.Handler())
	mux.HandleFunc("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+code)) != 1 {
			http.Error(w, "authentication required", 401)
			return
		}
		var summary any
		if err := service.Get(r.Context(), "/stats/overview", &summary); err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(summary)
	})
	address := os.Getenv("POLARIS_PLUGIN_LISTEN")
	if address == "" {
		address = "127.0.0.1:9070"
	}
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	log.Printf("traffic-summary listening on %s; pairing code and credentials are not logged", address)
	log.Fatal(server.ListenAndServe())
}
