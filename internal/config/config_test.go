package config

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allEnvs sets the full required + valid startup set. Tests unset what they
// need to exercise then call this to rebuild a known-good baseline.
func allEnvs(t *testing.T) {
	t.Helper()
	set := map[string]string{
		"NONBIRI_LISTEN_ADDR":           "127.0.0.1:9090",
		"NONBIRI_DB_PATH":               "test.db",
		"NONBIRI_ADMIN_USERNAME":        "admin",
		"NONBIRI_ADMIN_PASSWORD":        "pw",
		"NONBIRI_DISCORD_CLIENT_ID":     "dcid",
		"NONBIRI_DISCORD_CLIENT_SECRET": "dcsecret",
		"NONBIRI_SITE_BASE_URL":         "https://example.com",
		"NONBIRI_MASTER_KEY":            hex.EncodeToString(make([]byte, 32)),
	}
	for k, v := range set {
		t.Setenv(k, v)
	}
	// Ensure no master key file leaks between tests.
	t.Setenv("NONBIRI_MASTER_KEY_FILE", "")
}

func closeLoadedVault(t *testing.T, c *Config) {
	t.Helper()
	vault := c.TakeSecretVault()
	if vault == nil {
		t.Fatal("configuration did not retain a secret vault")
	}
	t.Cleanup(func() { _ = vault.Close() })
}

func TestLoadValid(t *testing.T) {
	allEnvs(t)
	t.Setenv("NONBIRI_DISCORD_OAUTH_SCOPES", "identify guilds.members.read")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vault := c.TakeSecretVault()
	if vault == nil {
		t.Fatal("Load did not retain the configured secret vault")
	}
	t.Cleanup(func() { _ = vault.Close() })
	if c.AdminHost != "admin.example.com" {
		t.Fatalf("AdminHost = %q, want admin.example.com", c.AdminHost)
	}
	if c.UserHost != "example.com" {
		t.Fatalf("UserHost = %q", c.UserHost)
	}
	if c.SiteBaseURL != "https://example.com" {
		t.Fatalf("SiteBaseURL = %q", c.SiteBaseURL)
	}
	if c.TakeSecretVault() != nil {
		t.Fatal("TakeSecretVault transferred the vault more than once")
	}
	if c.MasterSource != "env" {
		t.Fatalf("MasterSource = %q, want env", c.MasterSource)
	}
	if len(c.TrustedProxyCIDRs) != 2 {
		t.Fatalf("trusted proxies = %d, want 2", len(c.TrustedProxyCIDRs))
	}
	if c.DiscordOAuthScopes != "identify guilds.members.read" {
		t.Fatalf("DiscordOAuthScopes = %q", c.DiscordOAuthScopes)
	}
}

func TestAuthStartupValuesRejectControlCharactersAndBounds(t *testing.T) {
	cases := []string{"NONBIRI_ADMIN_USERNAME", "NONBIRI_ADMIN_PASSWORD", "NONBIRI_DISCORD_CLIENT_ID", "NONBIRI_DISCORD_CLIENT_SECRET", "NONBIRI_DISCORD_OAUTH_SCOPES"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			if err := validateStartupAuthValue(key, "valid\nvalue", 4096, key == "NONBIRI_DISCORD_OAUTH_SCOPES"); err == nil {
				t.Fatalf("validateStartupAuthValue accepted control character in %s", key)
			}
		})
	}
	if err := validateStartupAuthValue("NONBIRI_ADMIN_PASSWORD", strings.Repeat("x", 4097), 4096, false); err == nil {
		t.Fatal("validateStartupAuthValue accepted an overlong administrator password")
	}
}

