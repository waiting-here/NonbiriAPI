package stewardautomation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestAutomationDiscoverySkipsUnhealthySharedUse(t *testing.T) {
	f := newPersonalFixture(t)
	key, err := strconv.ParseInt(f.key(t, "j"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := tx.QueryRowContext(t.Context(), `SELECT source_id FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?`, key).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(t.Context(), tx, connectionstore.SourceAuthorizationRef{OwnerUserID: f.userID, SourceID: sourceID, Purpose: connectionstore.SourceSelf})
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	input := donation.CreateInput{Description: "shared discovery", OwnershipAuthorized: true, Keys: []donation.CreateKeyInput{{EndpointKeyID: key, NativeServices: []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings}, DiscoverySourceID: &sourceID, DiscoverySourceRevision: &source.SourceRevision}}}
	created, err := f.service.donations.Create(f.ctx, f.userID, automationMutation("D", "POST", "/api/donations", nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	donationID, _ := strconv.ParseInt(created.Value.ID, 10, 64)
	member, _ := strconv.ParseInt(created.Value.Keys[0].ID, 10, 64)
	setting := donation.KeySetting{DonationKeyID: member, Enabled: true}
	if revision := created.Value.Keys[0].Review.Revision; revision != nil {
		parsed, _ := strconv.ParseInt(*revision, 10, 64)
		setting.ExpectedReviewRevision = &parsed
	}
	review := donation.ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "approved", KeySettings: []donation.KeySetting{setting}}
	steward := authz.WithStewardCaller(context.Background(), authz.StewardCaller{UserID: f.userID, Generation: 1})
	if _, err := f.service.donations.ReviewSteward(steward, f.userID, donationID, automationMutation("R", "POST", "/api/steward/donations/{id}/review", []string{created.Value.ID}, review), review); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id
WHERE u.donation_key_id=? AND u.native_service_bit=1`, f.clock.Load()+60, f.clock.Load(), member)
	tx, err = f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	selected, err := ownDiscoveryTx(t.Context(), tx, f.userID, member, f.clock.Load())
	if err != nil || selected.identity.NativeService != contract.NativeServiceEmbeddings || selected.identity.SourceRevisionID != source.SourceRevisionID {
		t.Fatalf("automation chose cooling chat instead of healthy shared embeddings: %+v %v", selected, err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE id=?`, selected.identity.UseID); err != nil {
		t.Fatal(err)
	}
	if current, err := ownDiscoveryTx(t.Context(), tx, f.userID, member, f.clock.Load()); err != nil || current.identity.UseID != selected.identity.UseID {
		t.Fatalf("legacy sharing flag blocked catalog: %+v %v", current, err)
	}
	selected, err = ownDiscoveryTx(t.Context(), tx, f.userID, member, f.clock.Load()+61)
	if err != nil || selected.identity.NativeService != contract.NativeServiceChatCompletions {
		t.Fatalf("elapsed cooldown did not use decision time: %+v %v", selected, err)
	}
}

type discoveryReviewRegistrar struct {
	mux    *http.ServeMux
	userID int64
}

func (r discoveryReviewRegistrar) RegisterUserRoute(method, pattern string, next donation.AuthorizedUserHandler) error {
	r.mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, req *http.Request) {
		next(w, req, donation.UserPrincipal{UserID: r.userID})
	})
	return nil
}

