package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestRerankPathSnapshotsManagementIsolationAndCAS(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "rerank-owner", nil, false)
	level := int64(6)
	steward := e.seedUser(t, "rerank-steward", &level, false)
	endpoint, key := e.seedEndpointKey(t, owner, 'p')
	ctx := context.Background()
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE endpoints SET native_service_mask=32767,rerank_format='generic',rerank_path='/submitted/reranks' WHERE id=?", endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err = resources.AuthorizeProtocolServicesTx(ctx, tx, owner, key, e.clock.Load(), false); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	createInput := CreateInput{Description: "rerank donation", Keys: []CreateKeyInput{{EndpointKeyID: key, NativeServices: []contract.NativeService{contract.NativeServiceRerank}}}, OwnershipAuthorized: true}
	created, err := e.service.Create(ctx, owner, donationMutation(t, 'A', http.MethodPost, routeDonations, nil, createInput), createInput)
	if err != nil {
		t.Fatal(err)
	}
	d := created.Value
	id := parseTestID(t, d.ID)
	var use Use
	for _, candidate := range ownerUsesForTest(t, e, owner, id) {
		if candidate.NativeService == contract.NativeServiceRerank {
			use = candidate
			break
		}
	}
	if use.Pending == nil || use.Pending.RerankPath != "/submitted/reranks" || use.EffectiveRerankPath != "/submitted/reranks" || use.Capabilities.CanEditRerankPath {
		t.Fatalf("pending snapshot: %+v", use)
	}
	useID := parseTestID(t, use.ID)
	if _, err = e.store.DB().Exec("UPDATE endpoints SET rerank_path='/self/reranks' WHERE id=?", endpoint); err != nil {
		t.Fatal(err)
	}
	items := []UseReviewItem{{UseID: useID, ExpectedRevision: parseTestID(t, use.Revision), Decision: "approve", ReviewNote: "approved"}}
	receipt, err := e.service.reviewUses(ctx, reviewerAdmin, 0, id, items, donationMutation(t, 'q', http.MethodPost, routeAdminUses+"/review", []int64{id}, items))
	if err != nil || !receipt.Value.Results[0].OK {
		t.Fatal(receipt, err)
	}
	approvedPage, err := e.service.UsesPage(ctx, reviewerAdmin, 0, id, UseFilter{}, pagination.Default())
	if err != nil || len(approvedPage.Data) != 1 {
		t.Fatal(approvedPage, err)
	}
	use = approvedPage.Data[0]
	if use.Approved.RerankPath != "/submitted/reranks" || use.EffectiveRerankPath != "/submitted/reranks" || !use.Capabilities.CanEditRerankPath {
		t.Fatal(use)
	}
	e.store.SetManagementCapture(managementlog.CaptureEventID)
	for index, override := range []*string{ptrRerank("/managed/reranks"), ptrRerank("/managed-next/reranks"), ptrRerank(""), nil} {
		auditCtx := managementlog.WithOperation(ctx, managementlog.Metadata{Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(steward), Name: "Steward", Role: "level6"}, Domain: "donations", Action: "rerank_path.update"})
		input := UsePatch{ExpectedRevision: parseTestID(t, use.Revision), RerankPathOverride: &override}
		mutation := donationMutation(t, byte('r'+index), http.MethodPatch, routeStewardUses+"/{useId}", []int64{id, useID}, input)
		result, err := e.service.mutateUse(auditCtx, reviewerSteward, steward, id, useID, input, "rerank_path", mutation)
		if err != nil {
			t.Fatal(err)
		}
		var details, changes, outcome string
		if err = e.store.DB().QueryRow("SELECT details,changes,outcome FROM management_operation_items WHERE operation_id=(SELECT id FROM management_operations WHERE event_id=?)", managementlog.CaptureEventID(auditCtx)).Scan(&details, &changes, &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome != "succeeded" || !strings.Contains(changes, "rerank_path_override") || !strings.Contains(details, "updated") || strings.Contains(details+changes, "/managed") || strings.Contains(details+changes, "/submitted") {
			t.Fatal(details, outcome)
		}
		want := "/submitted/reranks"
		if override != nil {
			want = *override
		}
		if result.Value.EffectiveRerankPath != want || result.Value.DefaultRerankURL == "" {
			t.Fatal(result.Value)
		}
		replay, err := e.service.mutateUse(ctx, reviewerSteward, steward, id, useID, input, "rerank_path", mutation)
		if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
			t.Fatal(replay, err)
		}
		mutation.IdempotencyKey = donationMutation(t, byte('w'+index), http.MethodPatch, routeStewardUses+"/{useId}", []int64{id, useID}, input).IdempotencyKey
		if _, err = e.service.mutateUse(ctx, reviewerSteward, steward, id, useID, input, "rerank_path", mutation); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		use = result.Value
		tx, err = e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		self, err := connectionstore.LoadSelfGrantTx(ctx, tx, owner, key)
		if err != nil {
			t.Fatal(err)
		}
		approved, err := connectionstore.LoadApprovedUseTx(ctx, tx, parseTestID(t, use.DonationKeyID), contract.NativeServiceRerank)
		tx.Rollback()
		if err != nil || self.RerankPath != "/self/reranks" || approved.Grant.RerankPath != want {
			t.Fatal(self.RerankPath, approved.Grant.RerankPath, err)
		}
	}
	five := int64(5)
	trainee := e.seedUser(t, "path-trainee", &five, false)
	path := ptrRerank("/scoped")
	scoped := UsePatch{ExpectedRevision: parseTestID(t, use.Revision), RerankPathOverride: &path}
	scopedMutation := donationMutation(t, 'G', http.MethodPatch, routeStewardUses+"/{useId}", []int64{id, useID}, scoped)
	permissionCtx := authz.WithStewardPermission(ctx, "donations.manage_keys")
	if _, err = e.service.mutateUse(permissionCtx, reviewerSteward, trainee, id, useID, scoped, "rerank_path", scopedMutation); !errors.Is(err, ErrNotFound) {
		t.Fatal("unqualified scope", err)
	}
	qualifyStandardDonation(t, e, d.ID)
	allowed, err := e.service.mutateUse(permissionCtx, reviewerSteward, trainee, id, useID, scoped, "rerank_path", scopedMutation)
	if err != nil {
		t.Fatal(err)
	}
	use = allowed.Value
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["donations.manage_keys"] = false
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err = e.store.DB().Exec("INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'full',?,?)", trainee, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err = e.service.mutateUse(permissionCtx, reviewerSteward, trainee, id, useID, scoped, "rerank_path", scopedMutation); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked replay permission", err)
	}
	override := ptrRerank("/forbidden")
	input := UsePatch{ExpectedRevision: parseTestID(t, use.Revision), RerankPathOverride: &override}
	if _, err = e.service.mutateUse(ctx, recurringOwner, owner, id, useID, input, "rerank_path", donationMutation(t, 'z', http.MethodPatch, routeOwnerUse, []int64{id, useID}, input)); err == nil {
		t.Fatal("owner changed management override")
	}
	page, err := e.service.UsesPage(ctx, reviewerSteward, steward, id, UseFilter{}, pagination.Default())
	if err != nil || len(page.Data) == 0 {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	if err = RegisterAdminRoutes(recurringHTTPRegistrar{mux: mux}, e.service); err != nil {
		t.Fatal(err)
	}
	if err = RegisterOwnerRoutes(recurringHTTPRegistrar{mux: mux, actor: owner}, e.service); err != nil {
		t.Fatal(err)
	}
	if err = RegisterStewardRoutes(recurringHTTPRegistrar{mux: mux, actor: steward}, e.service); err != nil {
		t.Fatal(err)
	}
	call := func(path, body string, caller bool) int {
		request := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
		request.Header.Set("Idempotency-Key", "rerank-http-request-key-1234567890")
		if caller {
			request = request.WithContext(authz.WithStewardCaller(request.Context(), authz.StewardCaller{UserID: steward, Generation: 1}))
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response.Code
	}
	adminPath := "/admin/api/donations/" + d.ID + "/uses/" + use.ID
	for _, body := range []string{
		`{"expected_revision":"1"}`,
		`{"expected_revision":"1","rerank_path_override":"/ok","enabled":true}`,
		`{"expected_revision":"1","rerank_path_override":"https://other.example/rerank"}`,
	} {
		if status := call(adminPath, body, false); status != 400 {
			t.Fatal("invalid management input", status)
		}
	}
	body := fmt.Sprintf(`{"expected_revision":%q,"rerank_path_override":null}`, use.Revision)
	if status := call("/api/donations/"+d.ID+"/uses/"+use.ID, body, false); status != 400 {
		t.Fatal("owner accepted override", status)
	}
	if status := call("/api/steward/donations/"+d.ID+"/uses/"+use.ID, body, true); status != 403 {
		t.Fatal("CallerKey accepted session-only path edit", status)
	}
	if status := call(adminPath, body, false); status != 200 {
		t.Fatal("admin clear", status)
	}

	if _, err = e.store.DB().Exec("UPDATE donation_use_revisions SET rerank_path='/changed' WHERE id=(SELECT current_approved_revision_id FROM donation_uses WHERE id=?)", useID); err == nil {
		t.Fatal("approved path snapshot was mutable")
	}
}
func ptrRerank(s string) *string { return &s }
