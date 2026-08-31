package issues

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
)

func TestIssueSourceTupleMatrixIsClosed(t *testing.T) {
	sources := []Source{SourceModelDiscovery, SourceRoutingProjection, SourceResourceValidator, "unknown"}
	resources := []ResourceKind{ResourceEndpoint, ResourceEndpointKey, ResourceModel, "unknown"}
	roots := []RootCause{RootDiscoveryFailed, RootNoRoutableBinding, RootCredentialInvalid, RootConfigurationInvalid, "unknown"}
	legal := map[[3]string]struct{}{
		{string(SourceModelDiscovery), string(ResourceEndpointKey), string(RootDiscoveryFailed)}:         {},
		{string(SourceRoutingProjection), string(ResourceModel), string(RootNoRoutableBinding)}:          {},
		{string(SourceResourceValidator), string(ResourceEndpoint), string(RootCredentialInvalid)}:       {},
		{string(SourceResourceValidator), string(ResourceEndpoint), string(RootConfigurationInvalid)}:    {},
		{string(SourceResourceValidator), string(ResourceEndpointKey), string(RootCredentialInvalid)}:    {},
		{string(SourceResourceValidator), string(ResourceEndpointKey), string(RootConfigurationInvalid)}: {},
	}
	for _, source := range sources {
		for _, resource := range resources {
			for _, root := range roots {
				_, expected := legal[[3]string{string(source), string(resource), string(root)}]
				if got := validTuple(source, resource, root); got != expected {
					t.Fatalf("validTuple(%q,%q,%q)=%t want=%t", source, resource, root, got, expected)
				}
			}
		}
	}
}

func TestIssueThreeSourcesOwnershipRepeatRecoveryAndRefire(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	ownerID := environment.seedUser(t, "issue-owner")
	otherID := environment.seedUser(t, "issue-other")
	endpointID := environment.seedEndpoint(t, ownerID)
	keyID := environment.seedEndpointKey(t, endpointID)
	modelID := environment.seedModel(t, ownerID, "routing")

	environment.setDiscovery(t, keyID, "failed", "auth", issueTestNow-10)
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("first discovery reconcile: %v", err)
	}
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("duplicate discovery reconcile: %v", err)
	}
	current := listIssues(t, environment, ownerID, "current")
	discovery := findIssue(t, current.Data, SourceModelDiscovery, RootDiscoveryFailed)
	if discovery.Count != "1" || discovery.SafeDetail != "auth" || discovery.DeepLink == nil ||
		discovery.DeepLink.RouteID != "endpoint-detail" || discovery.DeepLink.ResourceID == nil ||
		*discovery.DeepLink.ResourceID != strconv.FormatInt(endpointID, 10) {
		t.Fatalf("first discovery projection: %+v", discovery)
	}

	environment.clock.Add(1)
	environment.setDiscovery(t, keyID, "failed", "timeout", environment.clock.Load())
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("new discovery observation: %v", err)
	}
	discovery = findIssue(t, listIssues(t, environment, ownerID, "current").Data, SourceModelDiscovery, RootDiscoveryFailed)
	if discovery.Count != "2" || discovery.LastSeenAt != environment.clock.Load() || discovery.SafeDetail != "timeout" {
		t.Fatalf("repeated discovery projection: %+v", discovery)
	}
	firstID := discovery.ID

	environment.clock.Add(1)
	environment.setDiscovery(t, keyID, "succeeded", "none", environment.clock.Load())
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("discovery recovery: %v", err)
	}
	if got := listIssues(t, environment, ownerID, "current").Data; len(got) != 0 {
		t.Fatalf("recovered discovery remained current: %+v", got)
	}
	closed := listIssues(t, environment, ownerID, "closed")
	if len(closed.Data) != 1 || closed.Data[0].ID != firstID || closed.Data[0].ClosedAt == nil ||
		*closed.Data[0].ClosedAt != environment.clock.Load() {
		t.Fatalf("closed discovery projection: %+v", closed)
	}

	environment.clock.Add(1)
	environment.setDiscovery(t, keyID, "failed", "protocol", environment.clock.Load())
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("discovery refire: %v", err)
	}
	refired := findIssue(t, listIssues(t, environment, ownerID, "current").Data, SourceModelDiscovery, RootDiscoveryFailed)
	if refired.ID == firstID || refired.Count != "1" {
		t.Fatalf("refire did not create a new occurrence: first=%q current=%+v", firstID, refired)
	}
	var generation int64
	if err := environment.store.DB().QueryRow(`SELECT generation FROM user_issues WHERE id=?`, refired.ID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("refire generation=%d err=%v", generation, err)
	}
	if err := environment.service.ReconcileModelDiscovery(context.Background(), otherID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner discovery err=%v", err)
	}
	if page := listIssues(t, environment, otherID, "current"); len(page.Data) != 0 {
		t.Fatalf("cross-owner projection leaked: %+v", page)
	}

	if err := environment.service.ReconcileRoutingProjection(context.Background(), ownerID, modelID); err != nil {
		t.Fatalf("routing reconcile without binding: %v", err)
	}
	routing := findIssue(t, listIssues(t, environment, ownerID, "current").Data, SourceRoutingProjection, RootNoRoutableBinding)
	if routing.DeepLink == nil || routing.DeepLink.RouteID != "models" || routing.DeepLink.ResourceID == nil ||
		*routing.DeepLink.ResourceID != strconv.FormatInt(modelID, 10) {
		t.Fatalf("routing deep link: %+v", routing)
	}
	environment.makeBindingAvailable(t, modelID, keyID, "upstream-model")
	if err := environment.service.ReconcileRoutingProjection(context.Background(), ownerID, modelID); err != nil {
		t.Fatalf("routing recovery: %v", err)
	}
	assertNoCurrentIssue(t, environment, ownerID, SourceRoutingProjection, RootNoRoutableBinding)

	environment.validation.set(ownerID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
		Active: true, ObservedAt: environment.clock.Load(), SafeDetail: "invalid_configuration",
	})
	if err := environment.service.ReconcileResourceValidation(context.Background(), ownerID, ResourceEndpoint, endpointID, RootConfigurationInvalid); err != nil {
		t.Fatalf("resource validation reconcile: %v", err)
	}
	validation := findIssue(t, listIssues(t, environment, ownerID, "current").Data, SourceResourceValidator, RootConfigurationInvalid)
	if validation.ResourceKind != ResourceEndpoint || validation.SafeDetail != "invalid_configuration" ||
		validation.DeepLink == nil || validation.DeepLink.RouteID != "endpoint-detail" {
		t.Fatalf("resource validation projection: %+v", validation)
	}
	environment.clock.Add(1)
	environment.validation.set(ownerID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
		ObservedAt: environment.clock.Load(),
	})
	if err := environment.service.ReconcileResourceValidation(context.Background(), ownerID, ResourceEndpoint, endpointID, RootConfigurationInvalid); err != nil {
		t.Fatalf("resource validation recovery: %v", err)
	}
	assertNoCurrentIssue(t, environment, ownerID, SourceResourceValidator, RootConfigurationInvalid)

	if err := environment.service.ReconcileResourceValidation(context.Background(), ownerID, ResourceModel, modelID, RootCredentialInvalid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("illegal resource-validator tuple err=%v", err)
	}
	environment.validation.set(ownerID, ResourceEndpoint, endpointID, RootCredentialInvalid, ResourceValidationState{
		Active: true, ObservedAt: environment.clock.Load(), SafeDetail: "hostile\ncontrol",
	})
	if err := environment.service.ReconcileResourceValidation(context.Background(), ownerID, ResourceEndpoint, endpointID, RootCredentialInvalid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe detail err=%v", err)
	}
}

