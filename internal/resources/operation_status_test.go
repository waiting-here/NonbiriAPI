package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func assertOperationStatus(t *testing.T, env *resourceTestEnvironment, userID int64, key, status, stage string, result *ResourceOperationResult) {
	t.Helper()
	got, err := env.repository.GetResourceOperationStatus(context.Background(), userID, key)
	want := ResourceOperationStatus{Status: status, Stage: stage, Result: result}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("operation status = (%+v,%v), want %+v", got, err, want)
	}
}

func TestResourceOperationStatusRealWritersReplayAndFixedWindow(t *testing.T) {
	env := newResourceTestEnvironment(t)
	user := env.seedUser(t, "status-writers")
	endpoint := env.createEndpoint(t, user, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	assertOperationStatus(t, env, user, resourceTestKey('A'), "recorded", "endpoint", &ResourceOperationResult{EndpointID: endpoint.ID})
	key := env.createEndpointKey(t, user, endpointID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)
	assertOperationStatus(t, env, user, resourceTestKey('B'), "recorded", "key", &ResourceOperationResult{EndpointID: endpoint.ID, EndpointKeyID: key.ID})
	entries := []ManualCatalogInput{{UpstreamModelID: "model-one"}, {UpstreamModelID: "model-two"}}
	manualMutation := resourceTestMutation(t, resourceTestKey('C'), http.MethodPost, routeManualCatalog, []int64{endpointID, keyID}, entries)
	manual, err := env.repository.CreateManualEntries(context.Background(), user, endpointID, keyID, manualMutation, entries)
	if err != nil {
		t.Fatal(err)
	}
	assertOperationStatus(t, env, user, resourceTestKey('C'), "recorded", "catalog_manual", &ResourceOperationResult{EndpointID: endpoint.ID, EndpointKeyID: key.ID, CatalogEntryIDs: catalogOperationIDs(manual.Value.Entries)})
	model := env.createModel(t, user, resourceTestKey('D'), "provider", "local")
	modelID := resourceTestID(t, model.ID)
	assertOperationStatus(t, env, user, resourceTestKey('D'), "recorded", "model", &ResourceOperationResult{ModelID: model.ID})
	selections := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "model-one"}}
	bindingMutation := resourceTestMutation(t, resourceTestKey('E'), http.MethodPost, routeBindingBatch, []int64{modelID}, selections)
	binding, err := env.repository.AddBindings(context.Background(), user, modelID, bindingMutation, 0, selections)
	if err != nil {
		t.Fatal(err)
	}
	assertOperationStatus(t, env, user, resourceTestKey('E'), "recorded", "binding_batch", &ResourceOperationResult{ModelID: model.ID, BindingIDs: bindingOperationIDs(binding.Value.Bindings)})
	// A later batch projects only the newly added connection.
	selections = []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "model-two"}}
	nextMutation := resourceTestMutation(t, resourceTestKey('G'), http.MethodPost, routeBindingBatch, []int64{modelID}, selections)
	next, err := env.repository.AddBindings(context.Background(), user, modelID, nextMutation, 1, selections)
	if err != nil {
		t.Fatal(err)
	}
	assertOperationStatus(t, env, user, resourceTestKey('G'), "recorded", "binding_batch", &ResourceOperationResult{ModelID: model.ID, BindingIDs: bindingOperationIDs(next.Value.Bindings[1:])})
	if got := env.createEndpoint(t, user, resourceTestKey('A')); got.ID != endpoint.ID {
		t.Fatal("endpoint replay changed")
	}
	if got := env.createEndpointKey(t, user, endpointID, resourceTestKey('B')); got.ID != key.ID {
		t.Fatal("key replay changed")
	}
	if got := env.createModel(t, user, resourceTestKey('D'), "provider", "local"); got.ID != model.ID {
		t.Fatal("model replay changed")
	}
	originalSelections := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "model-one"}}
	bindingReplay, err := env.repository.AddBindings(context.Background(), user, modelID, bindingMutation, 0, originalSelections)
	if err != nil || !bindingReplay.Replayed || string(bindingReplay.Body) != string(binding.Body) {
		t.Fatalf("binding replay = (%+v,%v)", bindingReplay, err)
	}
	var projectionText string
	if err := env.store.DB().QueryRow(`SELECT group_concat(result_json) FROM resource_operation_status`).Scan(&projectionText); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"ephemeral-upstream-credential", resourceTestKey('A'), "example.com", "endpoint note", "model-one"} {
		if strings.Contains(projectionText, private) {
			t.Fatalf("projection retained private input %q", private)
		}
	}
	env.clock.Store(resourceTestNow + 100)
	replay, err := env.repository.CreateManualEntries(context.Background(), user, endpointID, keyID, manualMutation, entries)
	if err != nil || !replay.Replayed || string(replay.Body) != string(manual.Body) {
		t.Fatalf("manual replay = (%+v,%v)", replay, err)
	}
	if env.rowCount(t, `SELECT count(*) FROM resource_operation_status`) != 6 {
		t.Fatal("replay duplicated projection")
	}
	var created, expires int64
	if err := env.store.DB().QueryRow(`SELECT created_at,expires_at FROM resource_operation_status WHERE stage='catalog_manual'`).Scan(&created, &expires); err != nil {
		t.Fatal(err)
	}
	if created != resourceTestNow || expires != resourceTestNow+idempotency.ReplayWindowSeconds {
		t.Fatal("replay refreshed original window")
	}
	env.clock.Store(expires - 1)
	assertOperationStatus(t, env, user, resourceTestKey('A'), "recorded", "endpoint", &ResourceOperationResult{EndpointID: endpoint.ID})
	env.clock.Store(expires)
	assertOperationStatus(t, env, user, resourceTestKey('A'), "expired", "", nil)
	// Reuse after the exact window replaces the expired projection once.
	fresh := env.createEndpoint(t, user, resourceTestKey('A'))
	assertOperationStatus(t, env, user, resourceTestKey('A'), "recorded", "endpoint", &ResourceOperationResult{EndpointID: fresh.ID})
	if fresh.ID == endpoint.ID {
		t.Fatal("expired key did not create a new resource")
	}
}

