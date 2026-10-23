package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func appFixtureTx[T any](t *testing.T, store *db.Store, seed func(*sql.Tx) (T, error)) T {
	t.Helper()
	tx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := seed(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

func appFixtureEndpoint(t *testing.T, store *db.Store, in testseed.EndpointInput) int64 {
	t.Helper()
	return appFixtureTx(t, store, func(tx *sql.Tx) (int64, error) {
		if in.ConnectionID == 0 {
			result, err := tx.Exec(`INSERT INTO service_connections(user_id,name,created_at,updated_at) VALUES(?,'Fixture connection',?,?)`, in.OwnerUserID, in.At, in.At)
			if err != nil {
				return 0, err
			}
			in.ConnectionID, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
		}
		return testseed.InsertEndpointTx(context.Background(), tx, in)
	})
}

func appFixtureKey(t *testing.T, store *db.Store, in testseed.KeyInput, vaults ...*secret.Vault) testseed.KeyIdentity {
	t.Helper()
	if in.Fingerprint == nil {
		digest := sha256.Sum256([]byte(fmt.Sprintf("fixture-key/%d/%d", in.EndpointID, in.SecretRefID)))
		in.Fingerprint = digest[:]
	}
	if in.DisplayHead == "" {
		in.DisplayHead = "head"
	}
	if in.DisplayTail == "" {
		in.DisplayTail = "tail"
	}
	var vault *secret.Vault
	if in.SecretRefID != 0 {
		if len(vaults) > 0 {
			vault = vaults[0]
		} else {
			// Session fixtures use the same fixed synthetic master key as their sealed bodies.
			var err error
			vault, err = secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
			if err != nil {
				t.Fatal(err)
			}
			defer vault.Close()
		}
	}
	return appFixtureTx(t, store, func(tx *sql.Tx) (testseed.KeyIdentity, error) {
		if in.SecretRefID != 0 {
			review, err := secret.NewDonationReview(vault)
			if err != nil {
				return testseed.KeyIdentity{}, err
			}
			if err := review.BackfillEndpointSecrets(context.Background(), tx, []int64{in.SecretRefID}, time.Unix(in.At, 0)); err != nil {
				return testseed.KeyIdentity{}, err
			}
		}
		out, err := testseed.InsertEndpointKeyTx(context.Background(), tx, in)
		if err == nil && in.SecretRefID != 0 {
			_, err = tx.Exec(`UPDATE credentials SET connection_id=(SELECT connection_id FROM endpoints WHERE id=?) WHERE id=?`, in.EndpointID, out.CredentialID)
		}
		return out, err
	})
}

func appFixtureBinding(t *testing.T, store *db.Store, in testseed.BindingInput, charity bool) testseed.BindingIdentity {
	t.Helper()
	return appFixtureTx(t, store, func(tx *sql.Tx) (testseed.BindingIdentity, error) {
		if charity {
			return testseed.InsertCharityBindingTx(context.Background(), tx, in)
		}
		return testseed.InsertModelBindingTx(context.Background(), tx, in)
	})
}

func appFixtureApprovedUses(t *testing.T, store *db.Store, keyID, at int64) {
	t.Helper()
	appFixtureTx(t, store, func(tx *sql.Tx) ([]int64, error) {
		return testseed.SeedDonationUsesTx(context.Background(), tx, keyID, true, at)
	})
}

func appFixtureManualModel(t *testing.T, store *db.Store, keyID int64, model string, at int64) {
	t.Helper()
	if _, err := store.DB().Exec(`INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,evidence_source,created_at,updated_at) VALUES(?,'manual',?,?,1,0,'unknown',?,?)`, keyID, model, model, at, at); err != nil {
		t.Fatal(err)
	}
}

func appFixtureSource(t *testing.T, store *db.Store, in testseed.DiscoverySourceInput) testseed.DiscoveryCatalogIdentity {
	t.Helper()
	return appFixtureTx(t, store, func(tx *sql.Tx) (testseed.DiscoveryCatalogIdentity, error) {
		out, err := testseed.SeedDiscoverySourceTx(context.Background(), tx, in)
		if err == nil {
			_, err = tx.Exec(`UPDATE discovery_sources SET connection_id=(SELECT e.connection_id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?) WHERE id=? AND connection_id IS NULL`, in.EndpointKeyID, out.SourceID)
		}
		return out, err
	})
}

func appFixtureCatalog(t *testing.T, store *db.Store, in testseed.DiscoveryCatalogInput) testseed.DiscoveryCatalogIdentity {
	t.Helper()
	return appFixtureTx(t, store, func(tx *sql.Tx) (testseed.DiscoveryCatalogIdentity, error) {
		out, err := testseed.SeedDiscoveryCatalogTx(context.Background(), tx, in)
		if err == nil {
			_, err = tx.Exec(`UPDATE discovery_sources SET connection_id=(SELECT e.connection_id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?) WHERE id=? AND connection_id IS NULL`, in.EndpointKeyID, out.SourceID)
		}
		return out, err
	})
}

func appFixtureMainstreamChannel(t *testing.T, store *db.Store, id, name, base string, kind connectorcontract.Type, at int64) int64 {
	t.Helper()
	services, err := connectorcontract.LegacyNativeServices(kind)
	if err != nil {
		t.Fatal(err)
	}
	return appFixtureTx(t, store, func(tx *sql.Tx) (int64, error) {
		if _, err := tx.Exec(`INSERT INTO mainstream_channels(id,name,category,enabled,state,revision,created_at,updated_at) VALUES(?,?,'subscription',1,'active',1,?,?)`, id, name, at, at); err != nil {
			return 0, err
		}
		result, err := tx.Exec(`INSERT INTO mainstream_channel_entries(channel_id,ord,connector_type,canonical_base_url,native_service_mask) VALUES(?,0,?,?,?)`, id, string(kind), base, services.Mask())
		if err != nil {
			return 0, err
		}
		format := "openai"
		switch kind {
		case connectorcontract.TypeAnthropicCompatible:
			format = "anthropic"
		case connectorcontract.TypeGoogleGemini:
			format = "gemini"
		case connectorcontract.TypeAISDKGatewayV3, connectorcontract.TypeAISDKGatewayV4:
			format = string(kind)
		}
		if _, err := tx.Exec(`INSERT INTO mainstream_channel_discovery_presets(channel_id,canonical_base_url,list_format) VALUES(?,?,?)`, id, base, format); err != nil {
			return 0, err
		}
		return result.LastInsertId()
	})
}

func mustAppFixtureID(t *testing.T, value string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("invalid fixture ID %q: %v", value, err)
	}
	return id
}

// These published-scope catalogs are initial fixture state, not the result of a
// refresh under test. Sharing is explicit in the subsequent real donation POST.
func appFixtureSharedCatalog(t *testing.T, store *db.Store, key string, models []string, at int64) map[string]any {
	t.Helper()
	services, err := connectorcontract.LegacyNativeServices(connectorcontract.TypeOpenAICompatible)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]testseed.DiscoveryModel, len(models))
	for i, model := range models {
		entries[i] = testseed.DiscoveryModel{ID: model, NativeServices: services, EvidenceSource: "legacy"}
	}
	source := appFixtureCatalog(t, store, testseed.DiscoveryCatalogInput{EndpointKeyID: mustAppFixtureID(t, key), ListFormat: "openai", Models: entries, At: at})
	var revision int64
	if err := store.DB().QueryRow(`SELECT r.source_revision FROM discovery_sources s JOIN discovery_source_revisions r ON r.id=s.current_revision_id WHERE s.id=?`, source.SourceID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"endpoint_key_id": key, "expires_at": nil, "native_services": services.Services(), "discovery_source_id": fmt.Sprint(source.SourceID), "discovery_source_revision": fmt.Sprint(revision)}
}

