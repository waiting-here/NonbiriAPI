package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const siteConfigTestNow = int64(1700000000)

type siteConfigTestFinalAuthorizer struct {
	mu      sync.Mutex
	err     error
	inspect func(context.Context, *sql.Tx, int64) error
	calls   int
}

func (authorizer *siteConfigTestFinalAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, adminID int64) error {
	authorizer.mu.Lock()
	authorizer.calls++
	inspect, decision := authorizer.inspect, authorizer.err
	authorizer.mu.Unlock()
	if inspect != nil {
		if err := inspect(ctx, tx, adminID); err != nil {
			return err
		}
	}
	return decision
}

func (authorizer *siteConfigTestFinalAuthorizer) setError(err error) {
	authorizer.mu.Lock()
	authorizer.err = err
	authorizer.mu.Unlock()
}

func newSiteConfigTestRepository(t *testing.T, store *db.Store, authorizer SiteConfigFinalAuthorizer) *SiteConfigRepository {
	t.Helper()
	repository, err := NewSiteConfigRepository(SiteConfigRepositoryOptions{
		Store: store, FinalAuthorizer: authorizer,
		Now: func() time.Time { return time.Unix(siteConfigTestNow, 0) },
	})
	if err != nil {
		t.Fatalf("new site configuration repository: %v", err)
	}
	return repository
}

func siteConfigRawValue(t *testing.T, store *db.Store, key string) (string, bool) {
	t.Helper()
	var value string
	err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read site configuration %q: %v", key, err)
	}
	return value, true
}

func siteConfigIdempotencyCount(t *testing.T, store *db.Store) int {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`).Scan(&count); err != nil {
		t.Fatalf("count site configuration idempotency records: %v", err)
	}
	return count
}

func siteConfigPatch(t *testing.T, repository *SiteConfigRepository, key, raw, idempotencyKey string) (SiteConfigMutationResult, error) {
	t.Helper()
	return repository.PatchSiteConfig(context.Background(), SiteConfigPatchInput{
		AdminID: 1, Key: key, RawValue: json.RawMessage(raw), IdempotencyKey: idempotencyKey,
	})
}

func TestSiteConfigReadDTOsAreClosedAndCatalogComplete(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	publicBootstrap, err := ReadPublicConfig(store)
	if err != nil {
		t.Fatalf("read public bootstrap: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)`, "unknown_catalog_row", "must-not-leak", siteConfigTestNow); err != nil {
		t.Fatalf("seed unknown configuration row: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)`, "alert_prefs_mail", "mail", siteConfigTestNow); err != nil {
		t.Fatalf("seed dynamic alert preference: %v", err)
	}
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)

	bootstrap, err := repository.ReadAdminPublicConfig(context.Background(), 1)
	if err != nil {
		t.Fatalf("read administrator bootstrap: %v", err)
	}
	if !reflect.DeepEqual(bootstrap, publicBootstrap) {
		t.Fatalf("administrator bootstrap=%+v, public bootstrap=%+v", bootstrap, publicBootstrap)
	}
	body, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatalf("marshal administrator bootstrap: %v", err)
	}
	var bootstrapWire map[string]any
	if err := json.Unmarshal(body, &bootstrapWire); err != nil {
		t.Fatalf("decode administrator bootstrap: %v", err)
	}
	wantBootstrapKeys := []string{
		"announcement_epoch", "charity_donation_notice_en", "charity_donation_notice_zh", "legal_authoritative_locale", "legal_privacy_override_en", "legal_privacy_override_zh",
		"legal_terms_override_en", "legal_terms_override_zh", "maintenance_mode", "registration_open", "site_logo_url", "site_name",
	}
	gotBootstrapKeys := make([]string, 0, len(bootstrapWire))
	for key := range bootstrapWire {
		gotBootstrapKeys = append(gotBootstrapKeys, key)
	}
	sort.Strings(gotBootstrapKeys)
	if !reflect.DeepEqual(gotBootstrapKeys, wantBootstrapKeys) {
		t.Fatalf("administrator bootstrap keys=%v, want %v", gotBootstrapKeys, wantBootstrapKeys)
	}

	configuration, err := repository.ReadSiteConfig(context.Background(), 1)
	if err != nil {
		t.Fatalf("read administrator site configuration: %v", err)
	}
	catalog, err := repository.ReadSiteConfigCatalog(context.Background(), 1)
	if err != nil {
		t.Fatalf("read site configuration catalog: %v", err)
	}
	if configuration.Revision != fmt.Sprint(siteConfigRevision(t, store)) {
		t.Fatalf("configuration revision=%q, want %d", configuration.Revision, siteConfigRevision(t, store))
	}
	if len(configuration.Values) != len(catalog.Data) {
		t.Fatalf("configuration values=%d, catalog=%d", len(configuration.Values), len(catalog.Data))
	}
	if _, exists := configuration.Values["unknown_catalog_row"]; exists {
		t.Fatal("unknown configuration row leaked into values")
	}
	if _, exists := configuration.Values["alert_prefs_mail"]; !exists {
		t.Fatal("stored dynamic alert preference is absent from values")
	}
	for _, key := range []string{KeySiteTimezoneOffsetMinutes, KeyCharityTokenReserveMilli, KeyAnthropicDefaultMaxTokens} {
		if value, exists := configuration.Values[key]; !exists || value != nil {
			t.Fatalf("optional configuration %q=%#v, exists=%v; want null", key, value, exists)
		}
	}
	previous := ""
	for _, entry := range catalog.Data {
		if _, exists := configuration.Values[entry.Key]; !exists {
			t.Fatalf("catalog key %q is absent from values", entry.Key)
		}
		order := entry.Group + "\x00" + entry.Key
		if previous != "" && order <= previous {
			t.Fatalf("catalog is not strictly ordered: %q before %q", previous, order)
		}
		previous = order
	}
	second, err := repository.ReadSiteConfigCatalog(context.Background(), 1)
	if err != nil {
		t.Fatalf("repeat site configuration catalog read: %v", err)
	}
	if !reflect.DeepEqual(catalog, second) {
		t.Fatal("site configuration catalog changed between identical reads")
	}
}

