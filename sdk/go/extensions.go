package polaris

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Subscription extensions and node metrics (protocol stage 4).

// SubContentTypes are the content types a subscription format may declare.
var SubContentTypes = []string{"text/plain", "text/yaml", "application/yaml", "application/x-yaml", "application/json", "application/xml", "text/xml"}

// MetricUnits are the units a reported metric may use.
var MetricUnits = []string{"ms", "s", "Mbps", "kbps", "%", "count", "bool"}

// MetricReserved are names kept for the node's own figures.
var MetricReserved = []string{"cpu", "mem", "memory", "rx", "tx", "load", "online", "status", "heartbeat", "traffic", "up", "down"}

var metricNameRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,39}$`)

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// RenderUser is the account summary sent to /polaris/render.
type RenderUser struct {
	Username    string     `json:"username"`
	Title       string     `json:"title"`
	Upload      int64      `json:"upload"`
	Download    int64      `json:"download"`
	Total       int64      `json:"total"` // 0 = unlimited
	Expire      *time.Time `json:"expire,omitempty"`
	UpdateHours int        `json:"updateHours,omitempty"`
}

// RenderRequest is the body of POST /polaris/render. Nodes carry connection
// credentials and keep the panel's own field names, so they are left raw.
type RenderRequest struct {
	Format string                       `json:"format"`
	User   RenderUser                   `json:"user"`
	Nodes  []map[string]json.RawMessage `json:"nodes"`
}

// TransformNode is one node as /polaris/transform sees it (no credentials).
type TransformNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Region   string `json:"region,omitempty"`
	Source   string `json:"source,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Server   string `json:"server,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// TransformRequest is the body of POST /polaris/transform.
type TransformRequest struct {
	Format string          `json:"format"`
	User   string          `json:"user"`
	Nodes  []TransformNode `json:"nodes"`
}

// TransformPick keeps a node, optionally renamed. The answer's order is the
// new order; nodes left out are dropped.
type TransformPick struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// TransformResponse is the answer to /polaris/transform.
type TransformResponse struct {
	Nodes []TransformPick `json:"nodes"`
}

// ParseSigned verifies a signed panel request and decodes its JSON body.
func ParseSigned(header, secret string, body []byte, now time.Time, out any) error {
	if !VerifySignature(header, secret, body, now) {
		return errors.New("bad signature")
	}
	return json.Unmarshal(body, out)
}

// CheckTransform applies the panel's rules to a /polaris/transform answer
// given as raw JSON. nil means the panel would accept it; the panel skips a
// plugin whose answer breaks a rule.
func CheckTransform(req TransformRequest, answer []byte) error {
	var rep struct {
		Nodes *[]map[string]json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(answer, &rep) != nil || rep.Nodes == nil {
		return errors.New(`answer must be {"nodes": [...]}`)
	}
	ids := map[string]bool{}
	for _, n := range req.Nodes {
		ids[n.ID] = true
	}
	seen := map[string]bool{}
	for _, n := range *rep.Nodes {
		var id string
		if json.Unmarshal(n["id"], &id) != nil || !ids[id] {
			return fmt.Errorf("unknown node id %s", n["id"])
		}
		if seen[id] {
			return fmt.Errorf("node id %s listed twice", id)
		}
		seen[id] = true
		raw, ok := n["name"]
		if !ok || string(raw) == "null" {
			continue
		}
		var name string
		if json.Unmarshal(raw, &name) != nil {
			return fmt.Errorf("name of %s must be a string", id)
		}
		if name = strings.TrimSpace(name); name != "" && (utf8.RuneCountInString(name) > 100 || hasControl(name)) {
			return fmt.Errorf("name of %s must be at most 100 characters without control characters", id)
		}
	}
	return nil
}

// Metric is one reading for POST /nodes/:id/metrics.
type Metric struct {
	Name  string  `json:"name"`
	Label string  `json:"label,omitempty"`
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
	At    string  `json:"at,omitempty"` // RFC 3339; empty = now
}

// CheckMetric applies the panel's rules to one metric. nil means valid.
func CheckMetric(m Metric, now time.Time) error {
	switch {
	case !metricNameRe.MatchString(m.Name):
		return errors.New("name must start with a lowercase letter, at most 40 characters")
	case slices.Contains(MetricReserved, m.Name):
		return fmt.Errorf("name %s is reserved", m.Name)
	case !slices.Contains(MetricUnits, m.Unit):
		return fmt.Errorf("unit must be one of %s", strings.Join(MetricUnits, ", "))
	case math.IsNaN(m.Value) || math.IsInf(m.Value, 0) || math.Abs(m.Value) > 1e12:
		return errors.New("value must be a finite number within ±1e12")
	case m.Unit == "bool" && m.Value != 0 && m.Value != 1:
		return errors.New("a bool metric is 0 or 1")
	case utf8.RuneCountInString(m.Label) > 40 || hasControl(m.Label):
		return errors.New("label must be at most 40 characters without control characters")
	}
	if m.At != "" {
		at, err := time.Parse(time.RFC3339, m.At)
		if err != nil || at.Before(now.Add(-24*time.Hour)) || at.After(now.Add(5*time.Minute)) {
			return errors.New("at must be an RFC 3339 time within the last 24 hours")
		}
	}
	return nil
}

// Nodes lists nodes into out (scope nodes:read).
func (c *Client) Nodes(out any) error { return c.Do("GET", "/nodes", nil, out, nil) }

// ReportMetrics stores readings on a node (scope nodes:metrics:write), at
// most 20 per call.
func (c *Client) ReportMetrics(nodeID string, metrics []Metric, out any) error {
	return c.Do("POST", "/nodes/"+url.PathEscape(nodeID)+"/metrics", map[string]any{"metrics": metrics}, out, nil)
}

// ExternalMetrics reads every source's metrics for a node (scope nodes:read).
func (c *Client) ExternalMetrics(nodeID string, out any) error {
	return c.Do("GET", "/nodes/"+url.PathEscape(nodeID)+"/metrics/external", nil, out, nil)
}
