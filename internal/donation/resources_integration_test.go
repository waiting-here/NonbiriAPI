package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type bulkDeletionBaseURLValidator struct{}

func (bulkDeletionBaseURLValidator) ValidateBaseURL(value string) (string, error) {
	return value, nil
}

type bulkDeletionSecretWriter struct{}

func (bulkDeletionSecretWriter) WriteEndpointSecret(
	context.Context,
	*sql.Tx,
	resources.SecretWriteInput,
) (resources.StoredSecret, error) {
	return resources.StoredSecret{}, errors.New("unexpected secret write")
}

func (bulkDeletionSecretWriter) MarkEndpointSecretOrphaned(
	ctx context.Context,
	tx *sql.Tx,
	refID, decisionNow int64,
) error {
	result, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets
SET orphaned_at=? WHERE id=? AND orphaned_at IS NULL`, decisionNow, refID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("secret orphan CAS failed")
	}
	return nil
}

type bulkDeletionDiscoveryRail struct{}

func (bulkDeletionDiscoveryRail) Discover(
	context.Context,
	resources.DiscoveryClaimInput,
) (resources.DiscoveryClaimResult, error) {
	return resources.DiscoveryClaimResult{Succeeded: true}, nil
}

type bulkDeletionDiscoveryWorker struct{}

func (bulkDeletionDiscoveryWorker) ReserveDiscovery() (resources.DiscoveryReservation, bool) {
	return nil, false
}

type bulkDeletionLifecycleHook struct{}

func (bulkDeletionLifecycleHook) ProtectNewEndpointKey(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}

func (bulkDeletionLifecycleHook) ReconcileModelDiscovery(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

func (bulkDeletionLifecycleHook) ReconcileRoutingProjection(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

func (bulkDeletionLifecycleHook) PrepareEndpointDeletion(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}

func (bulkDeletionLifecycleHook) PrepareEndpointKeyDeletion(context.Context, *sql.Tx, int64, []int64, int64) error {
	return nil
}

func (bulkDeletionLifecycleHook) PrepareModelDeletion(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}

func TestResourcesRepositoryDeletesEmptyEndpoint(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "empty-endpoint-owner", nil, false)
	otherOwner := environment.seedUser(t, "other-endpoint-owner", nil, false)
	endpointID, _ := seedBulkDeletionEndpoint(t, environment, owner, 0)
	otherEndpointID, _ := seedBulkDeletionEndpoint(t, environment, otherOwner, 0)
	repository, err := resources.New(resources.Config{
		Store: environment.store, Connectors: connector.NewDefaultRegistry(),
		BaseURLs: bulkDeletionBaseURLValidator{}, Secrets: bulkDeletionSecretWriter{},
		KeyDeletion: environment.service, DiscoveryRail: bulkDeletionDiscoveryRail{},
		KeyCreation: bulkDeletionLifecycleHook{}, Projection: bulkDeletionLifecycleHook{},
		DiscoveryWorker: bulkDeletionDiscoveryWorker{}, CursorKeys: environment.vault,
		FinalAuth: environment.auth,
		Now:       func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	mutation := resources.ControlMutation{
		IdempotencyKey: "LLLLLLLLLLLLLLLLLLLLLL", Method: http.MethodDelete,
		Route: "/api/endpoints/{id}", PathIDs: []string{strconv.FormatInt(endpointID, 10)},
	}
	if _, err := repository.DeleteEndpoint(context.Background(), otherOwner, endpointID, mutation, 1); !errors.Is(err, resources.ErrNotFound) {
		t.Fatalf("foreign endpoint deletion = %v", err)
	}
	for attempt := range 2 {
		deleted, err := repository.DeleteEndpoint(context.Background(), owner, endpointID, mutation, 1)
		if err != nil || deleted.Status != http.StatusNoContent {
			t.Fatalf("DeleteEndpoint attempt %d = %#v, %v", attempt, deleted, err)
		}
	}
	for id, want := range map[int64]int{endpointID: 0, otherEndpointID: 1} {
		var count int
		if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoints WHERE id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("endpoint %d count = %d, want %d", id, count, want)
		}
	}
}

func TestResourcesRepositoryDeletesEndpointWithMoreThanDonationCreateLimit(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "bulk-delete-owner", nil, false)
	environment.seedUser(t, "", nil, true)
	if _, err := environment.store.DB().Exec(`UPDATE site_config SET value='101'
WHERE key='default_endpoint_key_limit'`); err != nil {
		t.Fatal(err)
	}

	endpointID, endpointKeyIDs := seedBulkDeletionEndpoint(t, environment, owner, 101)
	donation := environment.createDonation(t, owner, endpointKeyIDs[0], endpointKeyIDs[1])
	donationID := parseTestID(t, donation.ID)
	donationKeyIDs := []int64{parseTestID(t, donation.Keys[0].ID), parseTestID(t, donation.Keys[1].ID)}
	review := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "bulk endpoint deletion",
		KeySettings: []KeySetting{
			{DonationKeyID: donationKeyIDs[0], Enabled: true},
			{DonationKeyID: donationKeyIDs[1], Enabled: true},
		},
	}
	if _, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, 'K', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"decision": "approve"}),
		donationID, review); err != nil {
		t.Fatal(err)
	}
	charityModelID := seedBulkDeletionCharityBindings(t, environment, donationKeyIDs, endpointKeyIDs[:2])

	repository, err := resources.New(resources.Config{
		Store: environment.store, Connectors: connector.NewDefaultRegistry(),
		BaseURLs: bulkDeletionBaseURLValidator{}, Secrets: bulkDeletionSecretWriter{},
		KeyDeletion: environment.service, DiscoveryRail: bulkDeletionDiscoveryRail{},
		KeyCreation: bulkDeletionLifecycleHook{}, Projection: bulkDeletionLifecycleHook{},
		DiscoveryWorker: bulkDeletionDiscoveryWorker{}, CursorKeys: environment.vault,
		FinalAuth: environment.auth,
		Now:       func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	mutation := resources.ControlMutation{
		IdempotencyKey: "LLLLLLLLLLLLLLLLLLLLLL",
		Method:         http.MethodDelete,
		Route:          "/api/endpoints/{id}",
		PathIDs:        []string{strconv.FormatInt(endpointID, 10)},
	}
	deleted, err := repository.DeleteEndpoint(context.Background(), owner, endpointID, mutation, 1)
	if err != nil || deleted.Status != http.StatusNoContent {
		t.Fatalf("DeleteEndpoint = %#v, %v", deleted, err)
	}

	var endpointCount, endpointKeyCount, memberships, bindings, orphaned int
	for _, check := range []struct {
		query  string
		args   []any
		target *int
	}{
		{`SELECT COUNT(*) FROM endpoints WHERE id=?`, []any{endpointID}, &endpointCount},
		{`SELECT COUNT(*) FROM endpoint_keys WHERE endpoint_id=?`, []any{endpointID}, &endpointKeyCount},
		{`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, []any{donationID}, &memberships},
		{`SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=?`, []any{charityModelID}, &bindings},
		{`SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at=?`, []any{donationTestNow}, &orphaned},
	} {
		if err := environment.store.DB().QueryRow(check.query, check.args...).Scan(check.target); err != nil {
			t.Fatal(err)
		}
	}
	if endpointCount != 0 || endpointKeyCount != 0 || memberships != 0 || bindings != 0 || orphaned != 101 {
		t.Fatalf("post-delete counts endpoint=%d keys=%d memberships=%d bindings=%d orphaned=%d",
			endpointCount, endpointKeyCount, memberships, bindings, orphaned)
	}
	var status string
	var revision, bindingRevision int64
	if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).
		Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT binding_revision FROM charity_models WHERE id=?`, charityModelID).
		Scan(&bindingRevision); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" || revision != 3 || bindingRevision != 8 {
		t.Fatalf("post-delete state status=%q revision=%d binding_revision=%d", status, revision, bindingRevision)
	}
	var ended, detached int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*),
SUM(CASE WHEN endpoint_key_id IS NULL THEN 1 ELSE 0 END) FROM donation_keys
WHERE donation_id=? AND ended_reason='member_removed'`, donationID).Scan(&ended, &detached); err != nil {
		t.Fatal(err)
	}
	if ended != 2 || detached != 2 {
		t.Fatalf("ended/detached donation keys = %d/%d", ended, detached)
	}
}