func TestSiteConfigPatchReplayConflictAndLiveAuthorization(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)
	initialRevision := siteConfigRevision(t, store)
	key := "site-config-replay-key-0001"

	first, err := siteConfigPatch(t, repository, KeySiteName, `"Corrective Site"`, key)
	if err != nil {
		t.Fatalf("first site configuration patch: %v", err)
	}
	if first.Replayed || first.Response.Key != KeySiteName || first.Response.Value != "Corrective Site" || first.Response.Revision != fmt.Sprint(initialRevision+1) {
		t.Fatalf("first result=%+v", first)
	}
	if got, ok := siteConfigRawValue(t, store, KeySiteName); !ok || got != "Corrective Site" {
		t.Fatalf("stored site name=%q, exists=%v", got, ok)
	}

	replayed, err := siteConfigPatch(t, repository, KeySiteName, `"Corrective Site"`, key)
	if err != nil {
		t.Fatalf("replay site configuration patch: %v", err)
	}
	if !replayed.Replayed || !bytes.Equal(replayed.Body, first.Body) {
		t.Fatalf("replay=%+v body=%q, first=%q", replayed, replayed.Body, first.Body)
	}
	if got := siteConfigRevision(t, store); got != initialRevision+1 {
		t.Fatalf("revision after replay=%d, want %d", got, initialRevision+1)
	}

	if _, err := siteConfigPatch(t, repository, KeySiteName, `"Different"`, key); !errors.Is(err, ErrSiteConfigConflict) {
		t.Fatalf("different digest error=%v, want conflict", err)
	}
	if got, _ := siteConfigRawValue(t, store, KeySiteName); got != "Corrective Site" {
		t.Fatalf("different digest changed site name to %q", got)
	}

	authorizer.setError(authz.ErrForbidden)
	if _, err := siteConfigPatch(t, repository, KeySiteName, `"Corrective Site"`, key); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatalf("unauthorized replay error=%v, want forbidden", err)
	}
	if got := siteConfigRevision(t, store); got != initialRevision+1 {
		t.Fatalf("forbidden replay changed revision to %d", got)
	}
}

