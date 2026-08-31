package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func createDeletionTestCandidate(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID, endpointID, keyID int64,
	idempotencyKey, upstreamModelID string,
) {
	t.Helper()
	entries := []ManualCatalogInput{{UpstreamModelID: upstreamModelID, Provider: "test provider"}}
	mutation := resourceTestMutation(t, idempotencyKey, http.MethodPost, routeManualCatalog,
		[]int64{endpointID, keyID}, createManualCanonical{Entries: entries})
	if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, mutation, entries); err != nil {
		t.Fatalf("create deletion-test candidate: %v", err)
	}
}

func addDeletionTestBindings(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID, modelID, expectedRevision int64,
	idempotencyKey string,
	selections []BindingSelection,
) BindingsResponse {
	t.Helper()
	canonicalSelections := make([]bindingSelectionCanonical, len(selections))
	for index, selection := range selections {
		canonicalSelections[index] = bindingSelectionCanonical{
			EndpointKeyID:   strconv.FormatInt(selection.EndpointKeyID, 10),
			UpstreamModelID: selection.UpstreamModelID,
		}
	}
	mutation := resourceTestMutation(t, idempotencyKey, http.MethodPost, routeBindingBatch,
		[]int64{modelID}, addBindingsCanonical{
			ExpectedBindingRevision: strconv.FormatInt(expectedRevision, 10),
			Selections:              canonicalSelections,
		})
	result, err := environment.repository.AddBindings(context.Background(), userID, modelID, mutation, expectedRevision, selections)
	if err != nil {
		t.Fatalf("add deletion-test bindings: %v", err)
	}
	return result.Value
}

func seedDeletionTestMembership(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID, endpointKeyID int64,
	connectorType string,
) (int64, int64) {
	t.Helper()
	result, err := environment.store.DB().Exec(`
INSERT INTO donations(user_id,status,revision,created_at,updated_at)
VALUES(?,'approved',1,?,?)`, userID, resourceTestNow, resourceTestNow)
	if err != nil {
		t.Fatalf("seed deletion-test donation: %v", err)
	}
	donationID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read deletion-test donation id: %v", err)
	}
	zero := make([]byte, 16)
	generation := make([]byte, 16)
	generation[15] = 1
	result, err = environment.store.DB().Exec(`
INSERT INTO donation_keys(
 donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
 failure_streak,streak_generation,next_claim_seq,next_fold_seq,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		donationID, endpointKeyID, "head", "tail", "https://example.com/v1", connectorType,
		zero, zero, zero, zero, zero, zero, zero, generation, zero, zero, resourceTestNow, resourceTestNow)
	if err != nil {
		t.Fatalf("seed deletion-test donation key: %v", err)
	}
	donationKeyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read deletion-test donation key id: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,?)`, endpointKeyID, donationKeyID, donationID, resourceTestNow); err != nil {
		t.Fatalf("seed deletion-test membership: %v", err)
	}
	return donationID, donationKeyID
}

