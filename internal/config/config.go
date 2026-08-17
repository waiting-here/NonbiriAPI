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
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"nonbiriapi/internal/host"
	"nonbiriapi/internal/secret"
)

// Defaults are centralized here.
const (
	defaultListenAddr     = "127.0.0.1:8080"
	defaultDBPath         = "nonbiriapi.db"
	defaultLogLevel       = "info"
	defaultProxies        = "127.0.0.0/8,::1/128"
	maxMasterKeyFileBytes = 128
)

// Config holds all resolved startup configuration. The encryption root is
// kept in an opaque Vault and can be transferred exactly once with
// TakeSecretVault; it is never exposed as a printable byte slice.
type Config struct {
	ListenAddr    string
	DBPath        string
	LogLevel      string
	AdminUsername string
	AdminPassword string
	// MasterSource is non-sensitive provenance for startup diagnostics. The
	// corresponding key bytes remain inside masterVault.
	MasterSource        string
	masterVault         *secret.Vault
	DiscordClientID     string
	DiscordClientSecret string
	// SiteBaseURL is the canonical public origin of the user station (no
	// trailing slash, no path/query/fragment/userinfo).
	SiteBaseURL string
	// UserHost is the hostname portion of SiteBaseURL.
	UserHost string
	// AdminHost is the admin station hostname (env override or derived as
	// "admin." + UserHost). It must differ from UserHost.
	AdminHost         string
	TrustedProxyCIDRs []netip.Prefix
	SMTP              SMTPConfig
}

// SMTPConfig is reserved (alpha has no email alerts); it is parsed and
// validated only when Host is set so a future rail can wire it without
// touching the loader surface.
type SMTPConfig struct {
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	TLS     string // "" auto, "starttls", "implicit"
	Enabled bool
}

// Load reads environment variables and returns a fully validated Config, or a
// descriptive error listing every problem found in the startup set.
func Load() (*Config, error) {
	c := &Config{
		ListenAddr:          getenv("NONBIRI_LISTEN_ADDR", defaultListenAddr),
		DBPath:              getenv("NONBIRI_DB_PATH", defaultDBPath),
		LogLevel:            getenv("NONBIRI_LOG_LEVEL", defaultLogLevel),
		AdminUsername:       os.Getenv("NONBIRI_ADMIN_USERNAME"),
		AdminPassword:       os.Getenv("NONBIRI_ADMIN_PASSWORD"),
		DiscordClientID:     os.Getenv("NONBIRI_DISCORD_CLIENT_ID"),
		DiscordClientSecret: os.Getenv("NONBIRI_DISCORD_CLIENT_SECRET"),
	}

	var errs []string

	// Master key: exactly one non-empty source is required. Parsing produces a
	// short-lived byte slice that setMasterKey clears after constructing the
	// opaque process vault.
	keyRaw, keyOK := os.LookupEnv("NONBIRI_MASTER_KEY")
	keyFile, fileOK := os.LookupEnv("NONBIRI_MASTER_KEY_FILE")
	keySet := keyOK && strings.TrimSpace(keyRaw) != ""
	fileSet := fileOK && strings.TrimSpace(keyFile) != ""
	if keySet && fileSet {
		errs = append(errs, "set either NONBIRI_MASTER_KEY or NONBIRI_MASTER_KEY_FILE, not both")
	} else {
		switch {
		case keySet:
			mk, err := decodeMasterKey(keyRaw)
			if err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY: "+err.Error())
			} else if err := c.setMasterKey(mk, "env"); err != nil {
				errs = append(errs, "NONBIRI_MASTER_KEY: "+err.Error())
			}
		case fileSet:
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
		c.discardSecretVault()
		return nil, fmt.Errorf("invalid startup configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return c, nil
}

// TakeSecretVault transfers ownership of the configured process vault. It
// returns nil after the first call. The caller must Close the returned Vault
// during shutdown; Config retains no recoverable master-key copy.
func (c *Config) TakeSecretVault() *secret.Vault {
	if c == nil {
		return nil
	}
	v := c.masterVault
	c.masterVault = nil
	return v
}

func (c *Config) setMasterKey(mk []byte, src string) error {
	defer clear(mk)
	if len(mk) != secret.MasterKeyBytes {
		return fmt.Errorf("must decode to exactly %d bytes, got %d", secret.MasterKeyBytes, len(mk))
	}
	v, err := secret.New(mk)
	if err != nil {
		return fmt.Errorf("initialize encryption root: %w", err)
	}
	c.discardSecretVault()
	c.masterVault = v
	c.MasterSource = src
	return nil
}

func (c *Config) discardSecretVault() {
	if c.masterVault != nil {
		_ = c.masterVault.Close()
		c.masterVault = nil
	}
}

func (c *Config) setSiteBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
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
	parsed, err := host.ParseConfigured(u.Host)
	if err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}
	c.SiteBaseURL = u.Scheme + "://" + parsed.Authority
	c.UserHost = parsed.Host
	return nil
}