func TestSiteConfigPatchRejectsEveryDedicatedAndUnknownKeyWithoutWrites(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	initialRevision := siteConfigRevision(t, store)

	dedicated := make([]string, 0)
	for key := range knownSiteConfig {
		if isSpecializedConfigKey(key) {
			dedicated = append(dedicated, key)
		}
	}
	sort.Strings(dedicated)
	if len(dedicated) < 4 {
		t.Fatalf("dedicated key coverage unexpectedly small: %v", dedicated)
	}
	for index, key := range dedicated {
		idempotencyKey := fmt.Sprintf("site-config-dedicated-%04d", index)
		if _, err := siteConfigPatch(t, repository, key, `null`, idempotencyKey); !errors.Is(err, ErrSiteConfigConflict) {
			t.Fatalf("dedicated key %q error=%v, want conflict", key, err)
		}
	}
	if _, err := siteConfigPatch(t, repository, "default_locale", `"zh"`, "site-config-unknown-key-0001"); !errors.Is(err, ErrSiteConfigNotFound) {
		t.Fatalf("unknown key error=%v, want not found", err)
	}
	if got := siteConfigRevision(t, store); got != initialRevision {
		t.Fatalf("rejected keys changed revision to %d, want %d", got, initialRevision)
	}
	if got := siteConfigIdempotencyCount(t, store); got != 0 {
		t.Fatalf("rejected keys created %d idempotency records", got)
	}
}

func TestSiteConfigPatchScalarBoundariesAndOptionalNull(t *testing.T) {
	valid := []struct {
		name, key, raw, stored string
	}{
		{"boolean false", KeyRegistrationOpen, `false`, "0"},
		{"boolean true", KeyRegistrationOpen, `true`, "1"},
		{"integer minimum", KeyDefaultEndpointLimit, `0`, "0"},
		{"integer maximum", KeyDefaultEndpointLimit, `10000`, "10000"},
		{"amount minimum", KeyLevelThreshold2Milli, `"0"`, "0"},
		{"amount maximum", KeyLevelThreshold2Milli, `"9000000000000"`, "9000000000000000"},
		{"optional amount minimum", KeyCharityTokenReserveMilli, `"0.001"`, "1"},
		{"optional amount maximum", KeyCharityTokenReserveMilli, `"9000000000000"`, "9000000000000000"},
		{"text byte maximum", KeySiteName, `"` + strings.Repeat("s", 256) + `"`, strings.Repeat("s", 256)},
		{"text rune maximum", KeyLevelDisplayName1, `"` + strings.Repeat("界", 64) + `"`, strings.Repeat("界", 64)},
		{"multiline text", KeyLegalTermsOverrideEn, `"first\nsecond\tline"`, "first\nsecond\tline"},
		{"required locale", KeyLegalAuthoritativeLocale, `"zh"`, "zh"},
		{"optional locale empty", KeyLegalAuthoritativeLocale, `""`, ""},
		{"enum", KeyCheckinMode, `"disabled"`, "disabled"},
		{"timezone minimum", KeySiteTimezoneOffsetMinutes, `-720`, "-720"},
		{"timezone maximum", KeySiteTimezoneOffsetMinutes, `840`, "840"},
		{"optional integer minimum", KeyAnthropicDefaultMaxTokens, `1`, "1"},
		{"optional integer maximum", KeyAnthropicDefaultMaxTokens, `2147483647`, "2147483647"},
		{"dynamic alert text", "alert_prefs_delivery", `"mail"`, "mail"},
	}
	for index, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			store := openGenerationTwoPublicConfigStore(t)
			repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
			result, err := siteConfigPatch(t, repository, test.key, test.raw, fmt.Sprintf("site-config-valid-%04d-key", index))
			if err != nil {
				t.Fatalf("valid patch: %v", err)
			}
			if result.Response.Key != test.key {
				t.Fatalf("response key=%q, want %q", result.Response.Key, test.key)
			}
			if got, ok := siteConfigRawValue(t, store, test.key); !ok || got != test.stored {
				t.Fatalf("stored value=%q, exists=%v; want %q", got, ok, test.stored)
			}
		})
	}

	invalid := []struct{ name, key, raw string }{
		{"boolean type", KeyRegistrationOpen, `1`},
		{"integer below", KeyDefaultEndpointLimit, `-1`},
		{"integer above", KeyDefaultEndpointLimit, `10001`},
		{"integer fraction", KeyDefaultEndpointLimit, `1.0`},
		{"amount type", KeyLevelThreshold2Milli, `1`},
		{"amount noncanonical", KeyLevelThreshold2Milli, `"00"`},
		{"amount above", KeyLevelThreshold2Milli, `"9000000000000.001"`},
		{"optional amount zero", KeyCharityTokenReserveMilli, `"0"`},
		{"text empty", KeySiteName, `""`},
		{"text above", KeySiteName, `"` + strings.Repeat("s", 257) + `"`},
		{"text rune above", KeyLevelDisplayName1, `"` + strings.Repeat("界", 65) + `"`},
		{"multiline control", KeyLegalTermsOverrideEn, `"bad\u0000text"`},
		{"locale member", KeyLegalAuthoritativeLocale, `"fr"`},
		{"enum member", KeyCheckinMode, `"sometimes"`},
		{"timezone below", KeySiteTimezoneOffsetMinutes, `-721`},
		{"timezone above", KeySiteTimezoneOffsetMinutes, `841`},
		{"optional integer below", KeyAnthropicDefaultMaxTokens, `0`},
		{"optional integer above", KeyAnthropicDefaultMaxTokens, `2147483648`},
		{"dynamic alert above", "alert_prefs_delivery", `"` + strings.Repeat("a", 513) + `"`},
	}
	for index, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			store := openGenerationTwoPublicConfigStore(t)
			repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
			initialRevision := siteConfigRevision(t, store)
			before, existed := siteConfigRawValue(t, store, test.key)
			if _, err := siteConfigPatch(t, repository, test.key, test.raw, fmt.Sprintf("site-config-invalid-%04d", index)); !errors.Is(err, ErrSiteConfigInvalid) {
				t.Fatalf("invalid patch error=%v, want invalid", err)
			}
			if got := siteConfigRevision(t, store); got != initialRevision {
				t.Fatalf("invalid patch changed revision to %d", got)
			}
			after, afterExists := siteConfigRawValue(t, store, test.key)
			if before != after || existed != afterExists || siteConfigIdempotencyCount(t, store) != 0 {
				t.Fatalf("invalid patch changed state: before=(%q,%v) after=(%q,%v)", before, existed, after, afterExists)
			}
		})
	}

	t.Run("optional null deletes", func(t *testing.T) {
		store := openGenerationTwoPublicConfigStore(t)
		repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
		if _, err := siteConfigPatch(t, repository, KeyAnthropicDefaultMaxTokens, `1024`, "site-config-optional-set-0001"); err != nil {
			t.Fatalf("set optional integer: %v", err)
		}
		result, err := siteConfigPatch(t, repository, KeyAnthropicDefaultMaxTokens, `null`, "site-config-optional-clear-001")
		if err != nil {
			t.Fatalf("clear optional integer: %v", err)
		}
		if result.Response.Value != nil {
			t.Fatalf("clear response value=%#v, want null", result.Response.Value)
		}
		if _, exists := siteConfigRawValue(t, store, KeyAnthropicDefaultMaxTokens); exists {
			t.Fatal("optional integer row remains after null patch")
		}
	})
}