func TestIssueReportReasonFilteringAndRederivation(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	ownerID := environment.seedUser(t, "report-owner")
	endpointID := environment.seedEndpoint(t, ownerID)
	keyID := environment.seedEndpointKey(t, endpointID)
	environment.setDiscovery(t, keyID, "failed", "auth", issueTestNow)
	environment.validation.set(ownerID, ResourceEndpointKey, keyID, RootCredentialInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "credential_rejected",
	})
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("seed discovery issue: %v", err)
	}
	if err := environment.service.ReconcileResourceValidation(context.Background(), ownerID, ResourceEndpointKey, keyID, RootCredentialInvalid); err != nil {
		t.Fatalf("seed validator issue: %v", err)
	}
	if got := len(listIssues(t, environment, ownerID, "current").Data); got != 2 {
		t.Fatalf("current before report reason=%d want=2", got)
	}
	caseID := environment.insertReportReason(t, keyID)
	withIssueTx(t, environment, func(tx *sql.Tx) error {
		return environment.service.Sources().ReconcileReportReason(context.Background(), tx, ownerID, keyID)
	})
	if got := len(listIssues(t, environment, ownerID, "current").Data); got != 0 {
		t.Fatalf("report reason did not filter current issues: %d", got)
	}

	injectedID := issueOpaqueID("iss_", 9001)
	if _, err := environment.store.DB().Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 deep_link_kind,deep_link_ref,first_seen_at,last_seen_at,count
) VALUES(?,?,'resource_validator','endpoint_key',?,'configuration_invalid',1,'current','configuration_invalid','',
 'endpoint_key',?,?,?,1)`, injectedID, ownerID, strconv.FormatInt(keyID, 10), strconv.FormatInt(endpointID, 10), issueTestNow, issueTestNow); err != nil {
		t.Fatalf("inject filtered projection: %v", err)
	}
	if got := len(listIssues(t, environment, ownerID, "current").Data); got != 0 {
		t.Fatalf("list leaked issue hidden by live report reason: %+v", listIssues(t, environment, ownerID, "current"))
	}
	if _, err := environment.store.DB().Exec(`DELETE FROM user_issues WHERE id=?`, injectedID); err != nil {
		t.Fatalf("remove injected projection: %v", err)
	}
	if _, err := environment.store.DB().Exec(`DELETE FROM endpoint_key_suspensions WHERE endpoint_key_id=? AND report_case_id=?`, keyID, caseID); err != nil {
		t.Fatalf("remove report reason: %v", err)
	}
	withIssueTx(t, environment, func(tx *sql.Tx) error {
		return environment.service.Sources().ReconcileReportReason(context.Background(), tx, ownerID, keyID)
	})
	current := listIssues(t, environment, ownerID, "current")
	if len(current.Data) != 2 {
		t.Fatalf("reason removal did not rederive authorities: %+v", current)
	}
	for _, issue := range current.Data {
		if issue.Count != "1" {
			t.Fatalf("rebuild manufactured occurrence count: %+v", issue)
		}
	}
}

func TestIssueResourceDeletionScrubsCurrentAndClosedIdentity(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	ownerID := environment.seedUser(t, "delete-owner")
	otherID := environment.seedUser(t, "delete-other")
	endpointID := environment.seedEndpoint(t, ownerID)
	keyID := environment.seedEndpointKey(t, endpointID)
	environment.setDiscovery(t, keyID, "failed", "auth", issueTestNow)
	if err := environment.service.ReconcileModelDiscovery(context.Background(), ownerID, keyID); err != nil {
		t.Fatalf("seed deletion issue: %v", err)
	}
	original := findIssue(t, listIssues(t, environment, ownerID, "current").Data, SourceModelDiscovery, RootDiscoveryFailed)
	if err := environment.service.ReconcileModelDiscovery(context.Background(), otherID, keyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner pre-delete err=%v", err)
	}
	withIssueTx(t, environment, func(tx *sql.Tx) error {
		if err := environment.service.Sources().PrepareEndpointKeyDeletion(context.Background(), tx, ownerID, []int64{keyID}, environment.clock.Load()); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `DELETE FROM endpoint_keys WHERE id=?`, keyID)
		return err
	})
	var state, resourceRef, safeDetail string
	var deepKind, deepRef sql.NullString
	var closedAt sql.NullInt64
	if err := environment.store.DB().QueryRow(`
