// Package plugin defines the versioned external-plugin installation protocol.
package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const HostAPI = "1.0.0"
const MaxManifest = 256 << 10

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,126}[a-z0-9]$`)
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var APIScopes = []string{"users:read", "users:write", "users:renew", "users:traffic:add", "subs:read", "nodes:read", "traffic:read", "settings:read", "plugin:config:read"}

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type Page struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
	Path  string `json:"path"`
}
type Manifest struct {
	ManifestVersion int    `json:"manifestVersion"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Author          string `json:"author"`
	Homepage        string `json:"homepage"`
	Requires        struct {
		HostAPI string `json:"hostApi"`
	} `json:"requires"`
	Runtime      string   `json:"runtime"`
	Scopes       []string `json:"scopes"`
	Events       []string `json:"events"`
	Pages        []Page   `json:"pages"`
	Portal       []Page   `json:"portal"`
	Settings     []Field  `json:"settings"`
	Subscription struct {
		Formats   []string `json:"formats"`
		Transform bool     `json:"transform"`
	} `json:"subscription"`
}

// DecodeManifest rejects duplicate keys, unknown fields, oversized/deep JSON,
// incompatible versions and capabilities not implemented by installation MVP.
func DecodeManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if len(raw) > MaxManifest {
		return m, errors.New("manifest exceeds 256 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSON(d, 0); err != nil {
		return m, err
	}
	if _, err := d.Token(); err != io.EOF {
		return m, errors.New("manifest contains trailing data")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, errors.New("manifest contains unknown fields or invalid values")
	}
	return m, m.Validate()
}
func walkJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("manifest nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return errors.New("duplicate JSON key")
			}
			seen[key] = true
		}
		if err := walkJSON(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
func (m Manifest) Validate() error {
	if m.ManifestVersion != 1 || m.Runtime != "external" {
		return errors.New("only manifestVersion 1 external plugins are supported")
	}
	if !idPattern.MatchString(m.ID) || strings.Contains(m.ID, "..") || strings.TrimSpace(m.Name) == "" || len(m.Name) > 100 || len(m.Author) > 128 {
		return errors.New("invalid plugin identity")
	}
	if !versionPattern.MatchString(m.Version) || len(m.Version) > 64 {
		return errors.New("version must be SemVer")
	}
	if i := strings.IndexByte(m.Version, '-'); i >= 0 {
		for _, part := range strings.Split(strings.Split(m.Version[i+1:], "+")[0], ".") {
			if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
				return errors.New("numeric prerelease has leading zero")
			}
		}
	}
	if m.Requires.HostAPI != "^1.0.0" && m.Requires.HostAPI != HostAPI {
		return errors.New("unsupported host API range; use ^1.0.0")
	}
	if m.Homepage != "" {
		u, err := url.Parse(m.Homepage)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(m.Homepage) > 2048 {
			return errors.New("invalid homepage")
		}
	}
	if len(m.Events) > 0 || len(m.Pages) > 0 || len(m.Portal) > 0 || len(m.Subscription.Formats) > 0 || m.Subscription.Transform {
		return errors.New("events, embedded pages, portal and subscription extensions are not supported yet")
	}
	seen := map[string]bool{}
	for _, scope := range m.Scopes {
		if !slices.Contains(APIScopes, scope) || seen[scope] {
			return errors.New("unknown or duplicate API scope")
		}
		seen[scope] = true
	}
	if len(m.Settings) > 32 {
		return errors.New("too many settings")
	}
	seen = map[string]bool{}
	for _, field := range m.Settings {
		if !keyPattern.MatchString(field.Key) || seen[field.Key] || strings.TrimSpace(field.Label) == "" || len(field.Label) > 100 || !slices.Contains([]string{"string", "number", "boolean", "secret"}, field.Type) {
			return errors.New("invalid or duplicate setting")
		}
		seen[field.Key] = true
	}
	return nil
}
func Digest(m Manifest) string {
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Settings validates known field types and separates secrets for encrypted storage.
// Values returned by JSON decoding use float64 for numbers.
func (m Manifest) SplitSettings(values map[string]any, previousSecrets map[string]string, clear []string) (map[string]any, map[string]string, error) {
	if len(values) > 32 || len(clear) > 32 {
		return nil, nil, errors.New("too many settings")
	}
	plain := map[string]any{}
	secrets := map[string]string{}
	known := map[string]Field{}
	for _, f := range m.Settings {
		known[f.Key] = f
		if f.Type == "secret" {
			secrets[f.Key] = previousSecrets[f.Key]
		}
	}
	for _, key := range clear {
		f, ok := known[key]
		if !ok || f.Type != "secret" {
			return nil, nil, errors.New("invalid secret removal")
		}
		delete(secrets, key)
	}
	for key, v := range values {
		f, ok := known[key]
		if !ok {
			return nil, nil, errors.New("unknown setting")
		}
		switch f.Type {
		case "secret", "string":
			text, ok := v.(string)
			if !ok || len(text) > 4096 {
				return nil, nil, fmt.Errorf("invalid setting %s", key)
			}
			if f.Type == "secret" {
				if text != "" {
					secrets[key] = text
				}
			} else {
				plain[key] = text
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return nil, nil, fmt.Errorf("invalid boolean %s", key)
			}
			plain[key] = v
		case "number":
			if n, ok := v.(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, nil, fmt.Errorf("invalid number %s", key)
			}
			plain[key] = v
		}
	}
	for _, f := range m.Settings {
		if !f.Required {
			continue
		}
		value, ok := plain[f.Key]
		if f.Type == "secret" {
			ok = secrets[f.Key] != ""
		} else if f.Type == "string" {
			ok = ok && strings.TrimSpace(fmt.Sprint(value)) != ""
		}
		if !ok {
			return nil, nil, fmt.Errorf("required setting %s", f.Key)
		}
	}
	return plain, secrets, nil
}

type Installation struct {
	ProtocolVersion string `json:"protocolVersion"`
	PanelURL        string `json:"panel_url"`
	APIBase         string `json:"api_base"`
	InstallID       string `json:"install_id"`
	Nonce           string `json:"nonce"`
	Token           string `json:"token"`
}
type InstallAck struct {
	InstallID string `json:"install_id"`
	Nonce     string `json:"nonce"`
}