func TestSiteConfigPatchCombinationTimezoneAndCompletionRollback(t *testing.T) {
	t.Run("combination", func(t *testing.T) {
		store := openGenerationTwoPublicConfigStore(t)
		repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
		initialRevision := siteConfigRevision(t, store)
		if _, err := siteConfigPatch(t, repository, KeyDonationAcceptEnabled, `true`, "site-config-combination-0001"); !errors.Is(err, ErrSiteConfigConflict) {
			t.Fatalf("invalid combination error=%v, want conflict", err)
		}
		if siteConfigRevision(t, store) != initialRevision || siteConfigIdempotencyCount(t, store) != 0 {
			t.Fatal("invalid combination changed revision or idempotency state")
		}
	})

	t.Run("timezone lock", func(t *testing.T) {
		store := openGenerationTwoPublicConfigStore(t)
		if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_locked','1',?)`, siteConfigTestNow); err != nil {
			t.Fatalf("seed timezone lock: %v", err)
		}
		repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
		initialRevision := siteConfigRevision(t, store)
		if _, err := siteConfigPatch(t, repository, KeySiteTimezoneOffsetMinutes, `60`, "site-config-timezone-lock-01"); !errors.Is(err, ErrSiteConfigConflict) {
			t.Fatalf("locked timezone error=%v, want conflict", err)
		}
		if siteConfigRevision(t, store) != initialRevision || siteConfigIdempotencyCount(t, store) != 0 {
			t.Fatal("locked timezone patch changed revision or idempotency state")
		}
	})

	t.Run("completion failure", func(t *testing.T) {
		store := openGenerationTwoPublicConfigStore(t)
		if _, err := store.DB().Exec(`CREATE TRIGGER site_config_test_fail_completion
			BEFORE UPDATE OF state ON idempotency_records
			WHEN NEW.state='completed'
			BEGIN SELECT RAISE(ABORT,'forced completion failure'); END`); err != nil {
			t.Fatalf("create completion failure trigger: %v", err)
		}
		repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
		initialRevision := siteConfigRevision(t, store)
		before, existed := siteConfigRawValue(t, store, KeySiteName)
		if _, err := siteConfigPatch(t, repository, KeySiteName, `"rollback"`, "site-config-rollback-key-01"); !errors.Is(err, ErrSiteConfigInvariant) {
			t.Fatalf("completion failure error=%v, want invariant", err)
		}
		after, afterExists := siteConfigRawValue(t, store, KeySiteName)
		if before != after || existed != afterExists || siteConfigRevision(t, store) != initialRevision || siteConfigIdempotencyCount(t, store) != 0 {
			t.Fatal("completion failure did not roll back domain and idempotency writes")
		}
	})
}

