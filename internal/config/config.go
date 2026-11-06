// Package config loads and validates the commandcode plugin configuration
// per spec 04 (configuration) and spec 05 §2 (HTTPS/timeouts).
package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults (spec 04 §2/§4).
const (
	DefaultBaseURL          = "https://api.commandcode.ai/provider/v1"
	DefaultModelPrefix      = "commandcode"
	DefaultRefreshInterval  = 15 * time.Minute
	DefaultRequestTimeout   = 5 * time.Minute
	DefaultMaxResponseBytes = int64(67108864) // 64 MiB
)

type ModelPrefix struct {
	Enabled bool
	Value   string
}

type Catalog struct {
	RefreshInterval       time.Duration
	StaleWhileUnavailable bool
}

type Protocols struct {
	ChatCompletions bool
	Messages        bool
	Responses       bool
}

type RouteOverride struct {
	Protocol string `yaml:"protocol"`
	Endpoint string `yaml:"endpoint"`
}

type Config struct {
	BaseURL     string
	CatalogURL  string
	ModelPrefix ModelPrefix
	// LegacyAPIKeys reports that a retired api-keys key was present.
	// Its contents are not kept or used as credentials.
	LegacyAPIKeys    bool
	Catalog          Catalog
	Protocols        Protocols
	RouteOverrides   map[string]RouteOverride
	AllowHTTP        bool
	RequestTimeout   time.Duration
	MaxResponseBytes int64
	// ModelWhitelist filters published models. Empty means "allow all"
	// (backward compatible). Each entry is matched case-insensitively against
	// the upstream and public model ids: plain text is a substring keyword,
	// entries containing * or ? are wildcard patterns, and an entry prefixed
	// with = is an exact match.
	ModelWhitelist []string
	// ModelBlacklist removes published models after the whitelist. It uses the
	// same entry syntax as ModelWhitelist and takes precedence over it.
	ModelBlacklist []string
	// ModelAliases renames published models (OAuth-alias style). Each entry maps
	// a model (upstream or current public id) to a new client-visible id; the
	// original id is not published.
	ModelAliases []ModelAlias
	// ModelAliasStripPrefix publishes every model under its upstream id (no
	// model-prefix) unless a ModelAliases entry overrides it.
	ModelAliasStripPrefix bool
}

// ModelAlias maps one model id to a client-visible alias.
type ModelAlias struct {
	Name  string `yaml:"name" json:"name"`
	Alias string `yaml:"alias" json:"alias"`
}

// rawConfig mirrors the YAML shape; pointer fields distinguish "unset"
// (apply default) from explicitly-set values including "" (validate as-is).
// Unknown fields are ignored (host may pass extra keys).
type rawConfig struct {
	BaseURL     *string   `yaml:"base-url"`
	CatalogURL  *string   `yaml:"catalog-url"`
	ModelPrefix rawPrefix `yaml:"model-prefix"`
	// LegacyAPIKeys carries the retired api-keys node only long enough
	// to see whether the key exists. Its contents are not interpreted.
	LegacyAPIKeys         yaml.Node                `yaml:"api-keys"`
	Catalog               rawCatalog               `yaml:"catalog"`
	Protocols             rawProtocols             `yaml:"protocols"`
	RouteOverrides        map[string]RouteOverride `yaml:"route-overrides"`
	AllowHTTP             bool                     `yaml:"allow-http"`
	RequestTimeout        *string                  `yaml:"request-timeout"`
	MaxResponseBytes      *int64                   `yaml:"max-response-bytes"`
	ModelWhitelist        []string                 `yaml:"model-whitelist"`
	ModelBlacklist        []string                 `yaml:"model-blacklist"`
	ModelAliases          []ModelAlias             `yaml:"model-aliases"`
	ModelAliasStripPrefix bool                     `yaml:"model-alias-strip-prefix"`
}

type rawPrefix struct {
	Enabled *bool   `yaml:"enabled"`
	Value   *string `yaml:"value"`
}

type rawCatalog struct {
	RefreshInterval       *string `yaml:"refresh-interval"`
	StaleWhileUnavailable *bool   `yaml:"stale-while-unavailable"`
}

type rawProtocols struct {
	ChatCompletions *bool `yaml:"chat-completions"`
	Messages        *bool `yaml:"messages"`
	Responses       *bool `yaml:"responses"`
}

var (
	validProtocols = map[string]bool{"chat-completions": true, "messages": true, "responses": true}
)