func terminateDeletionTestMemberships(
	ctx context.Context,
	tx *sql.Tx,
	keyIDs []int64,
	now int64,
) error {
	for _, keyID := range keyIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE endpoint_key_id=?`, keyID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM donation_key_memberships WHERE endpoint_key_id=?`, keyID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE donation_keys
SET endpoint_key_id=NULL,enabled=0,ended_reason='member_removed',ended_at=?,updated_at=?
WHERE endpoint_key_id=?`, now, now, keyID); err != nil {
			return err
		}
	}
	return nil
}

func TestRepositoryRequiresEndpointKeyDeletionHook(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	if _, err := New(Config{
		Store:           environment.store,
		Connectors:      environment.repository.connectors,
		BaseURLs:        environment.repository.baseURLs,
		Secrets:         environment.secrets,
		KeyCreation:     resourceTestLifecycleHook{},
		Projection:      resourceTestLifecycleHook{},
		DiscoveryRail:   environment.discovery,
		DiscoveryWorker: environment.worker,
		CursorKeys:      environment.vault,
		FinalAuth:       environment.authorizer,
	}); err == nil {
		t.Fatal("New accepted a nil endpoint-key deletion hook")
	}
}

func TestEndpointKeyDeletionAdvancesBindingRevisionAndInvalidatesOldAction(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "key-deletion-binding-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	targetKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
	targetKeyID := resourceTestID(t, targetKey.ID)
	otherKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('C'))
	otherKeyID := resourceTestID(t, otherKey.ID)
	createDeletionTestCandidate(t, environment, userID, endpointID, targetKeyID, resourceTestKey('D'), "delete/target")
	createDeletionTestCandidate(t, environment, userID, endpointID, otherKeyID, resourceTestKey('E'), "delete/other")

	affected := environment.createModel(t, userID, resourceTestKey('F'), "delete", "affected")
	affectedID := resourceTestID(t, affected.ID)
	addDeletionTestBindings(t, environment, userID, affectedID, 0, resourceTestKey('G'),
		[]BindingSelection{{EndpointKeyID: targetKeyID, UpstreamModelID: "delete/target"}})
	unaffected := environment.createModel(t, userID, resourceTestKey('H'), "delete", "unaffected")
	unaffectedID := resourceTestID(t, unaffected.ID)
	addDeletionTestBindings(t, environment, userID, unaffectedID, 0, resourceTestKey('I'),
		[]BindingSelection{{EndpointKeyID: otherKeyID, UpstreamModelID: "delete/other"}})

	decisionNow := resourceTestNow + 11
	environment.clock.Store(decisionNow)
	mutation := resourceTestMutation(t, resourceTestKey('J'), http.MethodDelete, routeEndpointKey,
		[]int64{endpointID, targetKeyID}, expectedRevisionCanonical{ExpectedRevision: "1"})
	if deleted, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, targetKeyID, mutation, 1); err != nil || deleted.Status != http.StatusNoContent {
		t.Fatalf("DeleteEndpointKey = %#v, %v", deleted, err)
	}
	affectedAfter, err := environment.repository.GetModel(context.Background(), userID, affectedID)
	if err != nil || affectedAfter.BindingRevision != "2" || affectedAfter.BindingCount != "0" || affectedAfter.UpdatedAt != decisionNow {
		t.Fatalf("affected model after key delete = %#v, %v", affectedAfter, err)
	}
	unaffectedAfter, err := environment.repository.GetModel(context.Background(), userID, unaffectedID)
	if err != nil || unaffectedAfter.BindingRevision != "1" || unaffectedAfter.BindingCount != "1" {
		t.Fatalf("unaffected model after key delete = %#v, %v", unaffectedAfter, err)
	}
	stale := resourceTestMutation(t, resourceTestKey('K'), http.MethodPut, routeBindingOrder,
		[]int64{affectedID}, orderBindingsCanonical{ExpectedBindingRevision: "1", Order: []string{}})
	if _, err := environment.repository.OrderBindings(context.Background(), userID, affectedID, stale, 1, []int64{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale binding action error = %v, want conflict", err)
	}
	environment.deletions.mu.Lock()
	calls := append([]resourceTestEndpointKeyDeletionCall(nil), environment.deletions.calls...)
	environment.deletions.mu.Unlock()
	if len(calls) != 1 || calls[0].ownerUserID != userID || calls[0].decisionNow != decisionNow ||
		len(calls[0].keyIDs) != 1 || calls[0].keyIDs[0] != targetKeyID {
		t.Fatalf("key deletion hook calls = %#v", calls)
	}
}

func TestEndpointDeletionAdvancesEachAffectedModelOnce(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "endpoint-deletion-binding-owner")
	targetEndpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	targetEndpointID := resourceTestID(t, targetEndpoint.ID)
	firstKey := environment.createEndpointKey(t, userID, targetEndpointID, resourceTestKey('B'))
	firstKeyID := resourceTestID(t, firstKey.ID)
	secondKey := environment.createEndpointKey(t, userID, targetEndpointID, resourceTestKey('C'))
	secondKeyID := resourceTestID(t, secondKey.ID)
	otherEndpoint := environment.createEndpoint(t, userID, resourceTestKey('D'))
	otherEndpointID := resourceTestID(t, otherEndpoint.ID)
	otherKey := environment.createEndpointKey(t, userID, otherEndpointID, resourceTestKey('E'))
	otherKeyID := resourceTestID(t, otherKey.ID)
	createDeletionTestCandidate(t, environment, userID, targetEndpointID, firstKeyID, resourceTestKey('F'), "endpoint/first")
	createDeletionTestCandidate(t, environment, userID, targetEndpointID, secondKeyID, resourceTestKey('G'), "endpoint/second")
	createDeletionTestCandidate(t, environment, userID, otherEndpointID, otherKeyID, resourceTestKey('H'), "endpoint/other")

	multi := environment.createModel(t, userID, resourceTestKey('I'), "endpoint", "multi")
	multiID := resourceTestID(t, multi.ID)
	addDeletionTestBindings(t, environment, userID, multiID, 0, resourceTestKey('J'), []BindingSelection{
		{EndpointKeyID: firstKeyID, UpstreamModelID: "endpoint/first"},
		{EndpointKeyID: secondKeyID, UpstreamModelID: "endpoint/second"},
	})
	second := environment.createModel(t, userID, resourceTestKey('K'), "endpoint", "second")
	secondID := resourceTestID(t, second.ID)
	addDeletionTestBindings(t, environment, userID, secondID, 0, resourceTestKey('L'),
		[]BindingSelection{{EndpointKeyID: secondKeyID, UpstreamModelID: "endpoint/second"}})
	unaffected := environment.createModel(t, userID, resourceTestKey('M'), "endpoint", "unaffected")
	unaffectedID := resourceTestID(t, unaffected.ID)
	addDeletionTestBindings(t, environment, userID, unaffectedID, 0, resourceTestKey('N'),
		[]BindingSelection{{EndpointKeyID: otherKeyID, UpstreamModelID: "endpoint/other"}})

	decisionNow := resourceTestNow + 12
	environment.clock.Store(decisionNow)
	mutation := resourceTestMutation(t, resourceTestKey('O'), http.MethodDelete, routeEndpoint,
		[]int64{targetEndpointID}, expectedRevisionCanonical{ExpectedRevision: "1"})
	if deleted, err := environment.repository.DeleteEndpoint(context.Background(), userID, targetEndpointID, mutation, 1); err != nil || deleted.Status != http.StatusNoContent {
		t.Fatalf("DeleteEndpoint = %#v, %v", deleted, err)
	}
	for _, expectation := range []struct {
		modelID         int64
		bindingRevision string
		bindingCount    string
		updatedAt       int64
	}{
		{modelID: multiID, bindingRevision: "2", bindingCount: "0", updatedAt: decisionNow},
		{modelID: secondID, bindingRevision: "2", bindingCount: "0", updatedAt: decisionNow},
		{modelID: unaffectedID, bindingRevision: "1", bindingCount: "1", updatedAt: resourceTestNow},
	} {
		model, err := environment.repository.GetModel(context.Background(), userID, expectation.modelID)
		if err != nil || model.BindingRevision != expectation.bindingRevision || model.BindingCount != expectation.bindingCount || model.UpdatedAt != expectation.updatedAt {
			t.Fatalf("model %d after endpoint delete = %#v, %v", expectation.modelID, model, err)
		}
	}
	stale := resourceTestMutation(t, resourceTestKey('P'), http.MethodPut, routeBindingOrder,
		[]int64{multiID}, orderBindingsCanonical{ExpectedBindingRevision: "1", Order: []string{}})
	if _, err := environment.repository.OrderBindings(context.Background(), userID, multiID, stale, 1, []int64{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale endpoint-delete binding action error = %v, want conflict", err)
	}
	environment.deletions.mu.Lock()
	calls := append([]resourceTestEndpointKeyDeletionCall(nil), environment.deletions.calls...)
	environment.deletions.mu.Unlock()
	if len(calls) != 1 || calls[0].ownerUserID != userID || calls[0].decisionNow != decisionNow ||
		len(calls[0].keyIDs) != 2 || calls[0].keyIDs[0] != firstKeyID || calls[0].keyIDs[1] != secondKeyID {
		t.Fatalf("endpoint deletion hook calls = %#v", calls)
	}
}

func TestDeletionHookResolvesMembershipRestrictForKeyAndEndpoint(t *testing.T) {
	for _, target := range []string{"key", "endpoint"} {
		t.Run(target, func(t *testing.T) {
			environment := newResourceTestEnvironment(t)
			userID := environment.seedUser(t, "membership-delete-"+target)
			endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
			endpointID := resourceTestID(t, endpoint.ID)
			key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
			keyID := resourceTestID(t, key.ID)
			_, donationKeyID := seedDeletionTestMembership(t, environment, userID, keyID, "openai-compatible")
			environment.deletions.run = func(ctx context.Context, tx *sql.Tx, _ int64, keyIDs []int64, now int64) error {
				return terminateDeletionTestMemberships(ctx, tx, keyIDs, now)
			}
			environment.clock.Store(resourceTestNow + 20)
			if target == "key" {
				mutation := resourceTestMutation(t, resourceTestKey('C'), http.MethodDelete, routeEndpointKey,
					[]int64{endpointID, keyID}, expectedRevisionCanonical{ExpectedRevision: "1"})
				if _, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, mutation, 1); err != nil {
					t.Fatalf("delete membership key: %v", err)
				}
			} else {
				mutation := resourceTestMutation(t, resourceTestKey('C'), http.MethodDelete, routeEndpoint,
					[]int64{endpointID}, expectedRevisionCanonical{ExpectedRevision: "1"})
				if _, err := environment.repository.DeleteEndpoint(context.Background(), userID, endpointID, mutation, 1); err != nil {
					t.Fatalf("delete membership endpoint: %v", err)
				}
			}
			if environment.rowCount(t, `SELECT count(*) FROM donation_key_memberships WHERE endpoint_key_id=?`, keyID) != 0 {
				t.Fatal("donation membership survived successful owner deletion")
			}
			var endpointRef any
			var enabled int
			var endedReason string
			var endedAt int64
			if err := environment.store.DB().QueryRow(`
SELECT endpoint_key_id,enabled,ended_reason,ended_at FROM donation_keys WHERE id=?`, donationKeyID).
				Scan(&endpointRef, &enabled, &endedReason, &endedAt); err != nil || endpointRef != nil || enabled != 0 || endedReason != "member_removed" || endedAt != resourceTestNow+20 {
				t.Fatalf("terminated donation key = ref %#v enabled %d reason %q at %d, %v", endpointRef, enabled, endedReason, endedAt, err)
			}
		})
	}
}

func TestDeletionHookAndPhysicalDeleteFailuresRollbackRevisionAndIdempotency(t *testing.T) {
	for _, failure := range []string{"hook", "delete"} {
		t.Run(failure, func(t *testing.T) {
			environment := newResourceTestEnvironment(t)
			userID := environment.seedUser(t, "delete-rollback-"+failure)
			endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
			endpointID := resourceTestID(t, endpoint.ID)
			key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
			keyID := resourceTestID(t, key.ID)
			createDeletionTestCandidate(t, environment, userID, endpointID, keyID, resourceTestKey('C'), "rollback/model")
			model := environment.createModel(t, userID, resourceTestKey('D'), "rollback", failure)
			modelID := resourceTestID(t, model.ID)
			addDeletionTestBindings(t, environment, userID, modelID, 0, resourceTestKey('E'),
				[]BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "rollback/model"}})
			seedDeletionTestMembership(t, environment, userID, keyID, "openai-compatible")
			baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
			sentinel := errors.New("deletion hook refused")
			if failure == "hook" {
				environment.deletions.run = func(ctx context.Context, tx *sql.Tx, _ int64, keyIDs []int64, now int64) error {
					if err := terminateDeletionTestMemberships(ctx, tx, keyIDs, now); err != nil {
						return err
					}
					return sentinel
				}
			}
			mutation := resourceTestMutation(t, resourceTestKey('F'), http.MethodDelete, routeEndpointKey,
				[]int64{endpointID, keyID}, expectedRevisionCanonical{ExpectedRevision: "1"})
			_, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, mutation, 1)
			if failure == "hook" {
				if !errors.Is(err, sentinel) {
					t.Fatalf("hook failure error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "delete endpoint key") {
				t.Fatalf("physical delete failure error = %v", err)
			}
			afterFailure, readErr := environment.repository.GetModel(context.Background(), userID, modelID)
			if readErr != nil || afterFailure.BindingRevision != "1" || afterFailure.BindingCount != "1" {
				t.Fatalf("model after %s rollback = %#v, %v", failure, afterFailure, readErr)
			}
			if _, readErr := environment.repository.GetEndpointKey(context.Background(), userID, endpointID, keyID); readErr != nil {
				t.Fatalf("key missing after %s rollback: %v", failure, readErr)
			}
			if environment.rowCount(t, `SELECT count(*) FROM donation_key_memberships WHERE endpoint_key_id=?`, keyID) != 1 ||
				environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
				t.Fatalf("%s rollback did not restore membership/idempotency", failure)
			}

			environment.deletions.mu.Lock()
			environment.deletions.run = func(ctx context.Context, tx *sql.Tx, _ int64, keyIDs []int64, now int64) error {
				return terminateDeletionTestMemberships(ctx, tx, keyIDs, now)
			}
			environment.deletions.mu.Unlock()
			if _, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, mutation, 1); err != nil {
				t.Fatalf("retry after %s rollback: %v", failure, err)
			}
		})
	}
}

func TestDeletionBindingRevisionMaximumConflictsBeforeHook(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "delete-max-revision-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)
	createDeletionTestCandidate(t, environment, userID, endpointID, keyID, resourceTestKey('C'), "max/model")
	model := environment.createModel(t, userID, resourceTestKey('D'), "max", "model")
	modelID := resourceTestID(t, model.ID)
	addDeletionTestBindings(t, environment, userID, modelID, 0, resourceTestKey('E'),
		[]BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "max/model"}})
	if _, err := environment.store.DB().Exec(`UPDATE models SET binding_revision=9223372036854775807 WHERE id=?`, modelID); err != nil {
		t.Fatalf("seed maximum binding revision: %v", err)
	}
	baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	mutation := resourceTestMutation(t, resourceTestKey('F'), http.MethodDelete, routeEndpointKey,
		[]int64{endpointID, keyID}, expectedRevisionCanonical{ExpectedRevision: "1"})
	if _, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, mutation, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("maximum binding revision delete error = %v, want conflict", err)
	}
	environment.deletions.mu.Lock()
	hookCalls := len(environment.deletions.calls)
	environment.deletions.mu.Unlock()
	if hookCalls != 0 || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys WHERE id=?`, keyID) != 1 ||
		environment.rowCount(t, `SELECT count(*) FROM model_bindings WHERE model_id=?`, modelID) != 1 ||
		environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
		t.Fatalf("maximum revision conflict side effects: hooks=%d", hookCalls)
	}
}

