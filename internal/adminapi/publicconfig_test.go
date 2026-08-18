package adminapi

// ReadPublicConfig allowlist tests: the unauthenticated /api/config
// bootstrap must project only display keys (site_name, site_logo_url,
// default_locale) and never operational, gating, rate-limit or Discord
// secrets — even when those are stored. Adding a key to knownSiteConfig
// does NOT expose it; only publicSiteConfigKeys is projected.

import (
	"sort"
	"strings"
	"testing"
)

func TestReadPublicConfigProjectsOnlyAllowlist(t *testing.T) {
	e := newEnv(t)

	// Set display keys plus a broad set of sensitive/operational keys.
	if err := e.store.SetSiteConfigValue("site_name", "Nonbiri Trial"); err != nil {
		t.Fatalf("set site_name: %v", err)
	}
	if err := e.store.SetSiteConfigValue("site_logo_url", "https://cdn.example/logo.png"); err != nil {
		t.Fatalf("set site_logo_url: %v", err)
	}
	if err := e.store.SetSiteConfigValue("default_locale", "zh"); err != nil {
		t.Fatalf("set default_locale: %v", err)
	}
	for _, kv := range []struct{ k, v string }{
		{"default_rpm_per_user", "999"},
		{"global_rpm", "999"},
		{"default_per_endpoint_concurrency", "999"},
		{"egress_global_concurrency", "999"},
		{"default_endpoint_limit", "999"},
		{"discord_guild_id", "secret-guild"},
		{"discord_role_id", "secret-role"},
		{"oauth_start_rate_limit", "999"},
		{"oauth_start_rate_window_seconds", "999"},
		{"oauth_start_rate_penalty_seconds", "999"},
		{"alert_prefs_warn_429", "true"},
		{"nonsense_key", "x"},
	} {
		if err := e.store.SetSiteConfigValue(kv.k, kv.v); err != nil {
			t.Fatalf("set %s: %v", kv.k, err)
		}
	}

	out, err := ReadPublicConfig(e.store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}

	// Exactly the allowlist keys, nothing else.
	var keys []string
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{
		"default_locale",
		"legal_authoritative_locale",
		"legal_privacy_override_en",
		"legal_privacy_override_zh",
		"legal_terms_override_en",
		"legal_terms_override_zh",
		"site_logo_url",
		"site_name",
	}
	if len(keys) != len(want) {
		t.Fatalf("projected keys=%v want=%v", keys, want)
	}
	for i, k := range keys {
		if k != want[i] {
			t.Fatalf("projected keys=%v want=%v", keys, want)
		}
	}

	if out["site_name"] != "Nonbiri Trial" {
		t.Fatalf("site_name=%v", out["site_name"])
	}
	if out["site_logo_url"] != "https://cdn.example/logo.png" {
		t.Fatalf("site_logo_url=%v", out["site_logo_url"])
	}
	if out["default_locale"] != "zh" {
		t.Fatalf("default_locale=%v", out["default_locale"])
	}

	// Defensive: none of the sensitive values may appear anywhere in the
	// projection (the allowlist is the single boundary; this guards against a
	// future key accidentally being added to publicSiteConfigKeys).
	for k, v := range out {
		s := strings.ToLower(toString(v))
		for _, secret := range []string{"secret-guild", "secret-role", "999"} {
			if strings.Contains(s, secret) {
				t.Fatalf("public projection leaked %q via %s", secret, k)
			}
		}
	}
}

func TestReadPublicConfigEmptyStoreYieldsDefaults(t *testing.T) {
	e := newEnv(t)
	out, err := ReadPublicConfig(e.store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}
	// Unset display keys fall back to typed defaults: name/logo blank, locale "".
	if out["site_name"] != "" {
		t.Fatalf("site_name=%v want empty", out["site_name"])
	}
	if out["site_logo_url"] != "" {
		t.Fatalf("site_logo_url=%v want empty", out["site_logo_url"])
	}
	if out["default_locale"] != "" {
		t.Fatalf("default_locale=%v want empty", out["default_locale"])
	}
	// Legal overrides default to blank (fallback to built-in template);
	// the authoritative locale defaults to blank (no authoritative notice).
	for _, k := range []string{
		"legal_privacy_override_zh", "legal_privacy_override_en",
		"legal_terms_override_zh", "legal_terms_override_en",
	} {
		if out[k] != "" {
			t.Fatalf("%s=%v want empty", k, out[k])
		}
	}
	if out["legal_authoritative_locale"] != "" {
		t.Fatalf("legal_authoritative_locale=%v want empty", out["legal_authoritative_locale"])
	}
}

func TestReadPublicConfigNilStoreYieldsDefaults(t *testing.T) {
	out, err := ReadPublicConfig(nil)
	if err != nil {
		t.Fatalf("ReadPublicConfig(nil): %v", err)
	}
	if out["site_name"] != "" || out["site_logo_url"] != "" || out["default_locale"] != "" {
		t.Fatalf("nil store projection=%v want all defaults", out)
	}
}

// Legal override text preserves newlines and tabs (operators author
// multi-paragraph documents) but a stored value with disallowed control
// characters falls back to blank rather than leaking them.
func TestReadPublicConfigLegalOverridePreservesNewlines(t *testing.T) {
	e := newEnv(t)
	const doc = "## Operator\n\nAcme Corp.\n\t- item one\n- item two"
	for _, k := range []string{"legal_privacy_override_zh", "legal_terms_override_en"} {
		if err := e.store.SetSiteConfigValue(k, doc); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	if err := e.store.SetSiteConfigValue("legal_authoritative_locale", "zh"); err != nil {
		t.Fatalf("set authoritative: %v", err)
	}
	out, err := ReadPublicConfig(e.store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}
	if out["legal_privacy_override_zh"] != doc {
		t.Fatalf("privacy zh=%q want %q", out["legal_privacy_override_zh"], doc)
	}
	if out["legal_terms_override_en"] != doc {
		t.Fatalf("terms en=%q want %q", out["legal_terms_override_en"], doc)
	}
	if out["legal_authoritative_locale"] != "zh" {
		t.Fatalf("authoritative=%v want zh", out["legal_authoritative_locale"])
	}
}

func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}