// Load decodes YAML, applies defaults, and validates (spec 04 §6).
// A legacy api-keys entry is recorded only as present or absent: its
// contents are not parsed into credentials, expanded, stored, or
// validated. Decode errors never echo decoded node values.
func Load(yamlBytes []byte) (Config, error) {
	var raw rawConfig
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		if n := regexp.MustCompile(`line (\d+)`).FindStringSubmatch(err.Error()); n != nil {
			return Config{}, fmt.Errorf("decode config: invalid YAML structure near line %s", n[1])
		}
		return Config{}, fmt.Errorf("decode config: invalid YAML structure")
	}
	legacyAPIKeys := raw.LegacyAPIKeys.Kind != 0
	raw.LegacyAPIKeys = yaml.Node{}
	refreshInterval, err := parseDuration("catalog.refresh-interval", raw.Catalog.RefreshInterval, DefaultRefreshInterval)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := parseDuration("request-timeout", raw.RequestTimeout, DefaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}
	if requestTimeout <= 0 {
		return Config{}, fmt.Errorf("request-timeout: must be positive")
	}
	c := Config{
		BaseURL: orDefault(raw.BaseURL, DefaultBaseURL),
		ModelPrefix: ModelPrefix{
			Enabled: orDefault(raw.ModelPrefix.Enabled, true),
			Value:   orDefault(raw.ModelPrefix.Value, DefaultModelPrefix),
		},
		LegacyAPIKeys: legacyAPIKeys,
		Catalog: Catalog{
			RefreshInterval:       refreshInterval,
			StaleWhileUnavailable: orDefault(raw.Catalog.StaleWhileUnavailable, true),
		},
		Protocols: Protocols{
			ChatCompletions: orDefault(raw.Protocols.ChatCompletions, true),
			Messages:        orDefault(raw.Protocols.Messages, true),
			Responses:       orDefault(raw.Protocols.Responses, true),
		},
		RouteOverrides:        raw.RouteOverrides,
		AllowHTTP:             raw.AllowHTTP,
		RequestTimeout:        requestTimeout,
		MaxResponseBytes:      orDefault(raw.MaxResponseBytes, DefaultMaxResponseBytes),
		ModelWhitelist:        normalizeWhitelist(raw.ModelWhitelist),
		ModelBlacklist:        normalizeWhitelist(raw.ModelBlacklist),
		ModelAliases:          normalizeModelAliases(raw.ModelAliases),
		ModelAliasStripPrefix: raw.ModelAliasStripPrefix,
	}
	if raw.CatalogURL != nil {
		// Mirror the derived-default trim so an explicit trailing-slash
		// catalog-url cannot double up separators downstream.
		c.CatalogURL = strings.TrimRight(*raw.CatalogURL, "/")
	} else {
		c.CatalogURL = strings.TrimRight(c.BaseURL, "/") + "/models"
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// PublicID returns the client-facing model ID (FR-003).
func PublicID(c Config, upstreamID string) string {
	if c.ModelPrefix.Enabled {
		return c.ModelPrefix.Value + "/" + upstreamID
	}
	return upstreamID
}

const (
	maxWhitelistEntries = 500
	maxWhitelistLength  = 256
)

// normalizeWhitelist trims entries, drops blanks and duplicates, and bounds the
// list so a malformed config cannot blow up the catalog build.
func normalizeWhitelist(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if len(entry) > maxWhitelistLength {
			entry = entry[:maxWhitelistLength]
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
		if len(out) >= maxWhitelistEntries {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

const maxModelAliases = 500

// normalizeModelAliases trims entries, drops incomplete or duplicate names, and
// bounds the list.
func normalizeModelAliases(in []ModelAlias) []ModelAlias {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelAlias, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, entry := range in {
		name := strings.TrimSpace(entry.Name)
		alias := strings.TrimSpace(entry.Alias)
		if name == "" || alias == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ModelAlias{Name: name, Alias: alias})
		if len(out) >= maxModelAliases {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ModelAliasFor resolves the published id for a model. A custom alias wins,
// then the strip-prefix option (publish the upstream id), otherwise the
// configured public id.
func (c Config) ModelAliasFor(upstreamID, publicID string) string {
	for _, entry := range c.ModelAliases {
		if strings.EqualFold(entry.Name, upstreamID) || strings.EqualFold(entry.Name, publicID) {
			return entry.Alias
		}
	}
	if c.ModelAliasStripPrefix {
		return upstreamID
	}
	return publicID
}

// ModelAllowed reports whether a model passes the configured whitelist.
// An empty whitelist allows every model (backward compatible). Entries are
// matched case-insensitively against both the upstream id and the public id;
// plain entries are substring keywords, and entries containing * or ? are
// wildcard patterns where * matches any run of characters (including "/").
func (c Config) ModelAllowed(upstreamID, publicID string) bool {
	if len(c.ModelWhitelist) == 0 {
		return true
	}
	return matchesAnyEntry(c.ModelWhitelist, upstreamID, publicID)
}

// ModelBlocked reports whether a model matches the configured blacklist.
func (c Config) ModelBlocked(upstreamID, publicID string) bool {
	return len(c.ModelBlacklist) > 0 && matchesAnyEntry(c.ModelBlacklist, upstreamID, publicID)
}

// ModelSelected applies the blacklist (deny) before the whitelist (allow).
// It returns whether the model is published and, when excluded, the reason
// recorded in catalog diagnostics.
func (c Config) ModelSelected(upstreamID, publicID string) (bool, string) {
	if c.ModelBlocked(upstreamID, publicID) {
		return false, "excluded by model-blacklist"
	}
	if !c.ModelAllowed(upstreamID, publicID) {
		return false, "excluded by model-whitelist"
	}
	return true, ""
}

// matchesAnyEntry matches an id against entries: "=x" is an exact match,
// entries with * or ? are wildcards, and everything else is a
// case-insensitive substring keyword. Both the upstream and public ids are
// tested.
func matchesAnyEntry(entries []string, upstreamID, publicID string) bool {
	upstream := strings.ToLower(upstreamID)
	public := strings.ToLower(publicID)
	for _, raw := range entries {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "=") {
			exact := strings.TrimSpace(strings.TrimPrefix(pattern, "="))
			if exact != "" && (upstream == exact || public == exact) {
				return true
			}
			continue
		}
		if strings.ContainsAny(pattern, "*?") {
			if wildcardMatch(pattern, upstream) || wildcardMatch(pattern, public) {
				return true
			}
			continue
		}
		if strings.Contains(upstream, pattern) || strings.Contains(public, pattern) {
			return true
		}
	}
	return false
}

// wildcardMatch matches s against pattern supporting * (any run) and ?
// (exactly one byte). It is iterative and allocation-free.
func wildcardMatch(pattern, s string) bool {
	pi, si := 0, 0
	star, resume := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			star = pi
			resume = si
			pi++
		case star >= 0:
			pi = star + 1
			resume++
			si = resume
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func (c Config) validate() error {
	if err := validateURL("base-url", c.BaseURL, c.AllowHTTP); err != nil {
		return err
	}
	if err := validateURL("catalog-url", c.CatalogURL, c.AllowHTTP); err != nil {
		return err
	}
	if c.Catalog.RefreshInterval < time.Minute {
		return fmt.Errorf("catalog.refresh-interval: must be at least 1m")
	}
	if c.MaxResponseBytes <= 0 {
		return fmt.Errorf("max-response-bytes: must be positive")
	}
	for name, o := range c.RouteOverrides {
		if !validProtocols[o.Protocol] {
			return fmt.Errorf("route-overrides[%s].protocol: unsupported protocol %q", name, o.Protocol)
		}
		if o.Endpoint == "" {
			return fmt.Errorf("route-overrides[%s].endpoint: must not be empty", name)
		}
		if !strings.HasPrefix(o.Endpoint, "/") {
			return fmt.Errorf("route-overrides[%s].endpoint: must start with /", name)
		}
	}
	if c.ModelPrefix.Enabled && !validPrefix(c.ModelPrefix.Value) {
		return fmt.Errorf("model-prefix.value: invalid provider-ID characters %q", c.ModelPrefix.Value)
	}
	return nil
}

func validateURL(name, raw string, allowHTTP bool) error {
	if raw == "" {
		return fmt.Errorf("%s: must not be empty", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: invalid URL", name)
	}
	if u.Scheme == "" || u.Host == "" {
		// Echo only scheme://host — the configured string may embed
		// userinfo credentials (https://user:key@host).
		return fmt.Errorf("%s: invalid URL %s://%s", name, u.Scheme, u.Host)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%s: must not contain query, fragment, or userinfo", name)
	}
	// Scheme allowlist (spec 05 §2): https always; http only behind
	// allow-http; anything else (ftp://, file://, custom schemes) is
	// rejected at load instead of failing at transport time.
	switch u.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return fmt.Errorf("%s: http scheme requires allow-http", name)
		}
	default:
		return fmt.Errorf("%s: unsupported scheme %q; use https", name, u.Scheme)
	}
	return nil
}

func validPrefix(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if i == 0 && !alnum {
			return false
		}
		if !alnum && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func parseDuration(name string, p *string, def time.Duration) (time.Duration, error) {
	if p == nil {
		return def, nil
	}
	d, err := time.ParseDuration(*p)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration", name)
	}
	return d, nil
}

func orDefault[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}
