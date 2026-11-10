package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema51LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	_, view, _ := strings.Cut(db.ServiceKeyScopeSQL, "DROP VIEW charity_model_catalog;")
	hostileMustExec(t, database, "DROP VIEW charity_model_catalog;"+view)
	assertRetainedManifest(t, database, db.Schema51ManifestHash)
}

func TestCompleteDiscoveryURLUpgradePreservesPinnedHistoryAndRollsBack(t *testing.T) {
	path, vault := bootstrapTestPath(t, "catalog-address.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	key := seedEndpointKeyHistory(t, store.DB(), vault, 'U')
	var owner int64
	if err := store.DB().QueryRow("SELECT user_id FROM endpoints WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)", key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	donation := hostileInsertDonation(t, store.DB(), owner)
	member := hostileInsertDonationKey(t, store.DB(), donation, key)
	hostileMustExec(t, store.DB(), "INSERT INTO donation_key_memberships(donation_key_id,endpoint_key_id,donation_id,created_at) VALUES(?,?,?,0)", member, key, donation)
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: key, BaseURL: "https://catalog.example/a%2Fb/v1/", ListFormat: "openai", Models: []testseed.DiscoveryModel{{ID: "retained-model"}}, At: 0})
	if err != nil {
		t.Fatal(err)
	}
	uses, err := testseed.SeedDonationUsesWithDiscoveryTx(t.Context(), tx, member, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Keep the approved revision pinned while a new self revision is current.
	hostileMustExec(t, store.DB(), `INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
 SELECT source_id,owner_user_id,2,credential_id,credential_version_id,'https://catalog.example/other/models/','anthropic',1 FROM discovery_source_revisions WHERE id=?`, source.SourceRevisionID)
	var current int64
	if err := store.DB().QueryRow("SELECT max(id) FROM discovery_source_revisions").Scan(&current); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, store.DB(), "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(?,1,1)", current)
	hostileMustExec(t, store.DB(), "UPDATE discovery_sources SET current_revision_id=?,revision=revision+1,updated_at=1 WHERE id=?", current, source.SourceID)
	long := "https://catalog.example/" + strings.Repeat("x", 4089-len("https://catalog.example/"))
	cases := []struct{ format, before, after string }{
		{"openai", "https://catalog.example/v1/models", "https://catalog.example/v1/models"},
		{"anthropic", "https://catalog.example/v1/", "https://catalog.example/v1/models"},
		{"gemini", "https://catalog.example/a%2Fb/v1beta", "https://catalog.example/a%2Fb/v1beta/models"},
		{"ai-sdk-gateway-v3", "https://catalog.example/gateway", "https://catalog.example/gateway/config"},
		{"ai-sdk-gateway-v4", "https://catalog.example/gateway/config/", "https://catalog.example/gateway/config/"},
		{"openai", long, long + "/models"},
	}
	for i, tc := range cases {
		hostileMustExec(t, store.DB(), `INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
 SELECT source_id,owner_user_id,?,credential_id,credential_version_id,?,?,1 FROM discovery_source_revisions WHERE id=?`, i+3, tc.before, tc.format, source.SourceRevisionID)
		channel := hostileOIDVariant("mch_", byte('a'+i), 'Q')
		hostileMustExec(t, store.DB(), "INSERT INTO mainstream_channels(id,name,category,enabled,state,revision,created_at,updated_at) VALUES(?,'catalog','api_platform',1,'active',1,0,0)", channel)
		hostileMustExec(t, store.DB(), "INSERT INTO mainstream_channel_discovery_presets(channel_id,canonical_base_url,list_format) VALUES(?,?,?)", channel, tc.before, tc.format)
	}
	restoreSchema51LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=51")
	before := gatewayRows(t, store.DB(), true)
	var pinned string
	if err := store.DB().QueryRow("SELECT canonical_base_url FROM discovery_source_revisions WHERE id=?", source.SourceRevisionID).Scan(&pinned); err != nil || pinned != "https://catalog.example/a%2Fb/v1/" {
		t.Fatal("fixture did not retain the legacy pinned address", pinned, err)
	}
	store.Close()
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("legacy catalog semantics opened at runtime")
	}
	if checked, err := Run(t.Context(), path, vault, Options{}); err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 51 {
		t.Fatal(checked, err)
	}
	index := migrationIndex(t, 51)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		if err := original(ctx, tx); err != nil {
			return err
		}
		return errors.New("injected after catalog conversion")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("failed catalog conversion committed")
	}
	storageSchema.versions[index].apply = original
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema51ManifestHash)
		if !reflect.DeepEqual(before, gatewayRows(t, database, true)) {
			t.Fatal("rollback changed retained rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 51 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after := gatewayRows(t, store.DB(), true)
	for _, table := range []string{"schema_state", "discovery_source_revisions", "mainstream_channel_discovery_presets"} {
		delete(before, table)
		delete(after, table)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("conversion changed unrelated rows, authorization or history")
	}
	for i, tc := range cases {
		var got, preset string
		if err := store.DB().QueryRow("SELECT canonical_base_url FROM discovery_source_revisions WHERE source_id=? AND source_revision=?", source.SourceID, i+3).Scan(&got); err != nil || got != tc.after {
			t.Fatal("source conversion", tc.format, got, err)
		}
		if err := store.DB().QueryRow("SELECT canonical_base_url FROM mainstream_channel_discovery_presets WHERE channel_id=?", hostileOIDVariant("mch_", byte('a'+i), 'Q')).Scan(&preset); err != nil || preset != tc.after {
			t.Fatal("preset conversion", tc.format, preset, err)
		}
	}
	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	authorization, err := connectionstore.LoadDiscoveryAuthorizationTx(t.Context(), tx, connectionstore.SourceAuthorizationRef{OwnerUserID: owner, SourceID: source.SourceID, Purpose: connectionstore.SourceCharity, DonationUseID: uses[0]})
	if err != nil || authorization.SourceRevisionID != source.SourceRevisionID || authorization.CanonicalBaseURL != "https://catalog.example/a%2Fb/v1/models" {
		t.Fatal("approved pin lost", authorization, err)
	}
	if _, err := tx.Exec("UPDATE discovery_source_revisions SET canonical_base_url='https://changed.example/models' WHERE id=?", source.SourceRevisionID); err == nil {
		t.Fatal("source revision guard was not restored")
	}
	tx.Rollback()
	store.Close()
	if again, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil || again.Status != "current" {
		t.Fatal("current database re-converted", again, err)
	}
}

func TestCompleteDiscoveryURLUpgradeRejectsOverflowWithoutMutation(t *testing.T) {
	path, vault := bootstrapTestPath(t, "catalog-overflow.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	key := seedEndpointKeyHistory(t, store.DB(), vault, 'V')
	address := "https://catalog.example/" + strings.Repeat("x", 4096-len("https://catalog.example/"))
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := testseed.SeedDiscoverySourceTx(t.Context(), tx, testseed.DiscoverySourceInput{EndpointKeyID: key, BaseURL: address, ListFormat: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	restoreSchema51LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=51")
	before := gatewayRows(t, store.DB(), true)
	store.Close()
	for _, apply := range []bool{false, true} {
		_, err := Run(t.Context(), path, vault, Options{Apply: apply})
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("source %d revision 1", source.SourceID)) || !strings.Contains(err.Error(), "4096") || strings.Contains(err.Error(), address) {
			t.Fatal("overflow must identify the source without exposing its address", err)
		}
	}
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
		assertRetainedManifest(t, database, db.Schema51ManifestHash)
		if !reflect.DeepEqual(before, gatewayRows(t, database, true)) {
			t.Fatal("overflow mutated the source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
