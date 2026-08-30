package resources

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestEndpointOwnershipIdentityCASAndSuspension(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	ownerID := environment.seedUser(t, "endpoint-owner")
	otherID := environment.seedUser(t, "endpoint-other")
	endpoint := environment.createEndpoint(t, ownerID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	otherEndpoint := environment.createEndpoint(t, ownerID, resourceTestKey('B'))
	otherEndpointID := resourceTestID(t, otherEndpoint.ID)

	if _, err := environment.repository.GetEndpoint(context.Background(), otherID, endpointID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetEndpoint error = %v, want not_found", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE endpoints SET base_url='https://other.example/v1' WHERE id=?`, endpointID); err == nil {
		t.Fatal("endpoint identity update unexpectedly succeeded")
	}

	patchInput := PatchEndpointInput{Note: pointer("updated note"), ExpectedRevision: 1}
	patchMutation := resourceTestMutation(t, resourceTestKey('C'), "PATCH", routeEndpoint, []int64{endpointID}, patchEndpointCanonical{
		Note: patchInput.Note, ExpectedRevision: "1",
	})
	patched, err := environment.repository.PatchEndpoint(context.Background(), ownerID, endpointID, patchMutation, patchInput)
	if err != nil || patched.Value.Revision != "2" || patched.Value.Note != "updated note" {
		t.Fatalf("PatchEndpoint = %#v, %v", patched, err)
	}
	replay, err := environment.repository.PatchEndpoint(context.Background(), ownerID, endpointID, patchMutation, patchInput)
	if err != nil || !replay.Replayed || string(replay.Body) != string(patched.Body) {
		t.Fatalf("PatchEndpoint replay = %#v, %v", replay, err)
	}
	environment.authorizer.deny.Store(true)
	if denied, err := environment.repository.PatchEndpoint(context.Background(), ownerID, endpointID, patchMutation, patchInput); !errors.Is(err, ErrForbidden) || denied.Replayed {
		t.Fatalf("downgraded PatchEndpoint replay = %#v, %v", denied, err)
	}
	environment.authorizer.deny.Store(false)
	badPath := patchMutation
	badPath.IdempotencyKey = resourceTestKey('D')
	badPath.PathIDs = []string{otherEndpoint.ID}
	if _, err := environment.repository.PatchEndpoint(context.Background(), ownerID, endpointID, badPath, PatchEndpointInput{Note: pointer("bad"), ExpectedRevision: 2}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mismatched digest path error = %v, want invalid_request", err)
	}

	key := environment.createEndpointKey(t, ownerID, endpointID, resourceTestKey('E'))
	keyID := resourceTestID(t, key.ID)
	if _, err := environment.repository.GetEndpointKey(context.Background(), ownerID, otherEndpointID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-parent GetEndpointKey error = %v, want not_found", err)
	}
	if _, err := environment.repository.GetEndpointKey(context.Background(), otherID, endpointID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetEndpointKey error = %v, want not_found", err)
	}

	reportCaseID := "rpc_" + strings.Repeat("A", 22)
	if _, err := environment.store.DB().Exec(`
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,
 deadline,cursor_text,material_count,target_count,distinct_owner_count,created_at
) VALUES(?,?,? ,?,'pending_review','complete',1,1,?,NULL,0,0,0,?)`,
		reportCaseID, make([]byte, 32), "openai-compatible", endpoint.BaseURL,
		resourceTestNow+3600, resourceTestNow); err != nil {
		t.Fatalf("seed report case: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at)
VALUES(?,'report_case',?,?)`, keyID, reportCaseID, resourceTestNow); err != nil {
		t.Fatalf("seed endpoint key suspension: %v", err)
	}

	lockedEndpointMutation := resourceTestMutation(t, resourceTestKey('F'), "PATCH", routeEndpoint, []int64{endpointID}, patchEndpointCanonical{
		Enabled: pointer(false), ExpectedRevision: "2",
	})
	if _, err := environment.repository.PatchEndpoint(context.Background(), ownerID, endpointID, lockedEndpointMutation, PatchEndpointInput{Enabled: pointer(false), ExpectedRevision: 2}); !errors.Is(err, ErrResourceLocked) {
		t.Fatalf("suspended parent patch error = %v, want resource_locked", err)
	}
	lockedKeyMutation := resourceTestMutation(t, resourceTestKey('G'), "PATCH", routeEndpointKey, []int64{endpointID, keyID}, patchEndpointKeyCanonical{
		Enabled: pointer(false), ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchEndpointKey(context.Background(), ownerID, endpointID, keyID, lockedKeyMutation, PatchEndpointKeyInput{Enabled: pointer(false), ExpectedRevision: 1}); !errors.Is(err, ErrResourceLocked) {
		t.Fatalf("suspended key patch error = %v, want resource_locked", err)
	}
	lockedChildCreate := resourceTestMutation(t, resourceTestKey('H'), "POST", routeEndpointKeys, []int64{endpointID}, createEndpointKeyCanonical{
		Secret: "another-credential", Note: "", Enabled: true, OwnershipConfirmed: true,
	})
	if _, err := environment.repository.CreateEndpointKey(context.Background(), ownerID, endpointID, lockedChildCreate, CreateEndpointKeyInput{
		Secret: []byte("another-credential"), Enabled: true, OwnershipConfirmed: true,
	}); !errors.Is(err, ErrResourceLocked) {
		t.Fatalf("suspended parent child-create error = %v, want resource_locked", err)
	}
}

func TestEndpointKeyDisableDeleteCASAndSecretOrphan(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "key-cas-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('I'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('J'))
	keyID := resourceTestID(t, key.ID)

	patch := resourceTestMutation(t, resourceTestKey('K'), "PATCH", routeEndpointKey, []int64{endpointID, keyID}, patchEndpointKeyCanonical{
		Enabled: pointer(false), ExpectedRevision: "1",
	})
	updated, err := environment.repository.PatchEndpointKey(context.Background(), userID, endpointID, keyID, patch, PatchEndpointKeyInput{Enabled: pointer(false), ExpectedRevision: 1})
	if err != nil || updated.Value.Enabled || updated.Value.Revision != "2" {
		t.Fatalf("disable key = %#v, %v", updated, err)
	}
	stale := resourceTestMutation(t, resourceTestKey('L'), "PATCH", routeEndpointKey, []int64{endpointID, keyID}, patchEndpointKeyCanonical{
		Note: pointer("stale"), ExpectedRevision: "1",
	})
	if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, endpointID, keyID, stale, PatchEndpointKeyInput{Note: pointer("stale"), ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale key patch error = %v, want conflict", err)
	}
	remove := resourceTestMutation(t, resourceTestKey('M'), "DELETE", routeEndpointKey, []int64{endpointID, keyID}, expectedRevisionCanonical{ExpectedRevision: "2"})
	deleted, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, remove, 2)
	if err != nil || deleted.Status != 204 || len(deleted.Body) != 0 {
		t.Fatalf("DeleteEndpointKey = %#v, %v", deleted, err)
	}
	if _, err := environment.repository.GetEndpointKey(context.Background(), userID, endpointID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key read error = %v, want not_found", err)
	}
	environment.secrets.mu.Lock()
	orphans := environment.secrets.orphans
	environment.secrets.mu.Unlock()
	if orphans != 1 {
		t.Fatalf("orphan calls = %d, want 1", orphans)
	}
}

func TestEndpointSecretPlaintextRepositoryBoundariesAndExactHandoff(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "endpoint-secret-validator-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('a'))
	endpointID := resourceTestID(t, endpoint.ID)

	create := func(seed byte, secretValue []byte) (MutationResult[EndpointKey], error) {
		mutation := resourceTestMutation(t, resourceTestKey(seed), http.MethodPost, routeEndpointKeys, []int64{endpointID}, createEndpointKeyCanonical{
			Secret: string(secretValue), Note: "", Enabled: true, OwnershipConfirmed: true,
		})
		return environment.repository.CreateEndpointKey(context.Background(), userID, endpointID, mutation, CreateEndpointKeyInput{
			Secret: secretValue, Enabled: true, OwnershipConfirmed: true,
		})
	}

	invalidSecrets := []struct {
		name  string
		value []byte
	}{
		{name: "empty", value: nil},
		{name: "over maximum", value: bytes.Repeat([]byte{'A'}, maxEndpointSecretBytes+1)},
		{name: "C0 control", value: []byte("prefix\x00suffix")},
		{name: "unicode control", value: []byte("prefix\u0085suffix")},
		{name: "DEL", value: []byte("prefix\x7fsuffix")},
		{name: "invalid UTF-8", value: []byte{0xf0, 0x28, 0x8c, 0x28}},
	}
	baselineReplayRows := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	baselineAuthCalls := environment.authorizer.calls.Load()
	for index, testCase := range invalidSecrets {
		if _, err := create(byte('b'+index), testCase.value); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s error = %v, want invalid_request", testCase.name, err)
		}
	}
	environment.secrets.mu.Lock()
	writesAfterRejects := environment.secrets.writes
	environment.secrets.mu.Unlock()
	if writesAfterRejects != 0 || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`) != 0 ||
		environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineReplayRows ||
		environment.authorizer.calls.Load() != baselineAuthCalls {
		t.Fatalf("invalid plaintext side effects: writes=%d keys=%d replay=%d auth=%d", writesAfterRejects,
			environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`),
			environment.rowCount(t, `SELECT count(*) FROM idempotency_records`), environment.authorizer.calls.Load())
	}

	validSecrets := []struct {
		name  string
		seed  byte
		value []byte
	}{
		{name: "one byte", seed: 'h', value: []byte{'A'}},
		{name: "maximum bytes", seed: 'i', value: bytes.Repeat([]byte{'Z'}, maxEndpointSecretBytes)},
		{name: "printable Unicode exact bytes", seed: 'j', value: []byte("  密钥 e\u0301 🔑  ")},
	}
	for _, testCase := range validSecrets {
		result, err := create(testCase.seed, testCase.value)
		if err != nil || result.Status != http.StatusCreated {
			t.Fatalf("%s create = %#v, %v", testCase.name, result, err)
		}
		environment.secrets.mu.Lock()
		observed := append([]byte(nil), environment.secrets.lastPlaintext...)
		environment.secrets.mu.Unlock()
		if !bytes.Equal(observed, testCase.value) {
			clear(observed)
			t.Fatalf("%s SecretWriter bytes changed", testCase.name)
		}
		clear(observed)
	}
	environment.secrets.mu.Lock()
	validWrites := environment.secrets.writes
	clear(environment.secrets.lastPlaintext)
	environment.secrets.lastPlaintext = nil
	environment.secrets.mu.Unlock()
	if validWrites != len(validSecrets) || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`) != len(validSecrets) {
		t.Fatalf("valid plaintext writes=%d keys=%d, want %d", validWrites,
			environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`), len(validSecrets))
	}
}

func TestEndpointSecretEscapedControlsHTTPFailClosed(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "endpoint-secret-http-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('k'))
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	handler := registrar.handlers[http.MethodPost+" "+routeEndpointKeys]
	principal := UserPrincipal{UserID: userID}
	for index, escapedControl := range []string{`\u001f`, `\u0085`, `\u007f`} {
		body := `{"secret":"prefix` + escapedControl + `suffix","note":"","enabled":true,"force_store_false":false,"ownership_confirmed":true}`
		response := resourceHTTPCall(t, handler, principal, http.MethodPost,
			"/api/endpoints/"+endpoint.ID+"/keys", body, resourceTestKey(byte('l'+index)), map[string]string{"id": endpoint.ID})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("escaped control %q status=%d body=%s", escapedControl, response.Code, response.Body.String())
		}
	}
	environment.secrets.mu.Lock()
	writes := environment.secrets.writes
	environment.secrets.mu.Unlock()
	if writes != 0 || environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`) != 0 {
		t.Fatalf("escaped controls wrote secrets=%d keys=%d", writes,
			environment.rowCount(t, `SELECT count(*) FROM endpoint_keys`))
	}
}

func TestCallerKeyFinalAuthorizationCASAndOneTimePlaintext(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "caller-key-owner")
	state, err := environment.repository.GetCallerKey(context.Background(), userID)
	if err != nil || state.Generation != "0" || state.Metadata != nil {
		t.Fatalf("initial caller key = %#v, %v", state, err)
	}

	first, err := environment.repository.RegenerateCallerKey(context.Background(), userID, 0)
	if err != nil || !strings.HasPrefix(first.Secret, "nbk_") || len(first.Secret) != 47 || first.Metadata.Generation != "1" {
		t.Fatalf("first regenerate = %#v, %v", first, err)
	}
	if _, err := environment.repository.RegenerateCallerKey(context.Background(), userID, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale regenerate error = %v, want conflict", err)
	}
	identity, err := environment.repository.ResolveCallerKey(context.Background(), first.Secret)
	if err != nil || identity.UserID != userID || identity.Generation != 1 {
		t.Fatalf("ResolveCallerKey = %#v, %v", identity, err)
	}

	environment.authorizer.deny.Store(true)
	if result, err := environment.repository.RegenerateCallerKey(context.Background(), userID, 1); !errors.Is(err, ErrForbidden) || result.Secret != "" {
		t.Fatalf("downgraded regenerate = %#v, %v", result, err)
	}
	environment.authorizer.deny.Store(false)
	state, err = environment.repository.GetCallerKey(context.Background(), userID)
	if err != nil || state.Generation != "1" || state.Metadata == nil {
		t.Fatalf("caller key after denied mutation = %#v, %v", state, err)
	}

	const racers = 8
	type outcome struct {
		secret CallerKeySecret
		err    error
	}
	outcomes := make(chan outcome, racers)
	var wait sync.WaitGroup
	for range racers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			secret, err := environment.repository.RegenerateCallerKey(context.Background(), userID, 1)
			outcomes <- outcome{secret: secret, err: err}
		}()
	}
	wait.Wait()
	close(outcomes)
	winners := 0
	for outcome := range outcomes {
		if outcome.err == nil {
			winners++
			if outcome.secret.Secret == "" {
				t.Fatal("CAS winner did not receive plaintext")
			}
			continue
		}
		if !errors.Is(outcome.err, ErrConflict) || outcome.secret.Secret != "" {
			t.Fatalf("CAS loser = %#v, %v", outcome.secret, outcome.err)
		}
	}
	if winners != 1 {
		t.Fatalf("caller-key CAS winners = %d, want 1", winners)
	}
	state, err = environment.repository.GetCallerKey(context.Background(), userID)
	if err != nil || state.Generation != "2" {
		t.Fatalf("post-race caller key = %#v, %v", state, err)
	}
	if _, err := environment.repository.ResolveCallerKey(context.Background(), first.Secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old caller key resolve error = %v, want not_found", err)
	}

	environment.authorizer.deny.Store(true)
	if generation, err := environment.repository.RevokeCallerKey(context.Background(), userID, 2); !errors.Is(err, ErrForbidden) || generation != "" {
		t.Fatalf("downgraded RevokeCallerKey = %q, %v", generation, err)
	}
	environment.authorizer.deny.Store(false)
	state, err = environment.repository.GetCallerKey(context.Background(), userID)
	if err != nil || state.Generation != "2" || state.Metadata == nil {
		t.Fatalf("caller key changed after denied revoke = %#v, %v", state, err)
	}

	if generation, err := environment.repository.RevokeCallerKey(context.Background(), userID, 2); err != nil || generation != "3" {
		t.Fatalf("RevokeCallerKey = %q, %v", generation, err)
	}
	state, err = environment.repository.GetCallerKey(context.Background(), userID)
	if err != nil || state.Generation != "3" || state.Metadata != nil {
		t.Fatalf("revoked caller key = %#v, %v", state, err)
	}
	var replayRows int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM idempotency_records`).Scan(&replayRows); err != nil || replayRows != 0 {
		t.Fatalf("caller-key replay rows = %d, %v", replayRows, err)
	}
}

func TestFlattenAnthropicRestorationGuardsAndCAS(t *testing.T) {
	type boundResources struct {
		endpoint   Endpoint
		endpointID int64
		key        EndpointKey
		keyID      int64
		model      Model
		modelID    int64
	}
	setup := func(t *testing.T, environment *resourceTestEnvironment, userID int64, connectorType string, flatten bool) boundResources {
		t.Helper()
		endpoint := environment.createEndpointWithConnector(t, userID, resourceTestKey('A'), connectorType, true)
		endpointID := resourceTestID(t, endpoint.ID)
		key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
		keyID := resourceTestID(t, key.ID)
		upstreamModelID := connectorType + "/model"
		entries := []ManualCatalogInput{{UpstreamModelID: upstreamModelID, Provider: ""}}
		catalog := resourceTestMutation(t, resourceTestKey('C'), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: entries})
		if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, catalog, entries); err != nil {
			t.Fatalf("create restoration catalog: %v", err)
		}
		modelInput := CreateModelInput{
			Provider: "restoration", Model: connectorType, RouteStrategy: "ordered", FlattenToolCalls: flatten,
		}
		modelMutation := resourceTestMutation(t, resourceTestKey('D'), "POST", routeModels, nil, createModelCanonical{
			Provider: modelInput.Provider, Model: modelInput.Model, RouteStrategy: pointer("ordered"), FlattenToolCalls: &flatten,
		})
		createdModel, err := environment.repository.CreateModel(context.Background(), userID, modelMutation, modelInput)
		if err != nil {
			t.Fatalf("create restoration model: %v", err)
		}
		model := createdModel.Value
		modelID := resourceTestID(t, model.ID)
		selection := BindingSelection{EndpointKeyID: keyID, UpstreamModelID: upstreamModelID}
		binding := resourceTestMutation(t, resourceTestKey('E'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
			ExpectedBindingRevision: "0", Selections: []bindingSelectionCanonical{{EndpointKeyID: key.ID, UpstreamModelID: upstreamModelID}},
		})
		if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, binding, 0, []BindingSelection{selection}); err != nil {
			t.Fatalf("create restoration binding: %v", err)
		}
		return boundResources{endpoint: endpoint, endpointID: endpointID, key: key, keyID: keyID, model: model, modelID: modelID}
	}

	t.Run("OpenAI restoration remains allowed", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "flatten-openai-restoration-owner")
		resources := setup(t, environment, userID, "openai-compatible", true)

		disableEndpoint := resourceTestMutation(t, resourceTestKey('F'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Enabled: pointer(false), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, disableEndpoint, PatchEndpointInput{
			Enabled: pointer(false), ExpectedRevision: 1,
		}); err != nil {
			t.Fatalf("disable OpenAI endpoint: %v", err)
		}
		enableEndpoint := resourceTestMutation(t, resourceTestKey('G'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Enabled: pointer(true), ExpectedRevision: "2",
		})
		restoredEndpoint, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, enableEndpoint, PatchEndpointInput{
			Enabled: pointer(true), ExpectedRevision: 2,
		})
		if err != nil || !restoredEndpoint.Value.Enabled || restoredEndpoint.Value.Revision != "3" {
			t.Fatalf("restore OpenAI endpoint = %#v, %v", restoredEndpoint, err)
		}

		disableKey := resourceTestMutation(t, resourceTestKey('H'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Enabled: pointer(false), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, disableKey, PatchEndpointKeyInput{
			Enabled: pointer(false), ExpectedRevision: 1,
		}); err != nil {
			t.Fatalf("disable OpenAI endpoint key: %v", err)
		}
		enableKey := resourceTestMutation(t, resourceTestKey('I'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Enabled: pointer(true), ExpectedRevision: "2",
		})
		restoredKey, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, enableKey, PatchEndpointKeyInput{
			Enabled: pointer(true), ExpectedRevision: 2,
		})
		if err != nil || !restoredKey.Value.Enabled || restoredKey.Value.Revision != "3" {
			t.Fatalf("restore OpenAI endpoint key = %#v, %v", restoredKey, err)
		}
	})

	t.Run("Anthropic endpoint restoration conflicts atomically", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "flatten-anthropic-endpoint-owner")
		resources := setup(t, environment, userID, "anthropic-compatible", false)

		disable := resourceTestMutation(t, resourceTestKey('F'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Enabled: pointer(false), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, disable, PatchEndpointInput{
			Enabled: pointer(false), ExpectedRevision: 1,
		}); err != nil {
			t.Fatalf("disable Anthropic endpoint: %v", err)
		}
		// Build the exact pre-write state whose restoration guard is under test.
		if _, err := environment.store.DB().Exec(`UPDATE models SET flatten_tool_calls=1 WHERE id=?`, resources.modelID); err != nil {
			t.Fatalf("seed flattened restoration state: %v", err)
		}

		replayRows := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		stale := resourceTestMutation(t, resourceTestKey('G'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Enabled: pointer(true), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, stale, PatchEndpointInput{
			Enabled: pointer(true), ExpectedRevision: 1,
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale endpoint enable error = %v, want conflict", err)
		}
		note := "must-not-commit"
		guarded := resourceTestMutation(t, resourceTestKey('H'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Note: &note, Enabled: pointer(true), ExpectedRevision: "2",
		})
		if _, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, guarded, PatchEndpointInput{
			Note: &note, Enabled: pointer(true), ExpectedRevision: 2,
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("Anthropic endpoint restoration error = %v, want conflict", err)
		}
		current, err := environment.repository.GetEndpoint(context.Background(), userID, resources.endpointID)
		if err != nil || current.Enabled || current.Note != resources.endpoint.Note || current.Revision != "2" {
			t.Fatalf("endpoint restoration conflict changed row = %#v, %v", current, err)
		}
		if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
			t.Fatalf("endpoint restoration conflict idempotency rows = %d, want %d", got, replayRows)
		}

		if _, err := environment.store.DB().Exec(`UPDATE models SET flatten_tool_calls=0 WHERE id=?`, resources.modelID); err != nil {
			t.Fatalf("clear flattened restoration fixture: %v", err)
		}
		allowed := resourceTestMutation(t, resourceTestKey('I'), "PATCH", routeEndpoint, []int64{resources.endpointID}, patchEndpointCanonical{
			Enabled: pointer(true), ExpectedRevision: "2",
		})
		restored, err := environment.repository.PatchEndpoint(context.Background(), userID, resources.endpointID, allowed, PatchEndpointInput{
			Enabled: pointer(true), ExpectedRevision: 2,
		})
		if err != nil || !restored.Value.Enabled || restored.Value.Revision != "3" {
			t.Fatalf("restore non-flatten Anthropic endpoint = %#v, %v", restored, err)
		}
	})

	t.Run("Anthropic endpoint key restoration conflicts atomically", func(t *testing.T) {
		environment := newResourceTestEnvironment(t)
		userID := environment.seedUser(t, "flatten-anthropic-key-owner")
		resources := setup(t, environment, userID, "anthropic-compatible", false)

		disable := resourceTestMutation(t, resourceTestKey('F'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Enabled: pointer(false), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, disable, PatchEndpointKeyInput{
			Enabled: pointer(false), ExpectedRevision: 1,
		}); err != nil {
			t.Fatalf("disable Anthropic endpoint key: %v", err)
		}
		// Build the exact pre-write state whose restoration guard is under test.
		if _, err := environment.store.DB().Exec(`UPDATE models SET flatten_tool_calls=1 WHERE id=?`, resources.modelID); err != nil {
			t.Fatalf("seed flattened key restoration state: %v", err)
		}

		replayRows := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
		stale := resourceTestMutation(t, resourceTestKey('G'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Enabled: pointer(true), ExpectedRevision: "1",
		})
		if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, stale, PatchEndpointKeyInput{
			Enabled: pointer(true), ExpectedRevision: 1,
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale endpoint key enable error = %v, want conflict", err)
		}
		note := "must-not-commit"
		guarded := resourceTestMutation(t, resourceTestKey('H'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Note: &note, Enabled: pointer(true), ExpectedRevision: "2",
		})
		if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, guarded, PatchEndpointKeyInput{
			Note: &note, Enabled: pointer(true), ExpectedRevision: 2,
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("Anthropic endpoint key restoration error = %v, want conflict", err)
		}
		current, err := environment.repository.GetEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID)
		if err != nil || current.Enabled || current.Note != resources.key.Note || current.Revision != "2" {
			t.Fatalf("endpoint key restoration conflict changed row = %#v, %v", current, err)
		}
		if got := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`); got != replayRows {
			t.Fatalf("endpoint key restoration conflict idempotency rows = %d, want %d", got, replayRows)
		}

		if _, err := environment.store.DB().Exec(`UPDATE models SET flatten_tool_calls=0 WHERE id=?`, resources.modelID); err != nil {
			t.Fatalf("clear flattened key restoration fixture: %v", err)
		}
		allowed := resourceTestMutation(t, resourceTestKey('I'), "PATCH", routeEndpointKey, []int64{resources.endpointID, resources.keyID}, patchEndpointKeyCanonical{
			Enabled: pointer(true), ExpectedRevision: "2",
		})
		restored, err := environment.repository.PatchEndpointKey(context.Background(), userID, resources.endpointID, resources.keyID, allowed, PatchEndpointKeyInput{
			Enabled: pointer(true), ExpectedRevision: 2,
		})
		if err != nil || !restored.Value.Enabled || restored.Value.Revision != "3" {
			t.Fatalf("restore non-flatten Anthropic endpoint key = %#v, %v", restored, err)
		}
	})
}