func seedBulkDeletionEndpoint(
	t *testing.T,
	environment *donationTestEnv,
	owner int64,
	keyCount int,
) (int64, []int64) {
	t.Helper()
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := tx.Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible','https://bulk.example.test/v1','',1,1,?,?)`, owner, donationTestNow, donationTestNow)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	keyIDs := make([]int64, 0, keyCount)
	for index := 1; index <= keyCount; index++ {
		contextID := make([]byte, 16)
		contextID[14], contextID[15] = byte(index>>8), byte(index)
		result, err = tx.Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,'https://bulk.example.test/v1','openai-compatible','fixture-envelope',?)`, contextID, donationTestNow)
		if err != nil {
			t.Fatalf("seed secret %d: %v", index, err)
		}
		secretID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := make([]byte, 32)
		fingerprint[30], fingerprint[31] = byte(index>>8), byte(index)
		result, err = tx.Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,
revision,created_at,updated_at) VALUES(?,?,?,'head','tail','',1,0,1,?,?)`,
			endpointID, secretID, fingerprint, donationTestNow, donationTestNow)
		if err != nil {
			t.Fatalf("seed endpoint key %d: %v", index, err)
		}
		keyID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		keyIDs = append(keyIDs, keyID)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	committed = true
	return endpointID, keyIDs
}

func seedBulkDeletionCharityBindings(
	t *testing.T,
	environment *donationTestEnv,
	donationKeyIDs, endpointKeyIDs []int64,
) int64 {
	t.Helper()
	if len(donationKeyIDs) != 2 || len(endpointKeyIDs) != 2 {
		t.Fatal("bulk deletion fixture requires two donation keys")
	}
	result, err := environment.store.DB().Exec(`INSERT INTO charity_models(
provider,model,full_name,enabled,pricing_mode,binding_revision,created_at,updated_at)
VALUES('fixture','bulk','[公益]fixture/bulk',1,'per_request',7,?,?)`, donationTestNow, donationTestNow)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for index := range donationKeyIDs {
		upstream := fmt.Sprintf("fixture/bulk-%d", index)
		if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,manual_supports,pair_revision,updated_at) VALUES(?,?,1,1,?)`,
			endpointKeyIDs[index], upstream, donationTestNow); err != nil {
			t.Fatal(err)
		}
		if _, err := environment.store.DB().Exec(`INSERT INTO charity_model_bindings(
charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?,?)`, modelID, donationKeyIDs[index], endpointKeyIDs[index], upstream, index,
			donationTestNow, donationTestNow); err != nil {
			t.Fatal(err)
		}
	}
	return modelID
}
