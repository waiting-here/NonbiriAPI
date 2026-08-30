package adminapi

// ReadPublicConfig allowlist tests: the unauthenticated /api/config
// bootstrap must project only the frozen ten fields and never operational,
// rate-limit or Discord secrets — even when those are stored. Adding a key
// to knownSiteConfig does not expose it; the concrete DTO is the boundary.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func openGenerationTwoPublicConfigStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "public-config.db")
	masterKey := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(masterKey)
	clear(masterKey)
	if err != nil {
		t.Fatalf("create Generation 2 test secret codec: %v", err)
	}
	t.Cleanup(func() {
		if err := vault.Close(); err != nil {
			t.Errorf("close Generation 2 test secret codec: %v", err)
		}
	})
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open Generation 2 test store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Generation 2 test store: %v", err)
		}
	})
	return store
}

func siteConfigRowCount(t *testing.T, store *db.Store) int {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM site_config`).Scan(&count); err != nil {
		t.Fatalf("count site_config rows: %v", err)
	}
	return count
}

func siteConfigRevision(t *testing.T, store *db.Store) int64 {
	t.Helper()
	var revision int64
	if err := store.DB().QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&revision); err != nil {
		t.Fatalf("read site config revision: %v", err)
	}
	return revision
}

func assertSiteConfigSnapshotEqual(t *testing.T, want, got map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("site config snapshot size=%d, want %d; got=%v", len(got), len(want), got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("site config %q=%q, want %q; got=%v", key, got[key], value, got)
		}
	}
}

func TestReadPublicConfigProjectsOnlyAllowlist(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)

	// Set display keys plus a broad set of sensitive/operational keys.
	if err := store.SetSiteConfigValue("site_name", "Nonbiri Trial"); err != nil {
		t.Fatalf("set site_name: %v", err)
	}
	if err := store.SetSiteConfigValue("site_logo_url", "https://cdn.example/logo.png"); err != nil {
		t.Fatalf("set site_logo_url: %v", err)
	}
	for _, kv := range []struct{ k, v string }{
		{"legal_authoritative_locale", "zh"},
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
	} {
		if err := store.SetSiteConfigValue(kv.k, kv.v); err != nil {
			t.Fatalf("set %s: %v", kv.k, err)
		}
	}

	out, err := ReadPublicConfig(store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}

	// Exactly the ten frozen DTO fields, nothing else.
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal public config: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode public config wire: %v", err)
	}
	var keys []string
	for k := range wire {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{
		"announcement_epoch",
		"legal_authoritative_locale",
		"legal_privacy_override_en",
		"legal_privacy_override_zh",
		"legal_terms_override_en",
		"legal_terms_override_zh",
		"maintenance_mode",
		"registration_open",
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

	if out.SiteName != "Nonbiri Trial" {
		t.Fatalf("site_name=%v", out.SiteName)
	}
	if out.SiteLogoURL != "https://cdn.example/logo.png" {
		t.Fatalf("site_logo_url=%v", out.SiteLogoURL)
	}
	if !db.ValidateOpaqueID(out.AnnouncementEpoch, "b1e_") {
		t.Fatalf("announcement_epoch=%v", out.AnnouncementEpoch)
	}
	if out.LegalAuthoritativeLocale != "zh" {
		t.Fatalf("legal_authoritative_locale=%v", out.LegalAuthoritativeLocale)
	}
	if !out.MaintenanceMode {
		t.Fatalf("maintenance_mode=%v want true", out.MaintenanceMode)
	}
	if out.RegistrationOpen {
		t.Fatalf("registration_open=%v want false", out.RegistrationOpen)
	}

	// Defensive: none of the sensitive values may appear anywhere in the
	// projection (the concrete DTO is the single boundary; this guards against
	// a future operational field accidentally being added to it).
	for k, v := range wire {
		s := strings.ToLower(toString(v))
		for _, secret := range []string{"secret-guild", "secret-role", "999"} {
			if strings.Contains(s, secret) {
				t.Fatalf("public projection leaked %q via %s", secret, k)
			}
		}
	}
}

func TestReadPublicConfigFreshStoreYieldsDefaults(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	out, err := ReadPublicConfig(store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}
	// Generation 2 fresh seed is fail-closed for these public toggles while
	// display/legal rows remain blank and the epoch is a fresh opaque ID.
	if out.SiteName != "" {
		t.Fatalf("site_name=%v want empty", out.SiteName)
	}
	if out.SiteLogoURL != "" {
		t.Fatalf("site_logo_url=%v want empty", out.SiteLogoURL)
	}
	if !db.ValidateOpaqueID(out.AnnouncementEpoch, "b1e_") {
		t.Fatalf("announcement_epoch=%v", out.AnnouncementEpoch)
	}
	// Legal overrides default to blank (fallback to built-in template);
	// the authoritative locale defaults to blank (no authoritative notice).
	for key, value := range map[string]string{
		"legal_privacy_override_zh": out.LegalPrivacyOverrideZh,
		"legal_privacy_override_en": out.LegalPrivacyOverrideEn,
		"legal_terms_override_zh":   out.LegalTermsOverrideZh,
		"legal_terms_override_en":   out.LegalTermsOverrideEn,
	} {
		if value != "" {
			t.Fatalf("%s=%v want empty", key, value)
		}
	}
	if out.LegalAuthoritativeLocale != "" {
		t.Fatalf("legal_authoritative_locale=%v want empty", out.LegalAuthoritativeLocale)
	}
	if !out.MaintenanceMode {
		t.Fatalf("maintenance_mode=%v want true", out.MaintenanceMode)
	}
	if out.RegistrationOpen {
		t.Fatalf("registration_open=%v want false", out.RegistrationOpen)
	}
	values, err := store.GetAllSiteConfigValues()
	if err != nil {
		t.Fatalf("read fresh site config: %v", err)
	}
	if _, exists := values["default_locale"]; exists {
		t.Fatal("fresh Generation 2 store seeded deleted default_locale")
	}
	if values[KeyMaintenanceMode] != "1" || values[KeyRegistrationOpen] != "0" {
		t.Fatalf("fresh raw public toggles=(%q,%q), want (1,0)", values[KeyMaintenanceMode], values[KeyRegistrationOpen])
	}
}

func TestGenerationTwoSiteConfigRejectsUnknownDeletedAndSpecializedWrites(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	beforeValues, err := store.GetAllSiteConfigValues()
	if err != nil {
		t.Fatalf("read initial site config: %v", err)
	}
	beforeRows := siteConfigRowCount(t, store)
	beforeRevision := siteConfigRevision(t, store)

	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "unknown", key: "nonsense_key", value: "x"},
		{name: "deleted default locale", key: "default_locale", value: "zh"},
		{name: "maintenance specialized", key: KeyMaintenanceMode, value: "0"},
		{name: "announcement epoch read-only", key: KeyAnnouncementEpoch, value: "b1e_QQQQQQQQQQQQQQQQQQQQQQ"},
		{name: "games specialized", key: KeyGamesEnabled, value: "1"},
		{name: "activities specialized", key: KeyActivitiesEnabled, value: "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.SetSiteConfigValue(tc.key, tc.value); err == nil {
				t.Fatalf("SetSiteConfigValue(%q) unexpectedly succeeded", tc.key)
			}
			if got := siteConfigRowCount(t, store); got != beforeRows {
				t.Fatalf("site_config rows=%d after rejected %q, want %d", got, tc.key, beforeRows)
			}
			if got := siteConfigRevision(t, store); got != beforeRevision {
				t.Fatalf("site config revision=%d after rejected %q, want %d", got, tc.key, beforeRevision)
			}
			gotValues, err := store.GetAllSiteConfigValues()
			if err != nil {
				t.Fatalf("read site config after rejected %q: %v", tc.key, err)
			}
			assertSiteConfigSnapshotEqual(t, beforeValues, gotValues)
		})
	}
}

func TestReadPublicConfigNilStoreYieldsDefaults(t *testing.T) {
	out, err := ReadPublicConfig(nil)
	if err != nil {
		t.Fatalf("ReadPublicConfig(nil): %v", err)
	}
	if out.SiteName != "" || out.SiteLogoURL != "" || out.AnnouncementEpoch != "" {
		t.Fatalf("nil store projection=%v want all defaults", out)
	}
	if out.MaintenanceMode || !out.RegistrationOpen {
		t.Fatalf("nil store toggles=%v want maintenance=false registration=true", out)
	}
}

// Legal override text preserves newlines and tabs (operators author
// multi-paragraph documents) but a stored value with disallowed control
// characters falls back to blank rather than leaking them.
func TestReadPublicConfigLegalOverridePreservesNewlines(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	const doc = "## Operator\n\nAcme Corp.\n\t- item one\n- item two"
	for _, k := range []string{"legal_privacy_override_zh", "legal_terms_override_en"} {
		if err := store.SetSiteConfigValue(k, doc); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	if err := store.SetSiteConfigValue("legal_authoritative_locale", "zh"); err != nil {
		t.Fatalf("set authoritative: %v", err)
	}
	out, err := ReadPublicConfig(store)
	if err != nil {
		t.Fatalf("ReadPublicConfig: %v", err)
	}
	if out.LegalPrivacyOverrideZh != doc {
		t.Fatalf("privacy zh=%q want %q", out.LegalPrivacyOverrideZh, doc)
	}
	if out.LegalTermsOverrideEn != doc {
		t.Fatalf("terms en=%q want %q", out.LegalTermsOverrideEn, doc)
	}
	if out.LegalAuthoritativeLocale != "zh" {
		t.Fatalf("authoritative=%v want zh", out.LegalAuthoritativeLocale)
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