func TestConfiguredResourceLimitsAreAtomic(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "resource-limit-owner")
	if _, err := environment.store.DB().Exec(`
UPDATE site_config SET value='1',updated_at=?
WHERE key IN ('default_endpoint_limit','default_endpoint_key_limit','default_model_limit','default_binding_limit')`, resourceTestNow); err != nil {
		t.Fatalf("configure resource limits: %v", err)
	}

	endpoint := environment.createEndpoint(t, userID, resourceTestKey('1'))
	endpointID := resourceTestID(t, endpoint.ID)
	secondEndpoint := resourceTestMutation(t, resourceTestKey('2'), "POST", routeEndpoints, nil, createEndpointCanonical{
		ConnectorType: "openai-compatible", BaseURL: "https://other.example/v1", Note: "", Enabled: true,
	})
	if _, err := environment.repository.CreateEndpoint(context.Background(), userID, secondEndpoint, CreateEndpointInput{
		ConnectorType: "openai-compatible", BaseURL: "https://other.example/v1", Enabled: true,
	}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("endpoint cap error = %v, want resource limit", err)
	}

	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('3'))
	keyID := resourceTestID(t, key.ID)
	secondKey := resourceTestMutation(t, resourceTestKey('4'), "POST", routeEndpointKeys, []int64{endpointID}, createEndpointKeyCanonical{
		Secret: "second-secret", Note: "", Enabled: true, OwnershipConfirmed: true,
	})
	if _, err := environment.repository.CreateEndpointKey(context.Background(), userID, endpointID, secondKey, CreateEndpointKeyInput{
		Secret: []byte("second-secret"), Enabled: true, OwnershipConfirmed: true,
	}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("endpoint-key cap error = %v, want resource limit", err)
	}

	entries := []ManualCatalogInput{{UpstreamModelID: "limit/A", Provider: ""}, {UpstreamModelID: "limit/B", Provider: ""}}
	manual := resourceTestMutation(t, resourceTestKey('5'), "POST", routeManualCatalog, []int64{endpointID, keyID}, createManualCanonical{Entries: entries})
	if _, err := environment.repository.CreateManualEntries(context.Background(), userID, endpointID, keyID, manual, entries); err != nil {
		t.Fatalf("create limit catalog: %v", err)
	}
	model := environment.createModel(t, userID, resourceTestKey('6'), "limit", "model")
	modelID := resourceTestID(t, model.ID)
	secondModel := resourceTestMutation(t, resourceTestKey('7'), "POST", routeModels, nil, createModelCanonical{
		Provider: "limit", Model: "second", RouteStrategy: pointer("ordered"),
	})
	if _, err := environment.repository.CreateModel(context.Background(), userID, secondModel, CreateModelInput{
		Provider: "limit", Model: "second", RouteStrategy: "ordered",
	}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("model cap error = %v, want resource limit", err)
	}

	firstSelection := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "limit/A"}}
	firstBinding := resourceTestMutation(t, resourceTestKey('8'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "0", Selections: []bindingSelectionCanonical{{EndpointKeyID: key.ID, UpstreamModelID: "limit/A"}},
	})
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, firstBinding, 0, firstSelection); err != nil {
		t.Fatalf("create first binding: %v", err)
	}
	secondSelection := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "limit/B"}}
	secondBinding := resourceTestMutation(t, resourceTestKey('9'), "POST", routeBindingBatch, []int64{modelID}, addBindingsCanonical{
		ExpectedBindingRevision: "1", Selections: []bindingSelectionCanonical{{EndpointKeyID: key.ID, UpstreamModelID: "limit/B"}},
	})
	if _, err := environment.repository.AddBindings(context.Background(), userID, modelID, secondBinding, 1, secondSelection); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("binding cap error = %v, want resource limit", err)
	}
	authoritative, err := environment.repository.ListBindings(context.Background(), userID, modelID)
	if err != nil || authoritative.BindingRevision != "1" || len(authoritative.Bindings) != 1 {
		t.Fatalf("binding cap was not atomic: %#v, %v", authoritative, err)
	}
}