type siteConfigBarrierAuthorizer struct {
	arrived atomic.Int32
	release chan struct{}
}

func (authorizer *siteConfigBarrierAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, _ int64) error {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&revision); err != nil {
		return err
	}
	if authorizer.arrived.Add(1) == 2 {
		close(authorizer.release)
	}
	select {
	case <-authorizer.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openSharedSiteConfigStores(t *testing.T) (*db.Store, *db.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared-site-config.db")
	masterKey := bytes.Repeat([]byte{0x67}, secret.MasterKeyBytes)
	vault, err := secret.New(masterKey)
	clear(masterKey)
	if err != nil {
		t.Fatalf("create shared test vault: %v", err)
	}
	dbtest.EnsureOwnerOnlyParent(t, path)
	first, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("open first shared store: %v", err)
	}
	second, err := db.Open(path, vault)
	if err != nil {
		_ = first.Close()
		_ = vault.Close()
		t.Fatalf("open second shared store: %v", err)
	}
	t.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
		_ = vault.Close()
	})
	return first, second
}

func TestSiteConfigConcurrentRevisionHasOneWinner(t *testing.T) {
	firstStore, secondStore := openSharedSiteConfigStores(t)
	authorizer := &siteConfigBarrierAuthorizer{release: make(chan struct{})}
	first := newSiteConfigTestRepository(t, firstStore, authorizer)
	second := newSiteConfigTestRepository(t, secondStore, authorizer)
	initialRevision := siteConfigRevision(t, firstStore)

	type outcome struct {
		key string
		err error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		_, err := first.PatchSiteConfig(context.Background(), SiteConfigPatchInput{
			AdminID: 1, Key: KeySiteName, RawValue: json.RawMessage(`"winner-a"`), IdempotencyKey: "site-config-concurrent-key-a",
		})
		outcomes <- outcome{key: KeySiteName, err: err}
	}()
	go func() {
		_, err := second.PatchSiteConfig(context.Background(), SiteConfigPatchInput{
			AdminID: 1, Key: KeySiteLogoURL, RawValue: json.RawMessage(`"https://example.test/winner-b.png"`), IdempotencyKey: "site-config-concurrent-key-b",
		})
		outcomes <- outcome{key: KeySiteLogoURL, err: err}
	}()

	results := []outcome{<-outcomes, <-outcomes}
	successes, conflicts := 0, 0
	for _, result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrSiteConfigConflict):
			conflicts++
		default:
			t.Fatalf("concurrent %s error=%v", result.key, result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes=%+v; successes=%d conflicts=%d", results, successes, conflicts)
	}
	if got := siteConfigRevision(t, firstStore); got != initialRevision+1 {
		t.Fatalf("concurrent revision=%d, want %d", got, initialRevision+1)
	}
	changed := 0
	if value, _ := siteConfigRawValue(t, firstStore, KeySiteName); value == "winner-a" {
		changed++
	}
	if value, _ := siteConfigRawValue(t, firstStore, KeySiteLogoURL); value == "https://example.test/winner-b.png" {
		changed++
	}
	if changed != 1 || siteConfigIdempotencyCount(t, firstStore) != 1 {
		t.Fatalf("concurrent changed=%d idempotency=%d, want one each", changed, siteConfigIdempotencyCount(t, firstStore))
	}
}
