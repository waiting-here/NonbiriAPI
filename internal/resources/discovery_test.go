package resources

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func discoveryMutation(key string, endpointID, keyID int64) ControlMutation {
	return ControlMutation{
		IdempotencyKey: key, Method: "POST", Route: routeDiscovery,
		PathIDs: []string{strconv.FormatInt(endpointID, 10), strconv.FormatInt(keyID, 10)},
	}
}

func waitForDiscoveryOperationState(t *testing.T, environment *resourceTestEnvironment, operationID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var state string
		err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations WHERE id=?`, operationID).Scan(&state)
		if err == nil && state == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s state = %q, %v; want %q", operationID, state, err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForDiscoveryRailCalls(t *testing.T, environment *resourceTestEnvironment, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		environment.discovery.mu.Lock()
		calls := environment.discovery.calls
		environment.discovery.mu.Unlock()
		if calls >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery rail calls = %d, want at least %d", calls, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDiscoveryClaimInputExcludesSecretReference(t *testing.T) {
	if _, exists := reflect.TypeOf(DiscoveryClaimInput{}).FieldByName("SecretRefID"); exists {
		t.Fatal("DiscoveryClaimInput must not expose a secret reference")
	}
}

func TestDiscoveryEvidenceDuplicateAutomaticLateOperationAndReplayAuthorization(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "discovery-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('N'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('O'))
	keyID := resourceTestID(t, key.ID)

	initial, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || initial.Evidence.State != "unknown" || initial.Evidence.Result != nil ||
		initial.Evidence.ObservedAt != nil || initial.Evidence.Count != nil || initial.Evidence.SafeClass != "none" {
		t.Fatalf("initial catalog = %#v, %v", initial, err)
	}

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{
		{UpstreamModelID: "vendor/model", Provider: "vendor-a"},
		{UpstreamModelID: "vendor/model", Provider: "vendor-b"},
	}}
	environment.discovery.mu.Unlock()
	mutation := discoveryMutation(resourceTestKey('P'), endpointID, keyID)
	accepted, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation)
	if err != nil || accepted.Status != 202 || accepted.Value.Evidence.State != "checking" || accepted.Value.OperationID == "" {
		t.Fatalf("RefreshDiscovery = %#v, %v", accepted, err)
	}
	waitForDiscoveryRailCalls(t, environment, 1)
	waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
	environment.discovery.mu.Lock()
	claimInput := environment.discovery.inputs[0]
	environment.discovery.mu.Unlock()
	if claimInput.OperationID != accepted.Value.OperationID || claimInput.OwnerUserID != userID ||
		claimInput.EndpointID != endpointID || claimInput.EndpointKeyID != keyID ||
		claimInput.ConnectorType != "openai-compatible" || claimInput.CanonicalBaseURL != "https://example.com/v1" ||
		claimInput.Discoverer == nil {
		t.Fatalf("discovery claim identity = %#v", claimInput)
	}
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "succeeded" || view.Evidence.Result == nil || *view.Evidence.Result != "nonempty" ||
		view.Evidence.Count == nil || *view.Evidence.Count != "2" || len(view.AutomaticEntries) != 2 {
		t.Fatalf("successful duplicate catalog = %#v, %v", view, err)
	}
	if view.AutomaticEntries[0].UpstreamModelID != "vendor/model" || view.AutomaticEntries[1].UpstreamModelID != "vendor/model" {
		t.Fatalf("duplicate automatic rows were not preserved: %#v", view.AutomaticEntries)
	}
	var supports int64
	if err := environment.store.DB().QueryRow(`
SELECT automatic_supports FROM model_pair_catalog WHERE endpoint_key_id=? AND normalized_model_id='vendor/model'`, keyID).Scan(&supports); err != nil || supports != 2 {
		t.Fatalf("automatic_supports = %d, %v", supports, err)
	}
	var operationState string
	if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations WHERE id=?`, accepted.Value.OperationID).Scan(&operationState); err != nil || operationState != "completed" {
		t.Fatalf("accepted operation state = %q, %v", operationState, err)
	}

	environment.authorizer.deny.Store(true)
	if denied, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation); !errors.Is(err, ErrForbidden) || denied.Replayed {
		t.Fatalf("downgraded discovery replay = %#v, %v", denied, err)
	}
	environment.authorizer.deny.Store(false)
	replay, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation)
	if err != nil || !replay.Replayed || replay.Value.OperationID != accepted.Value.OperationID {
		t.Fatalf("discovery replay = %#v, %v", replay, err)
	}
	environment.discovery.mu.Lock()
	if environment.discovery.calls != 1 {
		t.Fatalf("discovery rail calls after replay = %d, want 1", environment.discovery.calls)
	}
	environment.discovery.mu.Unlock()

	revision, err := strconv.ParseInt(accepted.Value.Evidence.Revision, 10, 64)
	if err != nil {
		t.Fatalf("parse accepted revision: %v", err)
	}
	if err := environment.repository.completeDiscovery(context.Background(), keyID, accepted.Value.OperationID,
		revision, DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "late", Provider: ""}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late operation completion error = %v, want conflict", err)
	}
	viewAfterLate, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || len(viewAfterLate.AutomaticEntries) != 2 || viewAfterLate.AutomaticEntries[0].UpstreamModelID != "vendor/model" {
		t.Fatalf("late completion changed catalog: %#v, %v", viewAfterLate, err)
	}

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{FailureClass: DiscoveryFailureAuth, SafeDiagnostic: "credential rejected"}
	environment.discovery.mu.Unlock()
	failure, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, discoveryMutation(resourceTestKey('Q'), endpointID, keyID))
	if err != nil || failure.Status != 202 {
		t.Fatalf("failed discovery acceptance = %#v, %v", failure, err)
	}
	waitForDiscoveryOperationState(t, environment, failure.Value.OperationID, "completed")
	failedView, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || failedView.Evidence.State != "failed" || failedView.Evidence.SafeClass != "auth" ||
		failedView.Evidence.Result != nil || failedView.Evidence.Count != nil || len(failedView.AutomaticEntries) != 2 {
		t.Fatalf("failed discovery catalog = %#v, %v", failedView, err)
	}

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{}}
	environment.discovery.mu.Unlock()
	empty, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, discoveryMutation(resourceTestKey('R'), endpointID, keyID))
	if err != nil {
		t.Fatalf("empty discovery: %v", err)
	}
	waitForDiscoveryOperationState(t, environment, empty.Value.OperationID, "completed")
	emptyView, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || emptyView.Evidence.State != "succeeded" || emptyView.Evidence.Result == nil || *emptyView.Evidence.Result != "empty" ||
		emptyView.Evidence.Count == nil || *emptyView.Evidence.Count != "0" || len(emptyView.AutomaticEntries) != 0 {
		t.Fatalf("successful empty catalog = %#v, %v", emptyView, err)
	}
}