func TestForceStoreFalseConnectorRestrictionsRepositoryAndHTTP(t *testing.T) {
	t.Run("repository", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "force-store-repository-owner")
		anthropic := environment.createEndpointWithConnector(t, userID, resourceTestKey('A'), "anthropic-compatible", true)
		anthropicID := resourceTestID(t, anthropic.ID)
		baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		trueCreate := resourceTestMutation(t, resourceTestKey('B'), http.MethodPost, routeEndpointKeys,
			[]int64{anthropicID}, createEndpointKeyCanonical{
				Secret: "anthropic-secret", Note: "", Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
			})
		if _, err := environment.repository.CreateEndpointKey(context.Background(), userID, anthropicID, trueCreate, CreateEndpointKeyInput{
			Secret: []byte("anthropic-secret"), Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
		}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Anthropic force-store create error = %v, want invalid_request", err)
		}
		environment.secrets.mu.Lock()
		writes := environment.secrets.writes
		environment.secrets.mu.Unlock()
		if writes != 0 || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys WHERE endpoint_id=?`, anthropicID) != 0 ||
			environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
			t.Fatal("Anthropic force-store create had side effects")
		}

		falseKey := environment.createEndpointKey(t, userID, anthropicID, resourceTestKey('C'))
		falseKeyID := resourceTestID(t, falseKey.ID)
		baselineIdempotency = environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		truePatch := resourceTestMutation(t, resourceTestKey('D'), http.MethodPatch, routeEndpointKey,
			[]int64{anthropicID, falseKeyID}, patchEndpointKeyCanonical{ForceStoreFalse: pointer(true), ExpectedRevision: "1"})
		if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, anthropicID, falseKeyID, truePatch,
			PatchEndpointKeyInput{ForceStoreFalse: pointer(true), ExpectedRevision: 1}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Anthropic force-store patch error = %v, want invalid_request", err)
		}
		unchanged, err := environment.repository.GetEndpointKey(context.Background(), userID, anthropicID, falseKeyID)
		if err != nil || unchanged.ForceStoreFalse || unchanged.Revision != "1" ||
			environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
			t.Fatalf("Anthropic force-store patch changed state = %#v, %v", unchanged, err)
		}
		falsePatch := resourceTestMutation(t, resourceTestKey('E'), http.MethodPatch, routeEndpointKey,
			[]int64{anthropicID, falseKeyID}, patchEndpointKeyCanonical{ForceStoreFalse: pointer(false), ExpectedRevision: "1"})
		if result, err := environment.repository.PatchEndpointKey(context.Background(), userID, anthropicID, falseKeyID, falsePatch,
			PatchEndpointKeyInput{ForceStoreFalse: pointer(false), ExpectedRevision: 1}); err != nil || result.Value.ForceStoreFalse || result.Value.Revision != "2" {
			t.Fatalf("Anthropic explicit false patch = %#v, %v", result, err)
		}

		openAI := environment.createEndpoint(t, userID, resourceTestKey('F'))
		openAIID := resourceTestID(t, openAI.ID)
		openAITrue := resourceTestMutation(t, resourceTestKey('G'), http.MethodPost, routeEndpointKeys,
			[]int64{openAIID}, createEndpointKeyCanonical{
				Secret: "openai-secret", Note: "", Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
			})
		if result, err := environment.repository.CreateEndpointKey(context.Background(), userID, openAIID, openAITrue, CreateEndpointKeyInput{
			Secret: []byte("openai-secret"), Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
		}); err != nil || !result.Value.ForceStoreFalse {
			t.Fatalf("OpenAI force-store create = %#v, %v", result, err)
		}
	})

	t.Run("http", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "force-store-http-owner")
		endpoint := environment.createEndpointWithConnector(t, userID, resourceTestKey('A'), "anthropic-compatible", true)
		registrar := &resourceTestRegistrar{}
		if err := RegisterRoutes(registrar, environment.repository); err != nil {
			t.Fatalf("RegisterRoutes: %v", err)
		}
		principal := UserPrincipal{UserID: userID}
		createHandler := registrar.handlers[http.MethodPost+" "+routeEndpointKeys]
		pathValues := map[string]string{"id": endpoint.ID}
		trueBody := `{"secret":"anthropic-http","note":"","enabled":true,"force_store_false":true,"ownership_confirmed":true}`
		baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		response := resourceHTTPCall(t, createHandler, principal, http.MethodPost,
			"/api/endpoints/"+endpoint.ID+"/keys", trueBody, resourceTestKey('B'), pathValues)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("Anthropic force-store HTTP create = %d %s", response.Code, response.Body.String())
		}
		environment.secrets.mu.Lock()
		writes := environment.secrets.writes
		environment.secrets.mu.Unlock()
		if writes != 0 || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys WHERE endpoint_id=?`, resourceTestID(t, endpoint.ID)) != 0 ||
			environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
			t.Fatal("Anthropic force-store HTTP create had side effects")
		}
		falseBody := `{"secret":"anthropic-http","note":"","enabled":true,"force_store_false":false,"ownership_confirmed":true}`
		response = resourceHTTPCall(t, createHandler, principal, http.MethodPost,
			"/api/endpoints/"+endpoint.ID+"/keys", falseBody, resourceTestKey('C'), pathValues)
		if response.Code != http.StatusCreated {
			t.Fatalf("Anthropic explicit false HTTP create = %d %s", response.Code, response.Body.String())
		}
		var key EndpointKey
		if err := json.Unmarshal(response.Body.Bytes(), &key); err != nil || key.ForceStoreFalse {
			t.Fatalf("decode Anthropic key = %#v, %v", key, err)
		}
		patchHandler := registrar.handlers[http.MethodPatch+" "+routeEndpointKey]
		patchValues := map[string]string{"id": endpoint.ID, "keyId": key.ID}
		baselineIdempotency = environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		response = resourceHTTPCall(t, patchHandler, principal, http.MethodPatch,
			"/api/endpoints/"+endpoint.ID+"/keys/"+key.ID,
			`{"force_store_false":true,"expected_revision":"1"}`, resourceTestKey('D'), patchValues)
		if response.Code != http.StatusBadRequest || environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency {
			t.Fatalf("Anthropic force-store HTTP patch = %d %s", response.Code, response.Body.String())
		}
		response = resourceHTTPCall(t, patchHandler, principal, http.MethodPatch,
			"/api/endpoints/"+endpoint.ID+"/keys/"+key.ID,
			`{"force_store_false":false,"expected_revision":"1"}`, resourceTestKey('E'), patchValues)
		if response.Code != http.StatusOK {
			t.Fatalf("Anthropic explicit false HTTP patch = %d %s", response.Code, response.Body.String())
		}

		openAI := environment.createEndpoint(t, userID, resourceTestKey('F'))
		openAIValues := map[string]string{"id": openAI.ID}
		response = resourceHTTPCall(t, createHandler, principal, http.MethodPost,
			"/api/endpoints/"+openAI.ID+"/keys",
			`{"secret":"openai-http","note":"","enabled":true,"force_store_false":true,"ownership_confirmed":true}`,
			resourceTestKey('G'), openAIValues)
		if response.Code != http.StatusCreated {
			t.Fatalf("OpenAI force-store HTTP create = %d %s", response.Code, response.Body.String())
		}
		var openAIKey EndpointKey
		if err := json.Unmarshal(response.Body.Bytes(), &openAIKey); err != nil || !openAIKey.ForceStoreFalse {
			t.Fatalf("decode OpenAI key = %#v, %v", openAIKey, err)
		}
	})
}