func TestAutomationDiscoveryBindsApprovedUseOnPendingDonation(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "standard fallback"}[fallback], func(t *testing.T) {
			testAutomationDiscoveryBindsApprovedUseOnPendingDonation(t, fallback)
		})
	}
}
func testAutomationDiscoveryBindsApprovedUseOnPendingDonation(t *testing.T, fallback bool) {
	f := newPersonalFixture(t)
	key, err := strconv.ParseInt(f.key(t, "p"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	steward := authz.WithStewardCaller(t.Context(), authz.StewardCaller{UserID: f.userID, Generation: 1})
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var sourceID int64
	if err := tx.QueryRowContext(t.Context(), `SELECT source_id FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?`, key).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(t.Context(), tx, connectionstore.SourceAuthorizationRef{OwnerUserID: f.userID, SourceID: sourceID, Purpose: connectionstore.SourceSelf})
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	input := donation.CreateInput{Description: "partially approved discovery", OwnershipAuthorized: true, Keys: []donation.CreateKeyInput{{EndpointKeyID: key, NativeServices: []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings}, DiscoverySourceID: &sourceID, DiscoverySourceRevision: &source.SourceRevision}}}
	created, err := f.service.donations.Create(f.ctx, f.userID, automationMutation("D", http.MethodPost, "/api/donations", nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	donationID, err := strconv.ParseInt(created.Value.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	member, err := strconv.ParseInt(created.Value.Keys[0].ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	uses, err := f.service.donations.UsesPage(f.ctx, "owner", f.userID, donationID, donation.UseFilter{DonationKeyID: member}, pagination.Default())
	if err != nil || len(uses.Data) != 2 {
		t.Fatalf("initial uses: %+v %v", uses, err)
	}
	var chat, embeddings donation.Use
	for _, use := range uses.Data {
		switch use.NativeService {
		case contract.NativeServiceChatCompletions:
			chat = use
		case contract.NativeServiceEmbeddings:
			embeddings = use
		}
	}
	if chat.Pending == nil || chat.Approved != nil || embeddings.Pending == nil || embeddings.Approved != nil {
		t.Fatal("fixture must begin with two pending uses")
	}
	var embeddingsPendingID int64
	if err := f.store.DB().QueryRow(`SELECT current_pending_revision_id FROM donation_uses WHERE id=?`, embeddings.ID).Scan(&embeddingsPendingID); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual per-use approval route, including its body-review CAS.
	mux := http.NewServeMux()
	if err := donation.RegisterStewardRoutes(discoveryReviewRegistrar{mux: mux, userID: f.userID}, f.service.donations); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"items": []map[string]any{{"use_id": chat.ID, "expected_revision": chat.Revision, "expected_review_revision": chat.Pending.ExpectedReviewRevision, "decision": "approve", "review_note": "approve chat only"}}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/steward/donations/"+created.Value.ID+"/uses/review", strings.NewReader(string(body))).WithContext(steward)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", strings.Repeat("R", 22))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var receipt donation.UseReviewReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != http.StatusOK || len(receipt.Results) != 1 || !receipt.Results[0].OK {
		t.Fatalf("partial use approval: status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	before, err := f.service.donations.GetOwner(f.ctx, f.userID, donationID)
	if err != nil || before.Status != "pending" || before.UseSummary.Approved != "1" || before.UseSummary.Pending != "1" {
		t.Fatalf("partial approval changed the donation header: %+v %v", before, err)
	}
	if fallback {
		f.exec(t, "DELETE FROM discovery_sources WHERE user_id=?", f.userID)
		f.exec(t, "UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE donation_key_id=?", member)
		// The unrelated pending use's resource revision is intentionally retained
		// below as part of the assertion that automatic binding cannot approve it.
		if err := f.store.DB().QueryRow("SELECT CAST(revision AS TEXT) FROM donation_uses WHERE id=?", embeddings.ID).Scan(&embeddings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	tx, err = f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, member, contract.NativeServiceChatCompletions)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	zero := "0"
	modelInput := charityrouting.ModelCreate{Provider: "fixture", Model: "partial-donation", Enabled: true, Pricing: charityrouting.PricingInput{Mode: "per_request", UserPrice: &zero, DonorReward: &zero}}
	model, err := f.service.charity.CreateSteward(steward, f.userID, automationMutation("C", http.MethodPost, "/api/steward/charity-models", nil, modelInput), modelInput)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := strconv.ParseInt(model.Value.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(steward, 10*time.Second)
	defer cancel()
	policy := authz.DefaultStewardPermissions(6)
	policy.Permissions["charity.non_mainstream"] = false
	writePolicy := func() {
		t.Helper()
		permissions, _ := json.Marshal(policy.Permissions)
		limits, _ := json.Marshal(policy.Limits)
		f.exec(t, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, f.userID, string(permissions), string(limits), f.clock.Load(), f.clock.Load())
	}
	f.exec(t, `UPDATE users SET level=5 WHERE id=?`, f.userID)
	writePolicy()
	sourceCount := f.count(t, `SELECT count(*) FROM discovery_sources`)
	if err := f.service.bindOne(ctx, f.userID, modelID, member, "exact/model", false); !errors.Is(err, charityrouting.ErrNotFound) || f.calls.Load() != 0 || f.count(t, `SELECT count(*) FROM discovery_sources`) != sourceCount {
		t.Fatal("restricted model started discovery", err)
	}
	policy.Permissions["charity.non_mainstream"] = true
	policy.Permissions["donations.discover"] = false
	writePolicy()
	if err := f.service.bindOne(ctx, f.userID, modelID, member, "exact/model", false); !errors.Is(err, authz.ErrPermissionDenied) || f.calls.Load() != 0 || f.count(t, `SELECT count(*) FROM discovery_sources`) != sourceCount {
		t.Fatal("revoked discover grant started discovery", err)
	}
	policy.Permissions["donations.discover"] = true
	policy.Permissions["donations.review"] = false
	writePolicy()
	tx, err = f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	reviewErr := f.service.donations.ApproveOwnNewInTransaction(ctx, tx, f.userID, donationID, donation.ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []donation.KeySetting{{DonationKeyID: member, Enabled: true}}})
	tx.Rollback()
	if !errors.Is(reviewErr, authz.ErrPermissionDenied) {
		t.Fatal("automation approval ignored review grant", reviewErr)
	}
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	if err := f.service.bindOne(ctx, f.userID, modelID, member, "exact/model", false); err != nil {
		t.Fatalf("automatic binding of the approved use on a pending donation: %v", err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("expected one real source worker request, got %d", f.calls.Load())
	}
	if fallback {
		tx, err := f.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := connectionstore.LoadDiscoveryUseTx(t.Context(), tx, approved.UseID, approved.ApprovedRevisionID)
		tx.Rollback()
		if err != nil || catalog.DiscoverySourceRevisionID <= 0 {
			t.Fatalf("standard catalog was not persisted: %+v %v", catalog, err)
		}
		approved.DiscoverySourceRevisionID = catalog.DiscoverySourceRevisionID
	}
	var runState, operationState, authorizationKind string
	var runUse, runApproval, runSource, runVersion, generation int64
	if err := f.store.DB().QueryRow(`SELECT r.state,o.state,r.authorization_kind,r.authorization_use_id,r.authorization_use_revision_id,r.source_revision_id,r.credential_version_id,r.result_generation
FROM discovery_source_states st JOIN discovery_runs r ON r.id=st.last_successful_run_id JOIN accepted_operations o ON o.id=r.operation_id WHERE st.source_revision_id=?`, approved.DiscoverySourceRevisionID).Scan(&runState, &operationState, &authorizationKind, &runUse, &runApproval, &runSource, &runVersion, &generation); err != nil {
		t.Fatal(err)
	}
	if runState != "succeeded" || operationState != "completed" || authorizationKind != "donation_use" || runUse != approved.UseID || runApproval != approved.ApprovedRevisionID || runSource != approved.DiscoverySourceRevisionID || runVersion != approved.Grant.CredentialVersionID || generation <= 1 {
		t.Fatalf("worker lost the approved source/version: state=%s operation=%s kind=%s use=%d approval=%d source=%d version=%d generation=%d", runState, operationState, authorizationKind, runUse, runApproval, runSource, runVersion, generation)
	}
	var targetMask int64
	if err := f.store.DB().QueryRow(`SELECT t.native_service_mask FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id WHERE b.charity_model_id=? AND t.donation_key_id=? AND t.upstream_model_id='exact/model'`, modelID, member).Scan(&targetMask); err != nil {
		t.Fatal(err)
	}
	chatSet, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceChatCompletions})
	if err != nil || targetMask != chatSet.Mask() || f.count(t, `SELECT count(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=?`, modelID) != 1 {
		t.Fatalf("binding included an unapproved service: mask=%d err=%v", targetMask, err)
	}
	after, err := f.service.donations.GetOwner(f.ctx, f.userID, donationID)
	if err != nil || after.Status != "pending" || after.UseSummary.Approved != "1" || after.UseSummary.Pending != "1" {
		t.Fatalf("discovery/binding changed approval state: %+v %v", after, err)
	}
	var pendingID int64
	if err := f.store.DB().QueryRow(`SELECT current_pending_revision_id FROM donation_uses WHERE id=? AND current_approved_revision_id IS NULL AND revision=?`, embeddings.ID, embeddings.Revision).Scan(&pendingID); err != nil || pendingID != embeddingsPendingID {
		t.Fatalf("unapproved embeddings were changed: pending=%d err=%v", pendingID, err)
	}
	tx, err = f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, member, contract.NativeServiceEmbeddings); !errors.Is(err, connectionstore.ErrUnavailable) && !errors.Is(err, connectionstore.ErrNotFound) {
		t.Fatalf("unapproved embeddings gained inference authorization: %v", err)
	}
	embeddingsID, err := strconv.ParseInt(embeddings.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	pendingCatalog, err := connectionstore.LoadDiscoveryUseTx(t.Context(), tx, embeddingsID, 0)
	if err != nil || pendingCatalog.RevisionID != embeddingsPendingID || pendingCatalog.Grant.CredentialVersionID != approved.Grant.CredentialVersionID {
		t.Fatalf("pending catalog selection: %+v %v", pendingCatalog, err)
	}
	_ = tx.Rollback()
	if err := f.service.bindOne(steward, f.userID, modelID, member, "exact/model", true); err != nil {
		t.Fatalf("manual material before fresh discovery failures: %v", err)
	}
	beforeRevision := f.count(t, `SELECT binding_revision FROM charity_models WHERE id=?`, modelID)
	for _, test := range []struct {
		mode  int32
		code  string
		state string
	}{{1, "model_not_found", "succeeded"}, {2, "discovery_failed", "failed"}} {
		f.mode.Store(test.mode)
		beforeCalls := f.calls.Load()
		failureCtx, cancel := context.WithTimeout(steward, 10*time.Second)
		err := f.service.bindOne(failureCtx, f.userID, modelID, member, "exact/model", false)
		cancel()
		code, message := safeError(err)
		if err == nil || code != test.code || strings.Contains(message, "private diagnostic") {
			t.Fatalf("fresh discovery classification: code=%s message=%s err=%v", code, message, err)
		}
		if f.calls.Load() != beforeCalls+1 || f.count(t, `SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=?`, modelID) != 1 || f.count(t, `SELECT binding_revision FROM charity_models WHERE id=?`, modelID) != beforeRevision {
			t.Fatal("fresh discovery reused old material or changed prior bindings")
		}
		if err := f.store.DB().QueryRow(`SELECT r.state,r.authorization_use_id,r.authorization_use_revision_id,r.source_revision_id,r.credential_version_id
FROM discovery_source_states st JOIN discovery_runs r ON r.id=st.current_run_id WHERE st.source_revision_id=?`, approved.DiscoverySourceRevisionID).Scan(&runState, &runUse, &runApproval, &runSource, &runVersion); err != nil {
			t.Fatal(err)
		}
		if runState != test.state || runUse != approved.UseID || runApproval != approved.ApprovedRevisionID || runSource != approved.DiscoverySourceRevisionID || runVersion != approved.Grant.CredentialVersionID {
			t.Fatal("fresh failure was not the pinned use/source/version run")
		}
	}
	f.exec(t, `UPDATE users SET level=5 WHERE id=?`, f.userID)
	policy.Permissions["donations.discover"] = true
	writePolicy()
	f.mode.Store(6)
	successful := f.count(t, `SELECT COUNT(*) FROM discovery_runs WHERE state='succeeded'`)
	err = f.service.bindOne(steward, f.userID, modelID, member, "exact/model", false)
	if err == nil || f.count(t, `SELECT COUNT(*) FROM discovery_runs WHERE state='succeeded'`) != successful || f.count(t, `SELECT binding_revision FROM charity_models WHERE id=?`, modelID) != beforeRevision {
		t.Fatal("revoked discover grant published worker result", err)
	}
}