func TestResourceOperationStatusDiscoveryTerminalFailureAndDeletion(t *testing.T) {
	for _, deleteUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal failure", true: "deletion before callback"}[deleteUser], func(t *testing.T) {
			env := newResourceTestEnvironment(t)
			user := env.seedUser(t, "status-discovery")
			endpoint := env.createEndpoint(t, user, resourceTestKey('A'))
			endpointID := resourceTestID(t, endpoint.ID)
			key := env.createEndpointKey(t, user, endpointID, resourceTestKey('B'))
			keyID := resourceTestID(t, key.ID)
			started, release := make(chan struct{}, 1), make(chan struct{})
			env.discovery.mu.Lock()
			env.discovery.started = started
			env.discovery.release = release
			env.discovery.result = DiscoveryClaimResult{FailureClass: DiscoveryFailureAuth}
			env.discovery.mu.Unlock()
			mutation := discoveryMutation(resourceTestKey('C'), endpointID, keyID)
			ctx, cancel := context.WithCancel(context.Background())
			accepted, err := env.repository.RefreshDiscovery(ctx, user, endpointID, keyID, mutation)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("discovery did not start")
			}
			cancel()
			result := &ResourceOperationResult{EndpointID: endpoint.ID, EndpointKeyID: key.ID, OperationID: accepted.Value.OperationID}
			assertOperationStatus(t, env, user, resourceTestKey('C'), "in_progress", "catalog_refresh", result)
			replay, err := env.repository.RefreshDiscovery(context.Background(), user, endpointID, keyID, mutation)
			if err != nil || !replay.Replayed || string(replay.Body) != string(accepted.Body) {
				t.Fatalf("discovery replay = (%+v,%v)", replay, err)
			}
			if deleteUser {
				if _, err := env.store.DB().Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
					t.Fatal(err)
				}
				if env.rowCount(t, `SELECT count(*) FROM resource_operation_status`) != 0 {
					t.Fatal("user deletion retained receipts")
				}
			}
			close(release)
			waitForDiscoveryOperationState(t, env, accepted.Value.OperationID, "completed")
			if deleteUser {
				if env.rowCount(t, `SELECT count(*) FROM resource_operation_status`) != 0 {
					t.Fatal("late callback resurrected receipts")
				}
				if _, err := env.repository.GetResourceOperationStatus(context.Background(), user, resourceTestKey('C')); !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("deleted actor lookup = %v", err)
				}
			} else {
				assertOperationStatus(t, env, user, resourceTestKey('C'), "recorded", "catalog_refresh", result)
				catalog, err := env.repository.GetCatalog(context.Background(), user, endpointID, keyID, 100, "")
				if err != nil || catalog.Evidence.State != "failed" {
					t.Fatalf("recorded failure evidence = (%+v,%v)", catalog, err)
				}
			}
		})
	}
}

