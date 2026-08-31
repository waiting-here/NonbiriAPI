package resources

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	issueapi "github.com/waiting-here/NonbiriAPI/internal/issues"
)

type resourceEmptyValidationAuthority struct{}

type resourceFailingRoutingProjection struct {
	ResourceProjectionHook
	err error
}

func (projection resourceFailingRoutingProjection) ReconcileRoutingProjection(
	context.Context,
	*sql.Tx,
	int64,
	int64,
) error {
	return projection.err
}

func (resourceEmptyValidationAuthority) Current(
	context.Context,
	*sql.Tx,
	int64,
	issueapi.ResourceKind,
	int64,
	issueapi.RootCause,
) (issueapi.ResourceValidationState, error) {
	return issueapi.ResourceValidationState{ObservedAt: 0}, nil
}

func (resourceEmptyValidationAuthority) Scan(
	context.Context,
	*sql.Tx,
	int64,
	string,
	int,
) (issueapi.ResourceValidationBatch, error) {
	return issueapi.ResourceValidationBatch{Items: []issueapi.ResourceValidationTarget{}, Done: true}, nil
}

func attachResourceIssueProjection(t *testing.T, environment *resourceTestEnvironment) *issueapi.Service {
	t.Helper()
	issueRepository, err := issueapi.NewRepository(issueapi.Config{
		Store: environment.store, CursorKeys: environment.vault,
		ResourceValidation: resourceEmptyValidationAuthority{},
		Now:                func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("issues.NewRepository: %v", err)
	}
	issueService, err := issueapi.NewService(issueRepository)
	if err != nil {
		t.Fatalf("issues.NewService: %v", err)
	}
	current := environment.repository
	repository, err := New(Config{
		Store: environment.store, Connectors: current.connectors, BaseURLs: current.baseURLs,
		Secrets: environment.secrets, KeyDeletion: environment.deletions,
		KeyCreation: resourceTestLifecycleHook{}, Projection: issueService.Sources(),
		DiscoveryRail: environment.discovery, DiscoveryWorker: environment.worker,
		CursorKeys: environment.vault, FinalAuth: environment.authorizer,
		Random: current.random, Now: func() time.Time { return time.Unix(environment.clock.Load(), 0) },
		OperationID: current.operationID,
	})
	if err != nil {
		t.Fatalf("resources.New with issue projection: %v", err)
	}
	environment.repository = repository
	return issueService
}

func listResourceIssues(t *testing.T, service *issueapi.Service, userID int64, state string) []issueapi.Issue {
	t.Helper()
	page, err := service.List(context.Background(), userID, issueapi.ListQuery{State: state, Limit: 100})
	if err != nil {
		t.Fatalf("list %s issues: %v", state, err)
	}
	return page.Data
}

func findResourceIssue(
	t *testing.T,
	items []issueapi.Issue,
	source issueapi.Source,
	root issueapi.RootCause,
) issueapi.Issue {
	t.Helper()
	for _, item := range items {
		if item.Source == source && item.SummaryCode == string(root) {
			return item
		}
	}
	t.Fatalf("issue %s/%s not found in %#v", source, root, items)
	return issueapi.Issue{}
}

func TestResourceMutationsProjectAuthoritativeIssuesInSameTransaction(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	issueService := attachResourceIssueProjection(t, environment)
	userID := environment.seedUser(t, "resource-issue-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('A'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('B'))
	keyID := resourceTestID(t, key.ID)
	createDeletionTestCandidate(t, environment, userID, endpointID, keyID, resourceTestKey('C'), "issue/upstream")

	model := environment.createModel(t, userID, resourceTestKey('D'), "issue", "logical")
	modelID := resourceTestID(t, model.ID)
	initial := findResourceIssue(t, listResourceIssues(t, issueService, userID, "current"),
		issueapi.SourceRoutingProjection, issueapi.RootNoRoutableBinding)
	if initial.FirstSeenAt != resourceTestNow || initial.Count != "1" {
		t.Fatalf("initial routing issue = %#v", initial)
	}

	addDeletionTestBindings(t, environment, userID, modelID, 0, resourceTestKey('E'), []BindingSelection{{
		EndpointKeyID: keyID, UpstreamModelID: "issue/upstream",
	}})
	if current := listResourceIssues(t, issueService, userID, "current"); len(current) != 0 {
		t.Fatalf("available binding left current issues = %#v", current)
	}

	disabledAt := resourceTestNow + 10
	environment.clock.Store(disabledAt)
	disable := resourceTestMutation(t, resourceTestKey('F'), http.MethodPatch, routeEndpointKey,
		[]int64{endpointID, keyID}, patchEndpointKeyCanonical{Enabled: pointer(false), ExpectedRevision: "1"})
	if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, endpointID, keyID, disable,
		PatchEndpointKeyInput{Enabled: pointer(false), ExpectedRevision: 1}); err != nil {
		t.Fatalf("disable endpoint key: %v", err)
	}
	disabled := findResourceIssue(t, listResourceIssues(t, issueService, userID, "current"),
		issueapi.SourceRoutingProjection, issueapi.RootNoRoutableBinding)
	if disabled.FirstSeenAt != disabledAt || disabled.LastSeenAt != disabledAt || disabled.ID == initial.ID {
		t.Fatalf("disabled routing issue did not use authority-change time: initial=%#v disabled=%#v", initial, disabled)
	}

	environment.clock.Store(disabledAt + 1)
	enable := resourceTestMutation(t, resourceTestKey('G'), http.MethodPatch, routeEndpointKey,
		[]int64{endpointID, keyID}, patchEndpointKeyCanonical{Enabled: pointer(true), ExpectedRevision: "2"})
	if _, err := environment.repository.PatchEndpointKey(context.Background(), userID, endpointID, keyID, enable,
		PatchEndpointKeyInput{Enabled: pointer(true), ExpectedRevision: 2}); err != nil {
		t.Fatalf("enable endpoint key: %v", err)
	}
	if current := listResourceIssues(t, issueService, userID, "current"); len(current) != 0 {
		t.Fatalf("reenabled key left current issues = %#v", current)
	}

	environment.discovery.mu.Lock()
	environment.discovery.result = DiscoveryClaimResult{
		Succeeded: false, FailureClass: DiscoveryFailureAuth, SafeDiagnostic: "credential_rejected",
	}
	environment.discovery.mu.Unlock()
	environment.clock.Store(disabledAt + 2)
	refresh, err := environment.repository.RefreshDiscovery(
		context.Background(), userID, endpointID, keyID, discoveryMutation(resourceTestKey('H'), endpointID, keyID),
	)
	if err != nil {
		t.Fatalf("refresh discovery: %v", err)
	}
	waitForDiscoveryOperationState(t, environment, refresh.Value.OperationID, "completed")
	discoveryIssue := findResourceIssue(t, listResourceIssues(t, issueService, userID, "current"),
		issueapi.SourceModelDiscovery, issueapi.RootDiscoveryFailed)
	if discoveryIssue.SafeDetail != "auth" {
		t.Fatalf("discovery issue = %#v", discoveryIssue)
	}

	deletedAt := disabledAt + 3
	environment.clock.Store(deletedAt)
	deleteEndpoint := resourceTestMutation(t, resourceTestKey('I'), http.MethodDelete, routeEndpoint,
		[]int64{endpointID}, expectedRevisionCanonical{ExpectedRevision: "1"})
	if result, err := environment.repository.DeleteEndpoint(context.Background(), userID, endpointID, deleteEndpoint, 1); err != nil || result.Status != http.StatusNoContent {
		t.Fatalf("delete endpoint = %#v, %v", result, err)
	}
	current := listResourceIssues(t, issueService, userID, "current")
	routingAfterDelete := findResourceIssue(t, current, issueapi.SourceRoutingProjection, issueapi.RootNoRoutableBinding)
	if routingAfterDelete.FirstSeenAt != deletedAt {
		t.Fatalf("routing issue after endpoint delete = %#v", routingAfterDelete)
	}
	for _, closed := range listResourceIssues(t, issueService, userID, "closed") {
		if closed.ID == discoveryIssue.ID && (closed.DeepLink != nil || closed.SafeDetail != "") {
			t.Fatalf("deleted endpoint-key issue retained recoverable identity = %#v", closed)
		}
	}
}

func TestApprovedReportDeletionCapabilityUsesIntegratedResourceCleanup(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "report-deletion-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('J'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('K'))
	keyID := resourceTestID(t, key.ID)
	createDeletionTestCandidate(t, environment, userID, endpointID, keyID, resourceTestKey('L'), "report/upstream")
	model := environment.createModel(t, userID, resourceTestKey('M'), "report", "logical")
	modelID := resourceTestID(t, model.ID)
	addDeletionTestBindings(t, environment, userID, modelID, 0, resourceTestKey('N'), []BindingSelection{{
		EndpointKeyID: keyID, UpstreamModelID: "report/upstream",
	}})

	decisionNow := resourceTestNow + 20
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin approved deletion: %v", err)
	}
	if err := environment.repository.DeleteEndpointKeyForReport(context.Background(), tx, userID, keyID, decisionNow); err != nil {
		_ = tx.Rollback()
		t.Fatalf("DeleteEndpointKeyForReport: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit approved deletion: %v", err)
	}

	if _, err := environment.repository.GetEndpointKey(context.Background(), userID, endpointID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted endpoint key lookup error = %v", err)
	}
	after, err := environment.repository.GetModel(context.Background(), userID, modelID)
	if err != nil || after.BindingRevision != "2" || after.BindingCount != "0" || after.UpdatedAt != decisionNow {
		t.Fatalf("model after approved key deletion = %#v, %v", after, err)
	}
	environment.deletions.mu.Lock()
	deletionCalls := append([]resourceTestEndpointKeyDeletionCall(nil), environment.deletions.calls...)
	environment.deletions.mu.Unlock()
	if len(deletionCalls) != 1 || deletionCalls[0].ownerUserID != userID || deletionCalls[0].decisionNow != decisionNow ||
		len(deletionCalls[0].keyIDs) != 1 || deletionCalls[0].keyIDs[0] != keyID {
		t.Fatalf("approved deletion lifecycle calls = %#v", deletionCalls)
	}
	environment.secrets.mu.Lock()
	orphans := environment.secrets.orphans
	environment.secrets.mu.Unlock()
	if orphans != 1 {
		t.Fatalf("orphaned secret count = %d", orphans)
	}
}

