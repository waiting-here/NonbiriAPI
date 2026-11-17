package v1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func TestModelConsumptionUpgradePreservesHistoryAndLiveHolds(t *testing.T) {
	path, vault := bootstrapTestPath(t, "model-consumption.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	physical := seedEndpointKeyHistory(t, database, vault, 'Q')
	var owner int64
	if err := database.QueryRow("SELECT user_id FROM endpoints WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)", physical).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	key := hostileInsertDonationKey(t, database, hostileInsertDonation(t, database, owner), physical)
	restoreSchema54LayoutForTest(t, database)
	hostileMustExec(t, database, "UPDATE schema_state SET version=54")
	hostileMustExec(t, database, `INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,token_limit_mag,tokens_used,tokens_reserved,created_at) VALUES(?,'alpha',nbi_u128(20),nbi_u128(15),nbi_u128(2),11),(?,'beta',NULL,nbi_u128(8),zeroblob(16),12)`, key, key)
	for i, model := range []string{"alpha", "alpha", "alpha", "", "beta"} {
		id, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			hostileMustExec(t, database, `INSERT INTO donation_usage_reservations(claim_id,donation_key_id,upstream_model_id,claim_seq,streak_generation,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at) VALUES(?,?,?,nbi_u128(?),nbi_u128(1),1250,1,2,'reserved',12)`, id, key, model, i+1)
		} else {
			state, disposition, origin := "committed", "success", "none"
			price, calls, tokens, success := 1200+i, 1, 2, 1
			if i == 2 {
				state, disposition, origin = "released", "neutral", "platform"
				price, calls, tokens, success = 0, 0, 0, 0
			}
			hostileMustExec(t, database, `INSERT INTO donation_usage_reservations(claim_id,donation_key_id,upstream_model_id,claim_seq,streak_generation,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at,finalized_at,price_actual_milli,reward_actual_milli,calls_actual,tokens_actual,protocol_success,usage_unknown,streak_disposition,failure_origin) VALUES(?,?,?,nbi_u128(?),nbi_u128(1),1250,1,2,?,12,13,?,0,?,?,?,0,?,?)`, id, key, model, i+1, state, price, calls, tokens, success, disposition, origin)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("service accepted source schema")
	}
	index := migrationIndex(t, 54)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			return err
		}
		return errors.New("injected after quota backfill")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("failed conversion committed")
	}
	storageSchema.versions[index].apply = original
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
	explicitUpgrade(t, path, vault)
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