SELECT state,resource_ref,safe_detail,deep_link_kind,deep_link_ref,closed_at FROM user_issues WHERE id=?`, original.ID).
		Scan(&state, &resourceRef, &safeDetail, &deepKind, &deepRef, &closedAt); err != nil {
		t.Fatalf("read scrubbed issue: %v", err)
	}
	if state != "closed" || resourceRef == strconv.FormatInt(keyID, 10) || !validDecimalResource(resourceRef) ||
		safeDetail != "" || deepKind.Valid || deepRef.Valid || !closedAt.Valid {
		t.Fatalf("scrubbed issue state=%q ref=%q detail=%q deep=(%+v,%+v) closed=%+v", state, resourceRef, safeDetail, deepKind, deepRef, closedAt)
	}
	closed := listIssues(t, environment, ownerID, "closed")
	if len(closed.Data) != 1 || closed.Data[0].DeepLink != nil || closed.Data[0].SafeDetail != "" {
		t.Fatalf("scrubbed DTO: %+v", closed)
	}
}

func listIssues(t *testing.T, environment *issueTestEnvironment, userID int64, state string) Page {
	t.Helper()
	page, err := environment.service.List(context.Background(), userID, ListQuery{State: state, Limit: maxPageLimit})
	if err != nil {
		t.Fatalf("List(%s): %v", state, err)
	}
	return page
}

func findIssue(t *testing.T, issues []Issue, source Source, root RootCause) Issue {
	t.Helper()
	for _, issue := range issues {
		if issue.Source == source && issue.SummaryCode == string(root) {
			return issue
		}
	}
	t.Fatalf("issue %s/%s not found in %+v", source, root, issues)
	return Issue{}
}

func assertNoCurrentIssue(t *testing.T, environment *issueTestEnvironment, userID int64, source Source, root RootCause) {
	t.Helper()
	for _, issue := range listIssues(t, environment, userID, "current").Data {
		if issue.Source == source && issue.SummaryCode == string(root) {
			t.Fatalf("unexpected current issue: %+v", issue)
		}
	}
}

func withIssueTx(t *testing.T, environment *issueTestEnvironment, run func(*sql.Tx) error) {
	t.Helper()
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin issue transaction: %v", err)
	}
	defer tx.Rollback()
	if err := run(tx); err != nil {
		t.Fatalf("issue transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit issue transaction: %v", err)
	}
}