func TestResourceProjectionFailureRollsBackAuthorityMutation(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "projection-rollback-owner")
	endpoint := environment.createEndpoint(t, userID, resourceTestKey('P'))
	endpointID := resourceTestID(t, endpoint.ID)
	key := environment.createEndpointKey(t, userID, endpointID, resourceTestKey('Q'))
	keyID := resourceTestID(t, key.ID)
	createDeletionTestCandidate(t, environment, userID, endpointID, keyID, resourceTestKey('R'), "rollback/upstream")
	model := environment.createModel(t, userID, resourceTestKey('S'), "rollback", "logical")
	modelID := resourceTestID(t, model.ID)
	addDeletionTestBindings(t, environment, userID, modelID, 0, resourceTestKey('T'), []BindingSelection{{
		EndpointKeyID: keyID, UpstreamModelID: "rollback/upstream",
	}})

	projectionErr := errors.New("routing projection unavailable")
	environment.repository.projection = resourceFailingRoutingProjection{
		ResourceProjectionHook: resourceTestLifecycleHook{}, err: projectionErr,
	}
	mutation := resourceTestMutation(t, resourceTestKey('U'), http.MethodPatch, routeEndpointKey,
		[]int64{endpointID, keyID}, patchEndpointKeyCanonical{Enabled: pointer(false), ExpectedRevision: "1"})
	if _, err := environment.repository.PatchEndpointKey(
		context.Background(), userID, endpointID, keyID, mutation,
		PatchEndpointKeyInput{Enabled: pointer(false), ExpectedRevision: 1},
	); !errors.Is(err, projectionErr) {
		t.Fatalf("PatchEndpointKey projection failure = %v", err)
	}
	after, err := environment.repository.GetEndpointKey(context.Background(), userID, endpointID, keyID)
	if err != nil || !after.Enabled || after.Revision != "1" {
		t.Fatalf("endpoint key changed despite projection rollback = %#v, %v", after, err)
	}
	if count := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); count != 5 {
		// Endpoint, key, manual entry, model, and binding setup are the only
		// committed mutations; the failed patch must not leave a replay row.
		t.Fatalf("control mutation rows after projection rollback = %d", count)
	}
}
