package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func Signature(secret string, body []byte, now time.Time) string {
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
func VerifySignature(secret, header string, body []byte, now time.Time) bool {
	parts := strings.Split(header, ",")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "t=") || !strings.HasPrefix(parts[1], "v1=") {
		return false
	}
	seconds, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "t="), 10, 64)
	if err != nil {
		return false
	}
	sent := time.Unix(seconds, 0)
	if now.Sub(sent) > 5*time.Minute || sent.Sub(now) > 5*time.Minute {
		return false
	}
	return hmac.Equal([]byte(header), []byte(Signature(secret, body, sent)))
}

type Credentials struct {
	Installation
	ControlSecret string `json:"control_secret"`
}
type HealthRequest struct {
	InstallID string `json:"install_id"`
	Nonce     string `json:"nonce"`
}
type HealthReply struct {
	HealthRequest
	Status string `json:"status"`
}

// Service provides durable, explicitly authorized pairing and signed health.
// Start your own process; the panel never executes or uploads plugin programs.
type Service struct {
	manifest    Manifest
	code, path  string
	mu          sync.RWMutex
	credentials Credentials
}

func NewService(manifest Manifest, pairingCode, statePath string) (*Service, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if len(pairingCode) < 16 || len(pairingCode) > 256 {
		return nil, errors.New("pairing code must contain 16-256 bytes")
	}
	if statePath == "" {
		return nil, errors.New("state path required")
	}
	s := &Service{manifest: manifest, code: pairingCode, path: statePath}
	data, err := os.ReadFile(statePath)
	if err == nil {
		if err := json.Unmarshal(data, &s.credentials); err != nil {
			return nil, errors.New("invalid saved credentials")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}
func (s *Service) Credentials() Credentials { s.mu.RLock(); defer s.mu.RUnlock(); return s.credentials }
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /polaris-plugin.json", func(w http.ResponseWriter, r *http.Request) { reply(w, s.manifest) })
	mux.HandleFunc("POST /polaris/install", s.install)
	mux.HandleFunc("POST /polaris/health", s.health)
	return mux
}
func reply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(value)
}
func readBody(w http.ResponseWriter, r *http.Request, out any) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return raw, nil
}
func (s *Service) install(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.code)) != 1 {
		http.Error(w, "pairing authorization required", http.StatusUnauthorized)
		return
	}
	var next Credentials
	if _, err := readBody(w, r, &next); err != nil {
		http.Error(w, "invalid installation", 400)
		return
	}
	u, err := url.Parse(next.APIBase)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || next.ProtocolVersion != HostAPI || len(next.InstallID) != 16 || len(next.Nonce) < 16 || len(next.Nonce) > 128 || !strings.HasPrefix(next.Token, "plt_") || len(next.ControlSecret) < 32 {
		http.Error(w, "invalid installation", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials.InstallID != "" && s.credentials.InstallID != next.InstallID {
		http.Error(w, "already paired; reset local state explicitly before a different installation", 409)
		return
	}
	if s.credentials.Nonce == next.Nonce && s.credentials != next {
		http.Error(w, "installation nonce conflict", 409)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		http.Error(w, "cannot persist installation", 500)
		return
	}
	data, _ := json.Marshal(next)
	file, err := os.CreateTemp(filepath.Dir(s.path), ".polaris-pair-*")
	if err != nil {
		http.Error(w, "cannot persist installation", 500)
		return
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.path)
	}
	if err != nil {
		http.Error(w, "cannot persist installation", 500)
		return
	}
	s.credentials = next
	reply(w, InstallAck{next.InstallID, next.Nonce})
}
func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	var req HealthRequest
	raw, err := readBody(w, r, &req)
	if err != nil {
		http.Error(w, "invalid health request", 400)
		return
	}
	credentials := s.Credentials()
	if credentials.InstallID == "" || req.InstallID != credentials.InstallID || len(req.Nonce) < 16 || len(req.Nonce) > 128 || !VerifySignature(credentials.ControlSecret, r.Header.Get("X-Polaris-Signature"), raw, time.Now()) {
		http.Error(w, "invalid health signature", 401)
		return
	}
	reply(w, HealthReply{req, "ok"})
}

// Get calls the paired panel API using this installation's scoped token.
func (s *Service) Get(ctx context.Context, path string, out any) error {
	credentials := s.Credentials()
	if credentials.InstallID == "" {
		return errors.New("not paired")
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#%\\") || strings.Contains(path, "..") || strings.Contains(path, "//") {
		return errors.New("invalid API path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(credentials.APIBase, "/")+path, nil)
	if err != nil {
		return errors.New("invalid panel API base")
	}
	req.Header.Set("Authorization", "Bearer "+credentials.Token)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("panel request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("panel returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("panel response too large or incomplete")
	}
	return json.Unmarshal(data, out)
}
