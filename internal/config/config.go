// Package config loads startup-determined configuration from environment
// variables. Per the contract, environment variables own the values that must
// be fixed before startup or that are security roots (administrator account,
// Discord OAuth credentials, master key, listen address, database path). SMTP
// is parsed when present but is reserved (no email alerts in alpha).
//
// Load fails fast on missing required fields and on format violations, so a
// misconfigured security root never lets the process come up silently. The
// rendered error lists every offending field at once where practical.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Defaults are centralized here.
const (
	defaultListenAddr = "127.0.0.1:8080"
	defaultDBPath     = "nonbiriapi.db"
	defaultLogLevel   = "info"
	defaultProxies    = "127.0.0.0/8,::1/128"
)

// Config holds all resolved startup configuration. The MasterKey is validated
// here (exactly 32 bytes) but secret encryption is wired by a later rail; this
// skeleton does not perform any crypto with it.
type Config struct {
	ListenAddr   string
	DBPath       string
	LogLevel     string
	AdminUsername string
	AdminPassword string
	// MasterKey is the 32-byte encryption root (env- or file-provided). It is
	// never logged; only its source/length is reported at startup.
	MasterKey     []byte
	MasterSource  string
	DiscordClientID     string
	DiscordClientSecret string
	// SiteBaseURL is the canonical public origin of the user station (no
	// trailing slash, no path/query/fragment/userinfo).
	SiteBaseURL string
	// UserHost is the hostname portion of SiteBaseURL.
	UserHost string
	// AdminHost is the admin station hostname (env override or derived as
	// "admin." + UserHost). It must differ from UserHost.
	AdminHost string
	TrustedProxyCIDRs []netip.Prefix
	SMTP              SMTPConfig
}

// SMTPConfig is reserved (alpha has no email alerts); it is parsed and
// validated only when Host is set so a future rail can wire it without
// touching the loader surface.
type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	TLS      string // "" auto, "starttls", "implicit"
	Enabled  bool
}

// Load reads environment variables and returns a fully validated Config, or a
// descriptive error listing every problem found in the startup set.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:   getenv("NONBIRI_LISTEN_ADDR", defaultListenAddr),
		DBPath:       getenv("NONBIRI_DB_PATH", defaultDBPath),
		LogLevel:     getenv("NONBIRI_LOG_LEVEL", defaultLogLevel),
		AdminUsername: os.Getenv("NONBIRI_ADMIN_USERNAME"),
		AdminPassword: os.Getenv("NONBIRI_ADMIN_PASSWORD"),
		DiscordClientID:     os.Getenv("NONBIRI_DISCORD_CLIENT_ID"),
		DiscordClientSecret: os.Getenv("NONBIRI_DISCORD_CLIENT_SECRET"),
	}

	var errs []string

	// Master key: env value first, then a key file. Exactly 32 bytes required.
	keyRaw, keyOK := os.LookupEnv("NONBIRI_MASTER_KEY")
	keyFile, fileOK := os.LookupEnv("NONBIRI_MASTER_KEY_FILE")
	if keyRaw != "" && keyFile != "" {
		errs = append(errs, "set either NONBIRI_MASTER_KEY or NONBIRI_MASTER_KEY_FILE, not both")
	} else {
		switch {
		case keyOK && strings.TrimSpace(keyRaw) != "":
			mk, err := decodeMasterKey(keyRaw)
			if err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY: "+err.Error())
			} else if err := c.setMasterKey(mk, "env"); err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY: "+err.Error())
			}
		case fileOK && strings.TrimSpace(keyFile) != "":
			mk, src, err := loadMasterKeyFile(keyFile)
			if err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY_FILE: "+err.Error())
			} else if err := c.setMasterKey(mk, src); err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY_FILE: "+err.Error())
			}
		default:
			errs = append(errs, "NONBIRI_MASTER_KEY (or NONBIRI_MASTER_KEY_FILE): required, must decode to exactly 32 bytes")
		}
	}

	if strings.TrimSpace(c.AdminUsername) == "" {
		errs = append(errs, "NONBIRI_ADMIN_USERNAME: required")
	}
	if strings.TrimSpace(c.AdminPassword) == "" {
		errs = append(errs, "NONBIRI_ADMIN_PASSWORD: required")
	}
	if strings.TrimSpace(c.DiscordClientID) == "" {
		errs = append(errs, "NONBIRI_DISCORD_CLIENT_ID: required")
	}
	if strings.TrimSpace(c.DiscordClientSecret) == "" {
		errs = append(errs, "NONBIRI_DISCORD_CLIENT_SECRET: required")
	}

	siteRaw := strings.TrimRight(strings.TrimSpace(os.Getenv("NONBIRI_SITE_BASE_URL")), "/")
	if siteRaw == "" {
		errs = append(errs, "NONBIRI_SITE_BASE_URL: required")
	} else if err := c.setSiteBaseURL(siteRaw); err != nil {
		errs = append(errs, "NONBIRI_SITE_BASE_URL: "+err.Error())
	} else if err := c.setAdminHost(os.Getenv("NONBIRI_ADMIN_HOST")); err != nil {
		errs = append(errs, "NONBIRI_ADMIN_HOST: "+err.Error())
	}

	if strings.TrimSpace(c.ListenAddr) == "" {
		errs = append(errs, "NONBIRI_LISTEN_ADDR: must not be empty")
	}

	proxies, err := parseProxies(os.Getenv("NONBIRI_TRUSTED_PROXY_CIDRS"), defaultProxies)
	if err != nil {
		errs = append(errs, "NONBIRI_TRUSTED_PROXY_CIDRS: "+err.Error())
	} else {
		c.TrustedProxyCIDRs = proxies
	}

	if err := c.setSMTP(); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid startup configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return c, nil
}

