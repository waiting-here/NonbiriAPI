package resources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func discoveryTestHTTPHandler(t *testing.T, environment *resourceTestEnvironment) AuthorizedUserHandler {
	t.Helper()
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return registrar.handlers[http.MethodPost+" "+routeDiscovery]
}

func requireDiscoveryServiceUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode service-unavailable response: %v", err)
	}
	if response.Code != http.StatusServiceUnavailable || envelope.Error.Code != "service_unavailable" {
		t.Fatalf("service-unavailable response = %d %s", response.Code, response.Body.String())
	}
}

func requireDiscoveryFailedInterrupted(
	t *testing.T,
	environment *resourceTestEnvironment,
	userID, endpointID, keyID int64,
) {
	t.Helper()
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "failed" || view.Evidence.SafeClass != "interrupted" {
		t.Fatalf("interrupted discovery evidence = %#v, %v", view.Evidence, err)
	}
}

func TestDiscoveryHTTPAcceptanceDoesNotWaitAndDetachesRequestCancellation(t *testing.T) {
	environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 2, 5*time.Second)
	userID := environment.seedUser(t, "discovery-async-http-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	finished := make(chan error, 1)
	environment.discovery.mu.Lock()
	environment.discovery.started = started
	environment.discovery.release = release
	environment.discovery.finished = finished
	environment.discovery.mu.Unlock()

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodPost,
		"https://user.example/api/endpoints/"+endpoint.ID+"/keys/"+key.ID+"/models/refresh", nil).WithContext(requestContext)
	request.Header.Set("Idempotency-Key", resourceTestKey('C'))
	request.SetPathValue("id", endpoint.ID)
	request.SetPathValue("keyId", key.ID)
	recorder := httptest.NewRecorder()
	handler := discoveryTestHTTPHandler(t, environment)
	handlerDone := make(chan struct{})
	go func() {
		handler(recorder, request, UserPrincipal{UserID: userID})
		close(handlerDone)
	}()
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("HTTP 202 waited for the discovery rail")
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("discovery HTTP acceptance = %d %s", recorder.Code, recorder.Body.String())
	}
	var accepted DiscoveryAccepted
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil || accepted.OperationID == "" || accepted.Evidence.State != "checking" {
		t.Fatalf("decode discovery acceptance = %#v, %v", accepted, err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("accepted discovery did not reach the worker")
	}
	cancelRequest()
	select {
	case err := <-finished:
		t.Fatalf("request cancellation ended accepted discovery: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("detached discovery context ended with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("released discovery rail did not finish")
	}
	waitForDiscoveryOperationState(t, environment, accepted.OperationID, "completed")
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "succeeded" {
		t.Fatalf("detached discovery result = %#v, %v", view.Evidence, err)
	}
}

func TestDiscoveryAdmissionCapacityRejectsWithoutWritesAndReplayBypassesWorker(t *testing.T) {
	environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 5*time.Second)
	userID := environment.seedUser(t, "discovery-capacity-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('D'))
	endpointID := resourceTestID(t, endpoint.ID)
	firstKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('E'))
	firstKeyID := resourceTestID(t, firstKey.ID)
	secondKey := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('F'))
	secondKeyID := resourceTestID(t, secondKey.ID)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	environment.discovery.mu.Lock()
	environment.discovery.started = started
	environment.discovery.release = release
	environment.discovery.mu.Unlock()
	firstMutation := discoveryMutation(resourceTestKey('G'), endpointID, firstKeyID)
	first, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, firstKeyID, firstMutation)
	if err != nil || first.Status != http.StatusAccepted {
		t.Fatalf("first discovery acceptance = %#v, %v", first, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first discovery did not occupy worker capacity")
	}
	baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	baselineOperations := environment.rowCount(t, `SELECT count(*) FROM accepted_operations`)

	handler := discoveryTestHTTPHandler(t, environment)
	response := resourceHTTPCall(t, handler, UserPrincipal{UserID: userID}, http.MethodPost,
		"/api/endpoints/"+endpoint.ID+"/keys/"+secondKey.ID+"/models/refresh", "", resourceTestKey('H'),
		map[string]string{"id": endpoint.ID, "keyId": secondKey.ID})
	requireDiscoveryServiceUnavailable(t, response)
	secondView, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, secondKeyID, 100, "")
	if err != nil || secondView.Evidence.State != "unknown" || secondView.Evidence.Revision != "1" ||
		environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency ||
		environment.rowCount(t, `SELECT count(*) FROM accepted_operations`) != baselineOperations {
		t.Fatalf("capacity refusal persisted state: evidence=%#v err=%v", secondView.Evidence, err)
	}

	replay, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, firstKeyID, firstMutation)
	if err != nil || !replay.Replayed || replay.Value.OperationID != first.Value.OperationID {
		t.Fatalf("capacity-full replay = %#v, %v", replay, err)
	}
	environment.discovery.mu.Lock()
	calls := environment.discovery.calls
	environment.discovery.mu.Unlock()
	if calls != 1 || environment.rowCount(t, `SELECT count(*) FROM accepted_operations`) != baselineOperations {
		t.Fatalf("replay queued duplicate work: calls=%d", calls)
	}
	close(release)
	waitForDiscoveryOperationState(t, environment, first.Value.OperationID, "completed")
}