func TestMissingRequiredFailsFast(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"admin_username":        func(t *testing.T) { t.Setenv("NONBIRI_ADMIN_USERNAME", "") },
		"admin_password":        func(t *testing.T) { t.Setenv("NONBIRI_ADMIN_PASSWORD", "") },
		"discord_client_id":     func(t *testing.T) { t.Setenv("NONBIRI_DISCORD_CLIENT_ID", "") },
		"discord_client_secret": func(t *testing.T) { t.Setenv("NONBIRI_DISCORD_CLIENT_SECRET", "") },
		"site_base_url":         func(t *testing.T) { t.Setenv("NONBIRI_SITE_BASE_URL", "") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			allEnvs(t)
			mutate(t)
			if _, err := Load(); err == nil {
				t.Fatalf("Load succeeded with missing %s; want fail-fast", name)
			}
		})
	}
}

func TestMasterKeyValidation(t *testing.T) {
	t.Run("env wrong length", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", hex.EncodeToString(make([]byte, 31)))
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted a 31-byte master key")
		}
	})
	t.Run("both env and file set", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY_FILE", "some.key")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted both env and file master key")
		}
	})
	t.Run("from file base64", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_MASTER_KEY", "")
		keyFile := filepath.Join(t.TempDir(), "master.key")
		contents := base64.StdEncoding.EncodeToString(make([]byte, 32)) + "\n"
		if err := os.WriteFile(keyFile, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("NONBIRI_MASTER_KEY_FILE", keyFile)
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		vault := c.TakeSecretVault()
		if vault == nil {
			t.Fatal("Load did not retain the file-provided secret vault")
		}
		t.Cleanup(func() { _ = vault.Close() })
		if c.MasterSource != "file" {
			t.Fatalf("MasterSource = %q, want file", c.MasterSource)
		}
	})
}

func TestSiteBaseURLFormat(t *testing.T) {
	t.Run("bad scheme", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_SITE_BASE_URL", "ftp://example.com")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted ftp origin")
		}
	})
	t.Run("with path", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_SITE_BASE_URL", "https://example.com/app")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted an origin with a path")
		}
	})
	t.Run("with userinfo", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_SITE_BASE_URL", "https://user:pw@example.com")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted userinfo in origin")
		}
	})
}

func TestAdminHostMustDifferFromUserHost(t *testing.T) {
	allEnvs(t)
	t.Setenv("NONBIRI_ADMIN_HOST", "example.com") // same as derived user host
	if _, err := Load(); err == nil {
		t.Fatalf("Load accepted an admin host identical to the user station host")
	}
}

func TestTrustedProxyCIDRValidation(t *testing.T) {
	t.Run("invalid cidr", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_TRUSTED_PROXY_CIDRS", "not-a-cidr")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted an invalid proxy CIDR")
		}
	})
	t.Run("none disables", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_TRUSTED_PROXY_CIDRS", "none")
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		closeLoadedVault(t, c)
		if len(c.TrustedProxyCIDRs) != 0 {
			t.Fatalf("expected no trusted proxies, got %d", len(c.TrustedProxyCIDRs))
		}
	})
	t.Run("bare ip accepted", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_TRUSTED_PROXY_CIDRS", "10.0.0.1")
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		closeLoadedVault(t, c)
		if len(c.TrustedProxyCIDRs) != 1 {
			t.Fatalf("expected one proxy, got %d", len(c.TrustedProxyCIDRs))
		}
	})
}

func TestSMTPValidation(t *testing.T) {
	t.Run("reserved when unset", func(t *testing.T) {
		allEnvs(t)
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		closeLoadedVault(t, c)
		if c.SMTP.Enabled {
			t.Fatalf("SMTP should be disabled when no host is set")
		}
	})
	t.Run("bad port", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_SMTP_HOST", "mail.example.com")
		t.Setenv("NONBIRI_SMTP_PORT", "99999")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted an out-of-range SMTP port")
		}
	})
	t.Run("bad tls", func(t *testing.T) {
		allEnvs(t)
		t.Setenv("NONBIRI_SMTP_HOST", "mail.example.com")
		t.Setenv("NONBIRI_SMTP_TLS", "bogus")
		if _, err := Load(); err == nil {
			t.Fatalf("Load accepted an invalid SMTP TLS mode")
		}
	})
}