func TestRoutingRejectsAnthropicForceStoreFalsePersistedState(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "force-store-routing-owner")
	anthropic := environment.createEndpointWithConnector(t, userID, resourceTestKey('A'), "anthropic-compatible", true)
	anthropicID := resourceTestID(t, anthropic.ID)
	anthropicKey := environment.createEndpointKey(t, userID, anthropicID, resourceTestKey('B'))
	anthropicKeyID := resourceTestID(t, anthropicKey.ID)
	createDeletionTestCandidate(t, environment, userID, anthropicID, anthropicKeyID, resourceTestKey('C'), "anthropic/model")
	anthropicModel := environment.createModel(t, userID, resourceTestKey('D'), "routing", "anthropic")
	anthropicModelID := resourceTestID(t, anthropicModel.ID)
	addDeletionTestBindings(t, environment, userID, anthropicModelID, 0, resourceTestKey('E'),
		[]BindingSelection{{EndpointKeyID: anthropicKeyID, UpstreamModelID: "anthropic/model"}})
	if _, err := environment.store.DB().Exec(`UPDATE endpoint_keys SET force_store_false=1 WHERE id=?`, anthropicKeyID); err != nil {
		t.Fatalf("seed hostile Anthropic force-store state: %v", err)
	}
	router, err := routing.New(environment.store)
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	if snapshot, err := router.Snapshot(context.Background(), userID, routing.Identity{ModelID: anthropicModel.ID}); err == nil || snapshot.CandidateCount() != 0 {
		t.Fatalf("hostile Anthropic snapshot = %#v, %v", snapshot, err)
	}

	openAI := environment.createEndpoint(t, userID, resourceTestKey('F'))
	openAIID := resourceTestID(t, openAI.ID)
	openAIKeyMutation := resourceTestMutation(t, resourceTestKey('G'), http.MethodPost, routeEndpointKeys,
		[]int64{openAIID}, createEndpointKeyCanonical{
			Secret: "openai-routing", Note: "", Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
		})
	openAIKey, err := environment.repository.CreateEndpointKey(context.Background(), userID, openAIID, openAIKeyMutation, CreateEndpointKeyInput{
		Secret: []byte("openai-routing"), Enabled: true, ForceStoreFalse: true, OwnershipConfirmed: true,
	})
	if err != nil {
		t.Fatalf("create OpenAI routing key: %v", err)
	}
	openAIKeyID := resourceTestID(t, openAIKey.Value.ID)
	createDeletionTestCandidate(t, environment, userID, openAIID, openAIKeyID, resourceTestKey('H'), "openai/model")
	openAIModel := environment.createModel(t, userID, resourceTestKey('I'), "routing", "openai")
	openAIModelID := resourceTestID(t, openAIModel.ID)
	addDeletionTestBindings(t, environment, userID, openAIModelID, 0, resourceTestKey('J'),
		[]BindingSelection{{EndpointKeyID: openAIKeyID, UpstreamModelID: "openai/model"}})
	snapshot, err := router.Snapshot(context.Background(), userID, routing.Identity{ModelID: openAIModel.ID})
	if err != nil || snapshot.CandidateCount() != 1 || !snapshot.Candidates()[0].ForceStoreFalse() {
		t.Fatalf("OpenAI force-store snapshot = %#v, %v", snapshot, err)
	}
}