func TestEndpointLimitExplicitOverrideAndDefaultFallback(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	if _, err := environment.store.DB().Exec(`
UPDATE site_config SET value='2',updated_at=?
WHERE key='default_endpoint_limit'`, resourceTestNow); err != nil {
		t.Fatalf("configure default endpoint limit: %v", err)
	}

	create := func(t *testing.T, userID int64, seed byte) error {
		t.Helper()
		input := CreateEndpointInput{
			ConnectorType: "openai-compatible", BaseURL: "https://example.com/v1",
			Note: "endpoint limit test", Enabled: true,
		}
		mutation := resourceTestMutation(t, resourceTestKey(seed), "POST", routeEndpoints, nil, createEndpointCanonical{
			ConnectorType: input.ConnectorType, BaseURL: input.BaseURL, Note: input.Note, Enabled: input.Enabled,
		})
		_, err := environment.repository.CreateEndpoint(context.Background(), userID, mutation, input)
		return err
	}
	assertCount := func(t *testing.T, userID int64, want int) {
		t.Helper()
		var got int
		if err := environment.store.DB().QueryRow(`SELECT count(*) FROM endpoints WHERE user_id=?`, userID).Scan(&got); err != nil {
			t.Fatalf("count endpoints: %v", err)
		}
		if got != want {
			t.Fatalf("endpoint count = %d, want %d", got, want)
		}
	}

	t.Run("explicit override above default", func(t *testing.T) {
		userID := environment.seedUser(t, "endpoint-limit-high-override")
		if _, err := environment.store.DB().Exec(`UPDATE users SET endpoint_limit=3 WHERE id=?`, userID); err != nil {
			t.Fatalf("configure high endpoint override: %v", err)
		}
		for _, seed := range []byte{'A', 'B', 'C'} {
			if err := create(t, userID, seed); err != nil {
				t.Fatalf("create endpoint through high override: %v", err)
			}
		}
		if err := create(t, userID, 'D'); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("endpoint above high override error = %v, want resource limit", err)
		}
		assertCount(t, userID, 3)
	})

	t.Run("explicit override below default", func(t *testing.T) {
		userID := environment.seedUser(t, "endpoint-limit-low-override")
		if _, err := environment.store.DB().Exec(`UPDATE users SET endpoint_limit=1 WHERE id=?`, userID); err != nil {
			t.Fatalf("configure low endpoint override: %v", err)
		}
		if err := create(t, userID, 'E'); err != nil {
			t.Fatalf("create endpoint within low override: %v", err)
		}
		if err := create(t, userID, 'F'); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("endpoint at low override error = %v, want resource limit", err)
		}
		assertCount(t, userID, 1)
	})

	t.Run("null override falls back to default", func(t *testing.T) {
		userID := environment.seedUser(t, "endpoint-limit-default-fallback")
		for _, seed := range []byte{'G', 'H'} {
			if err := create(t, userID, seed); err != nil {
				t.Fatalf("create endpoint through default limit: %v", err)
			}
		}
		if err := create(t, userID, 'I'); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("endpoint at default limit error = %v, want resource limit", err)
		}
		assertCount(t, userID, 2)
	})
}