func appFixtureServiceHealth(t *testing.T, store *db.Store, key int64, service connectorcontract.NativeService, at int64) int64 {
	t.Helper()
	selected, err := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{service})
	if err != nil {
		t.Fatal(err)
	}
	one, zero := db.EncodeU128(db.U128{15: 1}), db.EncodeU128(db.U128{})
	return appFixtureTx(t, store, func(tx *sql.Tx) (int64, error) {
		_, err := tx.Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,k.endpoint_key_id,u.native_service_bit,g.credential_version_id,?,?,?,?,1,0,0,? FROM donation_keys k JOIN donation_uses u ON u.donation_key_id=k.id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE k.endpoint_key_id=? AND u.native_service_bit=?
ON CONFLICT(use_id,endpoint_key_id,native_service_bit,credential_version_id) DO NOTHING`, one, zero, one, one, at, key, selected.Mask())
		if err != nil {
			return 0, err
		}
		var id int64
		err = tx.QueryRow(`SELECT h.id FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE h.endpoint_key_id=? AND h.native_service_bit=? AND h.credential_version_id=g.credential_version_id`, key, selected.Mask()).Scan(&id)
		return id, err
	})
}

func appFixtureServiceBackoff(t *testing.T, store *db.Store, health, now int64) smartbackoff.Projection {
	t.Helper()
	var period, source, trial sql.NullString
	var retry sql.NullInt64
	if err := store.DB().QueryRow(`SELECT backoff_period,backoff_source,backoff_retry_at,trial_claim_id FROM donation_service_health WHERE id=?`, health).Scan(&period, &source, &retry, &trial); err != nil {
		t.Fatal(err)
	}
	var at *int64
	if retry.Valid {
		at = &retry.Int64
	}
	return smartbackoff.DecodeProjection(period.String, source.String, at, trial.Valid, now)
}

func appFixtureDonationManualModel(t *testing.T, store *db.Store, key int64, model string, at int64) {
	t.Helper()
	appFixtureTx(t, store, func(tx *sql.Tx) (int64, error) {
		result, err := tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?) ON CONFLICT(donation_key_id,normalized_model_id) DO NOTHING`, key, model, model, at, at)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 0 {
			_, err = tx.Exec(`UPDATE donation_keys SET manual_catalog_revision=manual_catalog_revision+1 WHERE id=?`, key)
		}
		return count, err
	})
}