func TestDiscoveryCheckingConflictAndStaleRecoveryUseSystemContinuation(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "discovery-recovery-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('S'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('T'))
	keyID := resourceTestID(t, key.ID)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "too-late", Provider: ""}}}
	environment.discovery.started = started
	environment.discovery.release = release
	environment.discovery.mu.Unlock()

	mutation := discoveryMutation(resourceTestKey('U'), endpointID, keyID)
	type refreshOutcome struct {
		result MutationResult[DiscoveryAccepted]
		err    error
	}
	finished := make(chan refreshOutcome, 1)
	go func() {
		result, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation)
		finished <- refreshOutcome{result: result, err: err}
	}()
	<-started

	checking, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || checking.Evidence.State != "checking" || checking.Evidence.ObservedAt == nil {
		t.Fatalf("checking evidence = %#v, %v", checking.Evidence, err)
	}
	if _, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID,
		discoveryMutation(resourceTestKey('V'), endpointID, keyID)); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent refresh error = %v, want conflict", err)
	}

	authCallsBeforeRecovery := environment.authorizer.calls.Load()
	environment.clock.Store(resourceTestNow + staleDiscoverySecond + 1)
	recovered, err := environment.repository.RecoverStaleDiscoveries(context.Background())
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverStaleDiscoveries = %d, %v", recovered, err)
	}
	if environment.authorizer.calls.Load() != authCallsBeforeRecovery {
		t.Fatal("stale recovery incorrectly used a user principal")
	}
	close(release)
	outcome := <-finished
	if outcome.err != nil || outcome.result.Status != 202 {
		t.Fatalf("accepted refresh after late completion = %#v, %v", outcome.result, outcome.err)
	}
	failed, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || failed.Evidence.State != "failed" || failed.Evidence.SafeClass != "interrupted" || len(failed.AutomaticEntries) != 0 {
		t.Fatalf("recovered discovery evidence = %#v, %v", failed, err)
	}
	var operationState, checkpoint string
	if err := environment.store.DB().QueryRow(`SELECT state,checkpoint FROM accepted_operations WHERE id=?`, outcome.result.Value.OperationID).Scan(&operationState, &checkpoint); err != nil || operationState != "completed" || checkpoint != "" {
		t.Fatalf("recovered operation = state %q checkpoint %q, %v", operationState, checkpoint, err)
	}

	environment.authorizer.deny.Store(true)
	if replay, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation); !errors.Is(err, ErrForbidden) || replay.Replayed {
		t.Fatalf("replay after downgrade = %#v, %v", replay, err)
	}
}