func TestDiscoveryClosedWorkerRejectsWithoutWrites(t *testing.T) {
	environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 5*time.Second)
	userID := environment.seedUser(t, "discovery-closed-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('I'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('J'))
	keyID := resourceTestID(t, key.ID)
	environment.worker.Close()
	baselineAuth := environment.authorizer.calls.Load()
	baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	baselineOperations := environment.rowCount(t, `SELECT count(*) FROM accepted_operations`)

	response := resourceHTTPCall(t, discoveryTestHTTPHandler(t, environment), UserPrincipal{UserID: userID}, http.MethodPost,
		"/api/endpoints/"+endpoint.ID+"/keys/"+key.ID+"/models/refresh", "", resourceTestKey('K'),
		map[string]string{"id": endpoint.ID, "keyId": key.ID})
	requireDiscoveryServiceUnavailable(t, response)
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "unknown" || environment.authorizer.calls.Load() != baselineAuth+1 ||
		environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency ||
		environment.rowCount(t, `SELECT count(*) FROM accepted_operations`) != baselineOperations {
		t.Fatalf("closed-worker refusal state = %#v, %v", view.Evidence, err)
	}
}

func TestDiscoveryCommitFailureReleasesAdmission(t *testing.T) {
	environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 5*time.Second)
	userID := environment.seedUser(t, "discovery-commit-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('L'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('M'))
	keyID := resourceTestID(t, key.ID)
	if _, err := environment.store.DB().Exec(`CREATE TABLE discovery_commit_parent(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create commit-failure parent: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
CREATE TABLE discovery_commit_child(
 operation_id TEXT PRIMARY KEY,
 parent_id INTEGER NOT NULL,
 FOREIGN KEY(parent_id) REFERENCES discovery_commit_parent(id) DEFERRABLE INITIALLY DEFERRED
)`); err != nil {
		t.Fatalf("create commit-failure child: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
CREATE TRIGGER discovery_commit_failure
AFTER INSERT ON accepted_operations
WHEN NEW.kind='model_discovery'
BEGIN
 INSERT INTO discovery_commit_child(operation_id,parent_id) VALUES(NEW.id,1);
END`); err != nil {
		t.Fatalf("create commit-failure trigger: %v", err)
	}
	baselineIdempotency := environment.rowCount(t, `SELECT count(*) FROM idempotency_records`)
	baselineOperations := environment.rowCount(t, `SELECT count(*) FROM accepted_operations`)
	mutation := discoveryMutation(resourceTestKey('N'), endpointID, keyID)
	if _, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation); err == nil {
		t.Fatal("deferred foreign-key commit failure was accepted")
	}
	view, err := environment.repository.GetCatalog(context.Background(), userID, endpointID, keyID, 100, "")
	if err != nil || view.Evidence.State != "unknown" || view.Evidence.Revision != "1" ||
		environment.rowCount(t, `SELECT count(*) FROM idempotency_records`) != baselineIdempotency ||
		environment.rowCount(t, `SELECT count(*) FROM accepted_operations`) != baselineOperations {
		t.Fatalf("commit failure persisted state: evidence=%#v err=%v", view.Evidence, err)
	}
	if _, err := environment.store.DB().Exec(`DROP TRIGGER discovery_commit_failure`); err != nil {
		t.Fatalf("drop commit-failure trigger: %v", err)
	}
	if _, err := environment.store.DB().Exec(`DROP TABLE discovery_commit_child`); err != nil {
		t.Fatalf("drop commit-failure child: %v", err)
	}
	if _, err := environment.store.DB().Exec(`DROP TABLE discovery_commit_parent`); err != nil {
		t.Fatalf("drop commit-failure parent: %v", err)
	}
	accepted, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID, mutation)
	if err != nil || accepted.Status != http.StatusAccepted {
		t.Fatalf("admission after commit rollback = %#v, %v", accepted, err)
	}
	waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
}

func TestDiscoveryWorkerTimeoutsTerminalizeInterrupted(t *testing.T) {
	t.Run("in-flight", func(t *testing.T) {
		environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 500*time.Millisecond)
		userID := environment.seedUser(t, "discovery-inflight-timeout-owner")
		endpoint := environment.createEndpoint(t, userID, resourceTestKey('O'))
		endpointID := resourceTestID(t, endpoint.ID)
		key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('P'))
		keyID := resourceTestID(t, key.ID)
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		finished := make(chan error, 1)
		environment.discovery.mu.Lock()
		environment.discovery.started = started
		environment.discovery.release = release
		environment.discovery.finished = finished
		environment.discovery.mu.Unlock()

		accepted, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID,
			discoveryMutation(resourceTestKey('Q'), endpointID, keyID))
		if err != nil {
			t.Fatalf("accept in-flight timeout discovery: %v", err)
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("in-flight timeout discovery did not start")
		}
		waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
		requireDiscoveryFailedInterrupted(t, environment, userID, endpointID, keyID)
		select {
		case err := <-finished:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("in-flight rail ended with %v", err)
			}
		default:
			t.Fatal("in-flight timeout did not cancel the rail")
		}
	})

	t.Run("queued", func(t *testing.T) {
		environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 2, 500*time.Millisecond)
		userID := environment.seedUser(t, "discovery-queued-timeout-owner")
		endpoint := environment.createEndpoint(t, userID, resourceTestKey('R'))
		endpointID := resourceTestID(t, endpoint.ID)
		key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('S'))
		keyID := resourceTestID(t, key.ID)

		blocker, admitted := environment.worker.ReserveDiscovery()
		if !admitted {
			t.Fatal("reserve queue blocker")
		}
		blockerStarted := make(chan struct{})
		blockerRelease := make(chan struct{})
		blockerDone := make(chan struct{})
		var releaseOnce sync.Once
		t.Cleanup(func() { releaseOnce.Do(func() { close(blockerRelease) }) })
		blocker.Start(func(context.Context) {
			close(blockerStarted)
			<-blockerRelease
			close(blockerDone)
		})
		<-blockerStarted

		accepted, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID,
			discoveryMutation(resourceTestKey('T'), endpointID, keyID))
		if err != nil {
			t.Fatalf("accept queued timeout discovery: %v", err)
		}
		waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
		requireDiscoveryFailedInterrupted(t, environment, userID, endpointID, keyID)
		environment.discovery.mu.Lock()
		calls := environment.discovery.calls
		environment.discovery.mu.Unlock()
		if calls != 0 {
			t.Fatalf("queued timeout started %d network calls", calls)
		}
		releaseOnce.Do(func() { close(blockerRelease) })
		select {
		case <-blockerDone:
		case <-time.After(time.Second):
			t.Fatal("queue blocker did not exit")
		}
	})
}

func TestDiscoveryWorkerCloseCancelsRailAndCompletes(t *testing.T) {
	environment := newResourceTestEnvironmentWithDiscoveryPool(t, 1, 1, 30*time.Second)
	userID := environment.seedUser(t, "discovery-close-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('U'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('V'))
	keyID := resourceTestID(t, key.ID)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	finished := make(chan error, 1)
	environment.discovery.mu.Lock()
	environment.discovery.started = started
	environment.discovery.release = release
	environment.discovery.finished = finished
	environment.discovery.mu.Unlock()
	accepted, err := environment.repository.RefreshDiscovery(context.Background(), userID, endpointID, keyID,
		discoveryMutation(resourceTestKey('W'), endpointID, keyID))
	if err != nil {
		t.Fatalf("accept close-cancel discovery: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("close-cancel discovery did not start")
	}
	closed := make(chan struct{})
	go func() {
		environment.worker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("DiscoveryWorkerPool.Close did not return after bounded cleanup")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("closed worker rail ended with %v", err)
		}
	default:
		t.Fatal("worker close did not cancel the rail")
	}
	waitForDiscoveryOperationState(t, environment, accepted.Value.OperationID, "completed")
	requireDiscoveryFailedInterrupted(t, environment, userID, endpointID, keyID)
	if reservation, admitted := environment.worker.ReserveDiscovery(); admitted {
		reservation.Release()
		t.Fatal("closed discovery worker admitted new work")
	}
}
