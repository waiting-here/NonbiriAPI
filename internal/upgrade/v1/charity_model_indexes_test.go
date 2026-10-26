package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

var charityModelDeletionIndexes = []string{
	"idx_charity_reservations_model", "idx_charity_dispatch_receipts_model",
	"idx_charity_affinities_model", "idx_charity_header_sessions_model", "idx_charity_header_uses_model",
}

func dropCharityModelIndexesForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, index := range charityModelDeletionIndexes {
		hostileMustExec(t, database, "DROP INDEX "+index)
	}
}

func schema37Fixture(t *testing.T) (string, *secret.Vault, *db.Store) {
	t.Helper()
	path, vault := bootstrapTestPath(t, "schema37.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := seedEndpointKeyHistory(t, store.DB(), vault, 'A')
	hostileMustExec(t, store.DB(), `INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,binding_revision,created_at,updated_at) VALUES('saved','model','[公益]saved/model','per_request',3,7,1,2)`)
	database := store.DB()
	user := hostileInsertUser(t, database, "index-history", 0, 0)
	var model int64
	if err := database.QueryRow("SELECT id FROM charity_models WHERE provider='saved' AND model='model'").Scan(&model); err != nil {
		t.Fatal(err)
	}
	request, attempt := hostileOIDVariant("req_", 'B', 'Q'), hostileOIDVariant("clm_", 'B', 'Q')
	hostileInsertLogicalRequest(t, database, request, user, "charity_chat_completions", 1)
	hostileMustExec(t, database, `INSERT INTO charity_reservations(logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,discount_percent,request_user_price_milli,request_donor_reward_milli,uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,uncached_donor_reward_milli,cache_write_donor_reward_milli,cache_read_donor_reward_milli,output_donor_reward_milli,token_reserve_milli,user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,created_at,updated_at) VALUES(?,?,?,'saved-model','reserved','per_request',100,7,0,0,0,0,0,0,0,0,0,0,0,0,0,zeroblob(16),0,0)`, request, user, model)
	hostileMustExec(t, database, `INSERT INTO charity_dispatch_receipts(attempt_id,physical_id,endpoint_key_id,user_id,model_id,routing_revision,state,reserved_at,expires_at) VALUES(?,zeroblob(32),?,?,?,1,'reserved',0,300)`, attempt, key, user, model)
	hostileMustExec(t, database, `INSERT INTO charity_key_affinities(user_id,model_id,endpoint_key_id,physical_id,attempt_id,routing_revision,dispatched_at,expires_at) VALUES(?,?,?,zeroblob(32),?,1,0,300)`, user, model, key, attempt)
	hostileMustExec(t, database, `INSERT INTO charity_header_sessions(user_id,model_id,name,value,format,idle_seconds,expires_at) VALUES(?,?,'header','12345678901234567890123456789012','hex_32',300,300)`, user, model)
	hostileMustExec(t, database, `INSERT INTO charity_header_uses(logical_request_id,user_id,model_id,name,value,idle_seconds,sent) VALUES(?,?,?,'header','12345678901234567890123456789012',300,1)`, request, user, model)
	dropCharityModelIndexesForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=37")
	if hash, err := db.SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != db.Schema37ManifestHash {
		t.Fatal("source37 fixture drift", hash, err)
	}
	return path, vault, store
}

func TestSchema37OfflineIndexesPreserveFactsAndRetry(t *testing.T) {
	for _, failure := range []string{"none", "partial-ddl", "target-mismatch", "cancel-before-commit"} {
		t.Run(failure, func(t *testing.T) {
			path, vault, source := schema37Fixture(t)
			before := gatewayRows(t, source.DB())
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := Run(t.Context(), path, vault, Options{})
			if err != nil || result.Status != "ready" || result.SourceSchemaVersion != 37 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
				t.Fatal(result, err)
			}
			if failure != "none" {
				index := migrationIndex(t, 37)
				original := storageSchema.versions[index].sql
				defer func() { storageSchema.versions[index].sql = original }()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var codec secret.GenerationTwoContextCodec = vault
				switch failure {
				case "partial-ddl":
					storageSchema.versions[index].sql = strings.Split(original, ";")[0] + "; SELECT missing_migration_function();"
				case "target-mismatch":
					storageSchema.versions[index].sql = strings.Split(original, ";")[0] + ";"
				case "cancel-before-commit":
					codec = &cancelIndexUpgradeCodec{Vault: vault, cancel: cancel}
				}
				if err := upgradeWithCodec(ctx, path, codec); err == nil || failure == "cancel-before-commit" && !errors.Is(err, context.Canceled) {
					t.Fatal("incomplete index migration succeeded or lost cancellation", err)
				}
				if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, read *sql.DB) error {
					assertExactPublishedSource(t, read, 37, db.Schema37ManifestHash, before)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				storageSchema.versions[index].sql = original
			}
			result, err = Run(t.Context(), path, vault, Options{Apply: true})
			if err != nil || result.Status != "converted" {
				t.Fatal(result, err)
			}
			upgraded, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			after := gatewayRows(t, upgraded.DB())
			for table, rows := range before {
				if table != "schema_state" && !reflect.DeepEqual(rows, after[table]) {
					t.Fatal("index migration changed rows", table)
				}
			}
			plan := endpointKeyDeletePlan(t, upgraded.DB(), "DELETE FROM charity_models WHERE id=?")
			for _, index := range charityModelDeletionIndexes {
				if !strings.Contains(plan, index) {
					t.Fatal("index missing after upgrade", index, plan)
				}
			}
			if strings.Contains(plan, "SCAN ") {
				t.Fatal("model deletion still scans unrelated rows", plan)
			}
			if err := upgraded.Close(); err != nil {
				t.Fatal(err)
			}
			result, err = Run(t.Context(), path, vault, Options{Apply: true})
			if err != nil || result.Status != "current" {
				t.Fatal("second apply was not current", result, err)
			}
		})
	}
}
