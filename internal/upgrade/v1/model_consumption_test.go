package v1

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestModelConsumptionUpgradePreservesHistoryAndLiveHolds(t *testing.T) {
	path, vault := bootstrapTestPath(t, "model-consumption.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	const key = 1
	mustExec(t, database, `INSERT INTO donations(id,status,revision,description,review_note,created_at,updated_at) VALUES(1,'approved',1,'','',0,0)`)
	mustExec(t, database, `INSERT INTO donation_keys(id,donation_id,source_endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,ended_at,ended_reason,report_match_until,report_fingerprint) VALUES(1,1,1,nbi_u128(9000),nbi_u128(1250),nbi_u128(9),nbi_u128(1),nbi_u128(23),nbi_u128(2),zeroblob(16),nbi_u128(1),nbi_u128(6),zeroblob(16),0,1,1,1,'terminated',7776001,zeroblob(32))`)
	restoreSourceLayout(t, database)
	mustExec(t, database, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,token_limit_mag,tokens_used,tokens_reserved,created_at) VALUES(?,'alpha',nbi_u128(20),nbi_u128(15),nbi_u128(2),11),(?,'beta',NULL,nbi_u128(8),zeroblob(16),12)`, key, key)
	for i, model := range []string{"alpha", "alpha", "alpha", "", "beta"} {
		id, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			mustExec(t, database, `INSERT INTO donation_usage_reservations(claim_id,donation_key_id,upstream_model_id,claim_seq,streak_generation,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at) VALUES(?,?,?,nbi_u128(?),nbi_u128(1),1250,1,2,'reserved',12)`, id, key, model, i+1)
		} else {
			state, disposition, origin := "committed", "success", "none"
			price, calls, tokens, success := 1200+i, 1, 2, 1
			if i == 2 {
				state, disposition, origin = "released", "neutral", "platform"
				price, calls, tokens, success = 0, 0, 0, 0
			}
			mustExec(t, database, `INSERT INTO donation_usage_reservations(claim_id,donation_key_id,upstream_model_id,claim_seq,streak_generation,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at,finalized_at,price_actual_milli,reward_actual_milli,calls_actual,tokens_actual,protocol_success,usage_unknown,streak_disposition,failure_origin) VALUES(?,?,?,nbi_u128(?),nbi_u128(1),1250,1,2,?,12,13,?,0,?,?,?,0,?,?)`, id, key, model, i+1, state, price, calls, tokens, success, disposition, origin)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("service accepted source schema")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = Run(ctx, path, vault, Options{Apply: true, Progress: func(p Progress) {
		if p.Phase == "commit" && p.State == "started" {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled upgrade", err)
	}
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, source *sql.DB) error {
		assertRetainedManifest(t, source, db.Schema54ManifestHash)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 54 {
		t.Fatal(checked, err)
	}
	if converted, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil || converted.Status != "converted" {
		t.Fatal(converted, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	read := func(model string) donationquota.ModelView {
		t.Helper()
		view, err := donationquota.ModelUsage(t.Context(), store.DB(), key, model)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	a, b := read("alpha"), read("beta")
	if !a.HistoricalCostsPartial || a.Used.Credits != "1.201" || a.Used.Calls != "1" || a.Reserved.Credits != "1.25" || a.Reserved.Calls != "1" || a.Limits.Calls != nil || a.Limits.Credits != nil || a.Used.Tokens != "15" || a.Reserved.Tokens != "2" || *a.Remaining.Tokens != "3" || *a.TrackingStartedAt != 11 {
		t.Fatal(a)
	}
	if !b.HistoricalCostsPartial || b.Used.Credits != "1.204" || b.Used.Calls != "1" || b.Reserved.Credits != "0" || b.Reserved.Calls != "0" || b.Used.Tokens != "8" {
		t.Fatal(b)
	}
	if fresh := read("fresh"); fresh.HistoricalCostsPartial || fresh.Used.Credits != "0" || fresh.TrackingStartedAt != nil {
		t.Fatal(fresh)
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
}

func bootstrapTestPath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	dbtest.EnsureOwnerOnlyParent(t, path)
	return path
}

func bootstrapTestVault(t *testing.T) *secret.Vault {
	t.Helper()
	return testVault(t, 0x29)
}

func testVault(t *testing.T, marker byte) *secret.Vault {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{marker}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func assertRetainedManifest(t *testing.T, source *sql.DB, want string) {
	t.Helper()
	hash, err := db.SchemaManifestHash(t.Context(), source)
	if err != nil || hash != want {
		t.Fatal(hash, err)
	}
}

func mustExec(t *testing.T, source *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := source.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func restoreSourceLayout(t *testing.T, source *sql.DB) {
	t.Helper()
	mustExec(t, source, "DROP TRIGGER google_model_target_update")
	mustExec(t, source, "DROP TRIGGER google_model_target_delete")
	mustExec(t, source, "DROP TABLE donation_service_model_cooldowns")
	mustExec(t, source, "DROP INDEX idx_retry_pending_health")
	mustExec(t, source, "ALTER TABLE dispatch_retry_after_observations DROP COLUMN pending_health_id")
	mustExec(t, source, "DROP INDEX idx_donation_keys_due_expiry")
	for _, column := range []string{"historical_costs_partial", "price_reserved_mag", "calls_reserved", "price_used_mag", "calls_used", "price_limit_mag", "call_limit_mag"} {
		mustExec(t, source, "ALTER TABLE donation_model_quotas DROP COLUMN "+column)
	}
	mustExec(t, source, "UPDATE schema_state SET version=54")
	assertRetainedManifest(t, source, db.Schema54ManifestHash)
}

func TestConversionWaitsForExclusiveDatabaseAccess(t *testing.T) {
	vault := bootstrapTestVault(t)
	path := bootstrapTestPath(t, "locked.sqlite")
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSourceLayout(t, store.DB())
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("SELECT version FROM schema_state").Scan(&version); err != nil || version != SourceSchemaVersion {
		t.Fatal(version, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, path, vault, Options{Apply: true}); err == nil {
		t.Fatal("conversion bypassed the active SQLite reader")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, reader, "PRAGMA journal_mode=WAL")
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if result, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, apply := range []bool{false, true} {
		if _, err := Run(t.Context(), path, vault, Options{Apply: apply}); !errors.Is(err, db.ErrDatabaseInUse) {
			t.Fatal("owned database accepted", err)
		}
	}
}

func TestOfflineCheckRejectsBrokenLedgerAndStoredCredentials(t *testing.T) {
	for _, version := range []int{SourceSchemaVersion, db.CurrentSchemaVersion} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			vault := bootstrapTestVault(t)
			path := bootstrapTestPath(t, "audit.sqlite")
			store, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			if version == SourceSchemaVersion {
				restoreSourceLayout(t, store.DB())
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			mutate := func(query string) {
				t.Helper()
				data, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer data.Close()
				mustExec(t, data, query)
			}
			for _, tc := range []struct{ topic, damage, restore string }{
				{"ledger and reservation audit", "UPDATE credit_capacity SET reserved_future_rows=nbi_u128(1)", "UPDATE credit_capacity SET reserved_future_rows=zeroblob(16)"},
				{"stored credential audit", "INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at) VALUES(1,zeroblob(16),'https://upstream.example/v1','openai-compatible','invalid',1,1)", "DELETE FROM endpoint_key_secrets WHERE id=1"},
			} {
				mutate(tc.damage)
				if _, err := Run(t.Context(), path, vault, Options{}); err == nil || !strings.Contains(err.Error(), tc.topic) || strings.Contains(err.Error(), "schema_mismatch") || strings.Contains(err.Error(), "upstream.example") {
					t.Fatalf("expected safe %s error, got %v", tc.topic, err)
				}
				mutate(tc.restore)
			}
			if got, err := Run(t.Context(), path, vault, Options{}); err != nil || got.SourceSchemaVersion != version {
				t.Fatal(got, err)
			}
		})
	}
}