func TestDiscoveryCompletionAfterKeyDeletionUsesPersistentContinuation(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "discovery-delete-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('W'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('X'))
	keyID := resourceTestID(t, key.ID)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{Succeeded: true, Models: []DiscoveredModel{{UpstreamModelID: "deleted/key", Provider: ""}}}
	environment.discovery.started = started
	environment.discovery.release = release
	environment.discovery.mu.Unlock()

	type refreshOutcome struct {
		result MutationResult[DiscoveryAccepted]
		err    error
	}
	finished := make(chan refreshOutcome, 1)
	go func() {
		result, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID,
			discoveryMutation(resourceTestKey('Y'), endpointID, keyID))
		finished <- refreshOutcome{result: result, err: err}
	}()
	<-started

	remove := resourceTestMutation(t, resourceTestKey('Z'), "DELETE", routeEndpointKey, []int64{endpointID, keyID}, expectedRevisionCanonical{ExpectedRevision: "1"})
	if _, err := environment.repository.DeleteEndpointKey(context.Background(), userID, endpointID, keyID, remove, 1); err != nil {
		t.Fatalf("delete key during discovery: %v", err)
	}
	authCallsAfterDelete := environment.authorizer.calls.Load()
	environment.clock.Store(resourceTestNow + staleDiscoverySecond + 1)
	if recovered, err := environment.repository.RecoverStaleDiscoveries(context.Background()); err != nil || recovered != 1 {
		t.Fatalf("recover deleted-key discovery = %d, %v", recovered, err)
	}
	if environment.authorizer.calls.Load() != authCallsAfterDelete {
		t.Fatal("deleted-key recovery incorrectly re-used a user principal")
	}
	close(release)
	outcome := <-finished
	if outcome.err != nil || outcome.result.Status != 202 {
		t.Fatalf("refresh after key deletion = %#v, %v", outcome.result, outcome.err)
	}
	if environment.authorizer.calls.Load() != authCallsAfterDelete {
		t.Fatal("discovery completion incorrectly re-used a user principal")
	}
	var operationState, checkpoint string
	if err := environment.store.DB().QueryRow(`SELECT state,checkpoint FROM accepted_operations WHERE id=?`, outcome.result.Value.OperationID).Scan(&operationState, &checkpoint); err != nil || operationState != "completed" || checkpoint != "" {
		t.Fatalf("deleted-key operation = state %q checkpoint %q, %v", operationState, checkpoint, err)
	}
}

func TestDiscoveryInvalidTypedOutcomeTerminatesAsProtocolFailure(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "discovery-invalid-outcome-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('X'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('Y'))
	keyID := resourceTestID(t, key.ID)

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{
		Succeeded: true,
		Models: []DiscoveredModel{{
			UpstreamModelID: " invalid-leading-space",
			Provider:        "provider",
		}},
	}
	environment.discovery.mu.Unlock()

	accepted, err := environment.repository.RefreshDiscovery(
		context.Background(), userID, endpointID, keyID,
		discoveryMutation(resourceTestKey('Z'), endpointID, keyID),
	)
	if err != nil {
		t.Fatalf("accept invalid-outcome discovery: %v", err)
	}
	waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "failed" || view.Evidence.SafeClass != "protocol" ||
		len(view.AutomaticEntries) != 0 {
		t.Fatalf("invalid typed outcome evidence = %#v, automatic=%#v, err=%v", view.Evidence, view.AutomaticEntries, err)
	}
}