func (c *Config) setMasterKey(mk []byte, src string) error {
	if len(mk) != 32 {
		return fmt.Errorf("must decode to exactly 32 bytes, got %d", len(mk))
	}
	c.MasterKey = mk
	c.MasterSource = src
	return nil
}

func (c *Config) setSiteBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("missing hostname")
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		return fmt.Errorf("path is not allowed (got %q); use an origin only", u.Path)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("query is not allowed")
	}
	if u.Fragment != "" {
		return fmt.Errorf("fragment is not allowed")
	}
	c.SiteBaseURL = u.Scheme + "://" + u.Host
	c.UserHost = strings.ToLower(u.Hostname())
	return nil
}

func (c *Config) setAdminHost(override string) error {
	h := strings.TrimSpace(override)
	if h == "" {
		if c.UserHost == "" {
			return fmt.Errorf("cannot derive admin host without a valid site base URL")
		}
		h = "admin." + c.UserHost
	}
	// Accept "hostname[:port]" but reject a full URL with scheme/path.
	if strings.Contains(h, "://") || strings.ContainsAny(h, "/?#") {
		return fmt.Errorf("must be a hostname (optionally with :port), not a URL")
	}
	hostOnly := h
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i+1:], ":") {
		hostOnly = h[:i] // strip a single port (not an IPv6 bracket form)
	}
	hostOnly = strings.ToLower(hostOnly)
	if hostOnly == "" {
		return fmt.Errorf("hostname is empty")
	}
	if hostOnly == c.UserHost {
		return fmt.Errorf("must differ from the user station host %q", c.UserHost)
	}
	c.AdminHost = h
	return nil
}

// setSMTP parses the reserved SMTP fields and validates them only when a host
// is configured.
func (c *Config) setSMTP() error {
	host := strings.TrimSpace(os.Getenv("NONBIRI_SMTP_HOST"))
	if host == "" {
		return nil // reserved; nothing to validate when disabled
	}
	portStr := strings.TrimSpace(os.Getenv("NONBIRI_SMTP_PORT"))
	port := 587
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("NONBIRI_SMTP_PORT: must be an integer in [1,65535], got %q", portStr)
		}
		port = n
	}
	tls := strings.ToLower(strings.TrimSpace(os.Getenv("NONBIRI_SMTP_TLS")))
	if tls != "" && tls != "starttls" && tls != "implicit" {
		return fmt.Errorf("NONBIRI_SMTP_TLS: must be 'starttls' or 'implicit' (or unset), got %q", tls)
	}
	from := strings.TrimSpace(os.Getenv("NONBIRI_SMTP_FROM"))
	if from == "" {
		from = strings.TrimSpace(os.Getenv("NONBIRI_SMTP_USER"))
	}
	c.SMTP = SMTPConfig{
		Host: host, Port: port,
		User: os.Getenv("NONBIRI_SMTP_USER"),
		Pass: os.Getenv("NONBIRI_SMTP_PASS"),
		From: from, TLS: tls,
		Enabled: true,
	}
	return nil
}

// parseProxies parses a comma-separated CIDR list, accepting bare IPs as
// host routes. "none" yields an empty list (no proxy trusted).
func parseProxies(raw, def string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	if strings.EqualFold(strings.TrimSpace(raw), "none") {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(p)
		if err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		addr, aerr := netip.ParseAddr(p)
		if aerr != nil {
			return nil, fmt.Errorf("invalid IP/CIDR %q", p)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()).Masked())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one CIDR is required, or use 'none'")
	}
	return out, nil
}

// decodeMasterKey decodes an env master key as hex (64 chars) or base64
// (std/rawurl/url) into exactly 32 bytes.
func decodeMasterKey(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("must be hex (64 chars) or base64 of exactly 32 bytes")
}

// loadMasterKeyFile reads a key file and decodes its content (base64 or hex),
// falling back to the raw 32 bytes for a binary key file. Returns the bytes
// and a source description for startup logging.
func loadMasterKeyFile(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %v", filepath.Base(path), err)
	}
	src := "file:" + filepath.Base(path)
	trimmed := strings.TrimSpace(string(data))
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		return b, src, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil && len(b) == 32 {
		return b, src, nil
	}
	if b, err := hex.DecodeString(trimmed); err == nil && len(b) == 32 {
		return b, src, nil
	}
	if len(data) == 32 { // raw binary key file
		return data, src, nil
	}
	return nil, "", fmt.Errorf("content must decode to exactly 32 bytes, got %d bytes", len(data))
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}