func TestResourceOperationStatusHTTPStrictIsolationAndReadOnly(t *testing.T) {
	env := newResourceTestEnvironment(t)
	user := env.seedUser(t, "status-http")
	other := env.seedUser(t, "status-other")
	endpoint := env.createEndpoint(t, user, resourceTestKey('A'))
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, env.repository); err != nil {
		t.Fatal(err)
	}
	handler := registrar.handlers[http.MethodPost+" "+routeOperationStatus]
	call := func(actor int64, target, body string) *ResourceOperationStatus {
		t.Helper()
		response := resourceHTTPCall(t, handler, UserPrincipal{UserID: actor}, http.MethodPost, target, body, "", nil)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("lookup HTTP = %d %s", response.Code, response.Body.String())
		}
		var result ResourceOperationStatus
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return &result
	}
	before := env.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	got := call(user, routeOperationStatus, `{"operation_key":"`+resourceTestKey('A')+`"}`)
	if got.Result.EndpointID != endpoint.ID {
		t.Fatal("lost result ID")
	}
	for _, test := range []struct {
		actor int64
		key   string
	}{{other, resourceTestKey('A')}, {user, resourceTestKey('Z')}} {
		got = call(test.actor, routeOperationStatus, `{"operation_key":"`+test.key+`"}`)
		if *got != (ResourceOperationStatus{Status: "not_recorded"}) {
			t.Fatalf("unknown lookup = %+v", got)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"operation_key":null}`, `{"operation_key":"short"}`, `{"operation_key":"` + resourceTestKey('A') + `","actor":1}`, `{"operation_key":"` + resourceTestKey('A') + `","scope":"control_mutation"}`, `{"operation_key":"` + resourceTestKey('A') + `","route":"/api/endpoints"}`, `{"operation_key":"` + resourceTestKey('A') + `","operation_key":"` + resourceTestKey('B') + `"}`} {
		response := resourceHTTPCall(t, handler, UserPrincipal{UserID: user}, http.MethodPost, routeOperationStatus, body, "", nil)
		if response.Code != 400 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid lookup = %d %s", response.Code, response.Body.String())
		}
	}
	for _, test := range []struct {
		target, body string
		code         int
	}{{routeOperationStatus + "?scope=user", `{"operation_key":"` + resourceTestKey('A') + `"}`, 400}, {routeOperationStatus, strings.Repeat(" ", maxOperationStatusBytes+1), 413}} {
		response := resourceHTTPCall(t, handler, UserPrincipal{UserID: user}, http.MethodPost, test.target, test.body, "", nil)
		if response.Code != test.code {
			t.Fatalf("bounded lookup = %d", response.Code)
		}
	}
	env.authorizer.deny.Store(true)
	response := resourceHTTPCall(t, handler, UserPrincipal{UserID: user}, http.MethodPost, routeOperationStatus, `{"operation_key":"`+resourceTestKey('A')+`"}`, "", nil)
	if response.Code != 403 {
		t.Fatalf("revoked actor lookup = %d", response.Code)
	}
	if env.rowCount(t, `SELECT count(*) FROM idempotency_records`) != before {
		t.Fatal("lookup began replay record")
	}
	env.discovery.mu.Lock()
	calls := env.discovery.calls
	env.discovery.mu.Unlock()
	if calls != 0 {
		t.Fatal("lookup invoked network")
	}
}

func TestResourceOperationProjectionFailureRollsBackEachWriter(t *testing.T) {
	for _, stage := range []string{"endpoint", "key", "model", "catalog_manual", "binding_batch", "catalog_refresh"} {
		t.Run(stage, func(t *testing.T) {
			env := newResourceTestEnvironment(t)
			user := env.seedUser(t, "status-rollback")
			endpoint := env.createEndpoint(t, user, resourceTestKey('A'))
			endpointID := resourceTestID(t, endpoint.ID)
			key := env.createEndpointKey(t, user, endpointID, resourceTestKey('B'))
			keyID := resourceTestID(t, key.ID)
			model := env.createModel(t, user, resourceTestKey('C'), "provider", "original")
			modelID := resourceTestID(t, model.ID)
			entries := []ManualCatalogInput{{UpstreamModelID: "existing"}}
			manual := resourceTestMutation(t, resourceTestKey('D'), http.MethodPost, routeManualCatalog, []int64{endpointID, keyID}, entries)
			if _, err := env.repository.CreateManualEntries(context.Background(), user, endpointID, keyID, manual, entries); err != nil {
				t.Fatal(err)
			}
			tables := []string{"endpoints", "endpoint_keys", "endpoint_key_secrets", "models", "model_catalog_entries", "model_pair_catalog", "model_bindings", "accepted_operations", "idempotency_records", "resource_operation_status"}
			baseline := make(map[string]int)
			for _, table := range tables {
				baseline[table] = env.rowCount(t, "SELECT count(*) FROM "+table)
			}
			if _, err := env.store.DB().Exec(`CREATE TRIGGER reject_status BEFORE INSERT ON resource_operation_status WHEN NEW.stage='` + stage + `' BEGIN SELECT RAISE(ABORT,'receipt rejected'); END`); err != nil {
				t.Fatal(err)
			}
			mutationKey := resourceTestKey('E')
			var err error
			switch stage {
			case "endpoint":
				input := CreateEndpointInput{Source: "custom", ConnectorType: "openai-compatible", BaseURL: "https://example.com/v1", Enabled: true}
				_, err = env.repository.CreateEndpoint(context.Background(), user, resourceTestMutation(t, mutationKey, http.MethodPost, routeEndpoints, nil, input), input)
			case "key":
				input := CreateEndpointKeyInput{Secret: []byte("private-input"), OwnershipConfirmed: true, Enabled: true}
				_, err = env.repository.CreateEndpointKey(context.Background(), user, endpointID, resourceTestMutation(t, mutationKey, http.MethodPost, routeEndpointKeys, []int64{endpointID}, struct{}{}), input)
			case "model":
				input := CreateModelInput{Provider: "provider", Model: "new", RouteStrategy: "ordered"}
				_, err = env.repository.CreateModel(context.Background(), user, resourceTestMutation(t, mutationKey, http.MethodPost, routeModels, nil, input), input)
			case "catalog_manual":
				entries = []ManualCatalogInput{{UpstreamModelID: "new-entry"}}
				_, err = env.repository.CreateManualEntries(context.Background(), user, endpointID, keyID, resourceTestMutation(t, mutationKey, http.MethodPost, routeManualCatalog, []int64{endpointID, keyID}, entries), entries)
			case "binding_batch":
				selections := []BindingSelection{{EndpointKeyID: keyID, UpstreamModelID: "existing"}}
				_, err = env.repository.AddBindings(context.Background(), user, modelID, resourceTestMutation(t, mutationKey, http.MethodPost, routeBindingBatch, []int64{modelID}, selections), 0, selections)
			case "catalog_refresh":
				_, err = env.repository.RefreshDiscovery(context.Background(), user, endpointID, keyID, discoveryMutation(mutationKey, endpointID, keyID))
			}
			if err == nil {
				t.Fatal("projection rejection succeeded")
			}
			for _, table := range tables {
				if got := env.rowCount(t, "SELECT count(*) FROM "+table); got != baseline[table] {
					t.Fatalf("%s changed on rollback: %d -> %d", table, baseline[table], got)
				}
			}
			var state string
			if err := env.store.DB().QueryRow(`SELECT state FROM model_discovery_evidence WHERE endpoint_key_id=?`, keyID).Scan(&state); err != nil || state != "unknown" {
				t.Fatalf("discovery after rollback = %s,%v", state, err)
			}
			assertOperationStatus(t, env, user, mutationKey, "not_recorded", "", nil)
		})
	}
}

func TestResourceOperationStatusAmbiguousReplayScopesRemainUnchanged(t *testing.T) {
	env := newResourceTestEnvironment(t)
	user := env.seedUser(t, "status-scopes")
	shared := resourceTestKey('A')
	endpoint := env.createEndpoint(t, user, shared)
	endpointID := resourceTestID(t, endpoint.ID)
	key := env.createEndpointKey(t, user, endpointID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)
	accepted, err := env.repository.RefreshDiscovery(context.Background(), user, endpointID, keyID, discoveryMutation(shared, endpointID, keyID))
	if err != nil {
		t.Fatal(err)
	}
	waitForDiscoveryOperationState(t, env, accepted.Value.OperationID, "completed")
	if _, err := env.repository.GetResourceOperationStatus(context.Background(), user, shared); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous lookup = %v", err)
	}
	if got := env.createEndpoint(t, user, shared); got.ID != endpoint.ID {
		t.Fatal("control replay changed")
	}
	replay, err := env.repository.RefreshDiscovery(context.Background(), user, endpointID, keyID, discoveryMutation(shared, endpointID, keyID))
	if err != nil || !replay.Replayed {
		t.Fatalf("discovery replay changed: %v", err)
	}
	var stage string
	if err := env.store.DB().QueryRow(`SELECT stage FROM resource_operation_status WHERE key_hash=?`, mustOperationKeyHash(t, shared)).Scan(&stage); err != nil || stage != "endpoint" {
		t.Fatalf("live receipt overwritten = %s,%v", stage, err)
	}
}

func mustOperationKeyHash(t *testing.T, key string) []byte {
	t.Helper()
	hash, err := idempotency.KeyHash(key)
	if err != nil {
		t.Fatal(err)
	}
	return hash[:]
}

type canceledStatusProjection struct {
	resourceTestLifecycleHook
	entered chan struct{}
}

func (projection canceledStatusProjection) ReconcileRoutingProjection(ctx context.Context, _ *sql.Tx, _, _ int64) error {
	close(projection.entered)
	<-ctx.Done()
	return ctx.Err()
}

func TestResourceOperationStatusCanceledMutationCannotWriteLater(t *testing.T) {
	env := newResourceTestEnvironment(t)
	user := env.seedUser(t, "status-cancel")
	entered := make(chan struct{})
	env.repository.projection = canceledStatusProjection{entered: entered}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	input := CreateModelInput{Provider: "provider", Model: "cancelled", RouteStrategy: "ordered"}
	mutation := resourceTestMutation(t, resourceTestKey('A'), http.MethodPost, routeModels, nil, input)
	go func() { _, err := env.repository.CreateModel(ctx, user, mutation, input); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("mutation did not reach transaction hook")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled mutation did not return")
	}
	if env.rowCount(t, `SELECT count(*) FROM models`) != 0 || env.rowCount(t, `SELECT count(*) FROM idempotency_records`) != 0 || env.rowCount(t, `SELECT count(*) FROM resource_operation_status`) != 0 {
		t.Fatal("cancelled mutation retained domain work or receipts")
	}
	assertOperationStatus(t, env, user, resourceTestKey('A'), "not_recorded", "", nil)
}