func (c *Config) setAdminHost(override string) error {
	h := strings.TrimSpace(override)
	if h == "" {
		if c.UserHost == "" {
			return fmt.Errorf("cannot derive admin host without a valid site base URL")
		}
		if strings.Contains(c.UserHost, ":") {
			return fmt.Errorf("an IPv6 site requires an explicit admin host")
		}
		h = "admin." + c.UserHost
	}
	parsed, err := host.ParseConfigured(h)
	if err != nil {
		return fmt.Errorf("must be a hostname (optionally with :port): %w", err)
	}
	if parsed.Host == c.UserHost {
		return fmt.Errorf("must differ from the user station host %q", c.UserHost)
	}
	c.AdminHost = parsed.Authority
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

// decodeMasterKey decodes a canonical environment value as hex, padded or
// unpadded standard base64, or padded or unpadded URL-safe base64. It accepts
// exactly 32 decoded bytes and never includes input material in its error.
func decodeMasterKey(raw string) ([]byte, error) {
	encoded := []byte(strings.TrimSpace(raw))
	defer clear(encoded)
	return decodeMasterKeyBytes(encoded)
}

func decodeMasterKeyBytes(encoded []byte) ([]byte, error) {
	if len(encoded) == hex.EncodedLen(secret.MasterKeyBytes) {
		decoded := make([]byte, secret.MasterKeyBytes)
		if n, err := hex.Decode(decoded, encoded); err == nil && n == secret.MasterKeyBytes {
			return decoded, nil
		}
		clear(decoded)
	}
	if !bytes.ContainsAny(encoded, " \t\r\n") {
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		} {
			decoded := make([]byte, enc.DecodedLen(len(encoded)))
			n, err := enc.Decode(decoded, encoded)
			decoded = decoded[:n]
			canonical := enc.AppendEncode(nil, decoded)
			valid := err == nil && len(decoded) == secret.MasterKeyBytes && bytes.Equal(canonical, encoded)
			clear(canonical)
			if valid {
				return decoded, nil
			}
			clear(decoded)
		}
	}
	return nil, fmt.Errorf("must be hex (%d chars) or canonical base64 of exactly %d bytes", hex.EncodedLen(secret.MasterKeyBytes), secret.MasterKeyBytes)
}

// loadMasterKeyFile reads an existing owner-readable, owner-only regular file,
// bounds the read, and decodes canonical text or exactly 32 raw bytes. On Unix,
// the open itself uses O_NOFOLLOW; the Lstat identity is then matched against
// the opened descriptor. It never creates, rewrites, or generates a key.
func loadMasterKeyFile(path string) ([]byte, string, error) {
	path = filepath.Clean(path)
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("master key file does not exist")
		}
		return nil, "", fmt.Errorf("inspect master key file: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, "", fmt.Errorf("master key file must be a regular file")
	}

	file, opened, err := openValidatedMasterKeyFile(path, before)
	if err != nil {
		return nil, "", err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	data, err := io.ReadAll(io.LimitReader(file, maxMasterKeyFileBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read master key file: %w", err)
	}
	defer clear(data)
	if len(data) > maxMasterKeyFileBytes {
		return nil, "", fmt.Errorf("master key file content is too long")
	}

	after, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("reinspect master key file: %w", err)
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() ||
		!opened.ModTime().Equal(after.ModTime()) || opened.Mode() != after.Mode() ||
		!secureMasterKeyFileMode(after.Mode(), runtime.GOOS) {
		return nil, "", fmt.Errorf("master key file changed while being read")
	}
	if err := file.Close(); err != nil {
		return nil, "", fmt.Errorf("close master key file: %w", err)
	}
	closed = true

	src := "file"
	trimmed := bytes.TrimSpace(data)
	if key, err := decodeMasterKeyBytes(trimmed); err == nil {
		return key, src, nil
	}
	if len(data) == secret.MasterKeyBytes {
		key := make([]byte, secret.MasterKeyBytes)
		copy(key, data)
		return key, src, nil
	}
	return nil, "", fmt.Errorf("master key file must contain exactly %d raw bytes or a canonical encoding", secret.MasterKeyBytes)
}

// openValidatedMasterKeyFile binds the pathname identity observed by Lstat to
// the descriptor used for all subsequent checks and reads. The platform opener
// is O_NOFOLLOW on Unix, so replacing the path with a symlink to the very same
// inode cannot bypass os.SameFile.
func openValidatedMasterKeyFile(path string, before os.FileInfo) (*os.File, os.FileInfo, error) {
	file, err := openMasterKeyFilePlatform(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open master key file: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("inspect opened master key file: %w", err)
	}
	if !before.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("master key file changed during validation")
	}
	if !secureMasterKeyFileMode(opened.Mode(), runtime.GOOS) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("master key file permissions must be owner-readable and owner-only")
	}
	return file, opened, nil
}

func secureMasterKeyFileMode(mode os.FileMode, goos string) bool {
	// Windows FileMode permission bits are synthesized from ACLs and cannot
	// express the deployment guarantee. Unix key files must grant owner-read,
	// may grant owner-write, and must grant no execute or group/other access.
	if goos == "windows" {
		return true
	}
	if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	perm := mode.Perm()
	return perm == 0o400 || perm == 0o600
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
