// Package polaris is the Go SDK for Polaris plugins: it verifies what the
// panel sends (webhook signatures, identity tokens) and wraps the panel API
// calls a plugin typically makes. Standard library only.
package polaris

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Tolerance is the clock skew accepted on signatures.
const Tolerance = 300 * time.Second

// Sign returns hex HMAC-SHA256(secret, "<ts>.<body>").
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// SignatureHeader builds the X-Polaris-Signature value for body.
func SignatureHeader(secret string, body []byte, now time.Time) string {
	ts := now.Unix()
	return fmt.Sprintf("t=%d,v1=%s", ts, Sign(secret, ts, body))
}

var sigRe = regexp.MustCompile(`^t=(\d+),v1=([0-9a-f]{64})$`)

// VerifySignature checks an X-Polaris-Signature header against the RAW
// request body (never a re-serialised copy).
func VerifySignature(header, secret string, body []byte, now time.Time) bool {
	m := sigRe.FindStringSubmatch(header)
	if m == nil || secret == "" {
		return false
	}
	ts, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(ts, 0)); d > Tolerance || d < -Tolerance {
		return false
	}
	return hmac.Equal([]byte(Sign(secret, ts, body)), []byte(m[2]))
}

// Claims are the fields of the identity token the panel appends to page and
// portal URLs as ?polaris_token=.
type Claims struct {
	Issuer   string `json:"iss"`
	Audience string `json:"aud"` // the plugin id
	Subject  string `json:"sub"` // admin name, or user id for scope "user"
	Name     string `json:"name"`
	Scope    string `json:"scope"` // "admin" or "user"
	ID       string `json:"jti"`
	Issued   int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

// VerifyIdentity validates an HS256 identity token signed with the plugin
// secret. audience and scope are required matches (empty = don't check).
func VerifyIdentity(token, secret, audience, scope string, now time.Time) (*Claims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || secret == "" {
		return nil, false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal([]byte(base64.RawURLEncoding.EncodeToString(m.Sum(nil))), []byte(parts[2])) {
		return nil, false
	}
	var head struct {
		Alg string `json:"alg"`
	}
	hb, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	pb, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil || json.Unmarshal(hb, &head) != nil || head.Alg != "HS256" {
		return nil, false
	}
	var c Claims
	if json.Unmarshal(pb, &c) != nil || c.Issuer != "polaris" || c.Expires < now.Unix() {
		return nil, false
	}
	if (audience != "" && c.Audience != audience) || (scope != "" && c.Scope != scope) {
		return nil, false
	}
	return &c, true
}

// Client calls the panel API with a plugin token.
type Client struct {
	APIBase string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client for the api_base and token the panel gave the
// plugin at install.
func NewClient(apiBase, token string) *Client {
	return &Client{APIBase: strings.TrimRight(apiBase, "/"), Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Error is a non-2xx answer from the panel.
type Error struct {
	Status int
	Body   string
}

func (e *Error) Error() string { return fmt.Sprintf("polaris %d: %s", e.Status, e.Body) }

// Do sends a request and decodes the JSON answer into out (may be nil).
func (c *Client) Do(method, path string, in, out any, header map[string]string) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.APIBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	h := c.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	resp, err := h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: resp.StatusCode, Body: string(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func idem(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Idempotency-Key": key}
}

// Renew extends an account by days. Use a stable idempotencyKey per order so
// a retried payment callback renews once.
func (c *Client) Renew(userID string, days int, resetTraffic bool, idempotencyKey string, out any) error {
	return c.Do("POST", "/users/"+url.PathEscape(userID)+"/renew",
		map[string]any{"days": days, "resetTraffic": resetTraffic}, out, idem(idempotencyKey))
}

// AddTraffic adds gb to the account's quota.
func (c *Client) AddTraffic(userID string, gb float64, idempotencyKey string, out any) error {
	return c.Do("POST", "/users/"+url.PathEscape(userID)+"/add-traffic", map[string]any{"gb": gb}, out, idem(idempotencyKey))
}

// Config reads this plugin's settings (plugin token only).
func (c *Client) Config(out any) error { return c.Do("GET", "/plugin/config", nil, out, nil) }
