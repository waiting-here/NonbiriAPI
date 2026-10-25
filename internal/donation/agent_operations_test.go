package donation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func allowAgent(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
func agentReviewFixture(t *testing.T) (*donationTestEnv, agentplan.Actor, Donation) {
	t.Helper()
	e := newDonationTestEnv(t)
	admin := e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "agent-owner", nil, false)
	_, a := e.seedEndpointKey(t, owner, 'a')
	_, b := e.seedEndpointKey(t, owner, 'b')
	d := e.createDonation(t, owner, a, b)
	return e, agentplan.Actor{ID: admin, Role: "admin"}, d
}
func agentReviewOperation(t *testing.T, e *donationTestEnv, d Donation, decision string) agentplan.Operation {
	t.Helper()
	ctx := context.Background()
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, sig, err := e.service.AgentIntelligence(ctx, tx, parseTestID(t, d.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "credential-a") || strings.Contains(string(raw), `"display_head":"head"`) || strings.Contains(string(raw), `"owner":{"`) {
		t.Fatal("credential/identity leaked", string(raw))
	}
	ops, err := e.service.AgentReviewOperations(ctx, tx, parseTestID(t, d.ID), sig, decision, "Reviewed")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	return ops[0]
}
func pendingCount(t *testing.T, e *donationTestEnv, id string) int {
	t.Helper()
	var n int
	if err := e.store.DB().QueryRow(`SELECT count(*) FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND u.current_pending_revision_id IS NOT NULL`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestAgentDonationReviewAtomicVersionAndRevocation(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	ctx := context.Background()
	op := agentReviewOperation(t, e, d, "approve")
	count := pendingCount(t, e, d.ID)
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentPreview(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	var inside int
	tx.QueryRow(`SELECT count(*) FROM donation_uses WHERE current_approved_revision_id IS NOT NULL`).Scan(&inside)
	if inside != 0 {
		t.Fatal("preview mutated")
	}
	deny := func(context.Context, *sql.Tx, agentplan.Actor, string) error { return authz.ErrPermissionDenied }
	if _, err := e.service.AgentApply(ctx, tx, actor, op, deny); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("late revoke", err)
	}
	tx.Rollback()
	if pendingCount(t, e, d.ID) != count {
		t.Fatal("denied operation changed state")
	}
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal("approve", err)
	}
	bad := agentplan.Operation{Kind: "donation.handle", TargetID: d.ID, Input: json.RawMessage(`{"expected_revision":"99"}`)}
	if _, err := e.service.AgentApply(ctx, tx, actor, bad, allowAgent); !errors.Is(err, ErrConflict) {
		t.Fatal("late failure", err)
	}
	tx.Rollback()
	if pendingCount(t, e, d.ID) != count {
		t.Fatal("outer rollback retained approvals")
	}
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE donation_uses SET revision=revision+1 WHERE id=(SELECT MIN(id) FROM donation_uses)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, ErrConflict) {
		t.Fatal("changed pending snapshot", err)
	}
	tx.Rollback()
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	handle := agentplan.Operation{Kind: "donation.handle", TargetID: d.ID, Input: json.RawMessage(`{"expected_revision":"1"}`)}
	if _, err := e.service.AgentApply(ctx, tx, actor, handle, allowAgent); err != nil {
		t.Fatal("second same tx operation", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if pendingCount(t, e, d.ID) != 0 {
		t.Fatal("pending remained")
	}
	var audits int
	e.store.DB().QueryRow(`SELECT count(*) FROM donation_reviews WHERE donation_id=? AND action='approve'`, d.ID).Scan(&audits)
	if audits != 1 {
		t.Fatal("whole review audit", audits)
	}
}
func TestAgentDonationScopeAndPrivateProjection(t *testing.T) {
	e, _, d := agentReviewFixture(t)
	five := int64(5)
	id := e.seedUser(t, "agent-five", &five, false)
	actor := agentplan.Actor{ID: id, Role: "level5"}
	ctx := context.Background()
	op := agentReviewOperation(t, e, d, "approve")
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, ErrNotFound) {
		t.Fatal("custom source admitted", err)
	}
	tx.Rollback()
	qualifyStandardDonation(t, e, d.ID)
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	body, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"owner":{"`)) || bytes.Contains(body, []byte(`"endpoint_key_id":"`)) {
		t.Fatal("private actor projection", string(body))
	}
	tx.Rollback()

	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["donations.review"] = false
	writeDonationPolicy(t, e, id, policy)
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err = e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("live grant revoke bypass", err)
	}
	tx.Rollback()
	policy.Permissions["donations.review"] = true
	policy.DonationScope = authz.DonationScopeNone
	writeDonationPolicy(t, e, id, policy)
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err = e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("none bypass", err)
	}
	tx.Rollback()
	actor.Automatic = true
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err = e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, ErrForbidden) {
		t.Fatal("automatic nonadmin", err)
	}
	tx.Rollback()
}
func TestPendingReviewHTTPCompleteSetRollbackAndReplay(t *testing.T) {
	e, _, d := agentReviewFixture(t)
	ctx := context.Background()
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	snap, _, err := agentPendingTx(ctx, tx, parseTestID(t, d.ID))
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	input := PendingReviewInput{ExpectedRevision: d.Revision, Decision: "reject", Reason: "Please correct submission", Uses: []PendingReviewUse{}}
	for _, v := range snap.Pending {
		input.Uses = append(input.Uses, v.PendingReviewUse)
	}
	mux := http.NewServeMux()
	if err = RegisterAdminRoutes(recurringHTTPRegistrar{mux: mux}, e.service); err != nil {
		t.Fatal(err)
	}
	call := func(in PendingReviewInput, key string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(in)
		req := httptest.NewRequest(http.MethodPost, "/admin/api/donations/"+d.ID+"/pending-review", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", strings.Repeat(key, 22))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	bulk := input
	bulk.Uses = make([]PendingReviewUse, 100)
	for i := range bulk.Uses {
		bulk.Uses[i] = PendingReviewUse{UseID: fmt.Sprint(10000 + i), ExpectedRevision: "1"}
	}
	if w := call(bulk, "b"); w.Code != http.StatusConflict {
		t.Fatal("valid bulk JSON rejected by ordinary field limit", w.Code, w.Body.String())
	}
	partial := input
	partial.Uses = append([]PendingReviewUse{}, input.Uses[:len(input.Uses)-1]...)
	if w := call(partial, "p"); w.Code != http.StatusConflict {
		t.Fatal("partial set", w.Code, w.Body.String())
	}
	if pendingCount(t, e, d.ID) != len(snap.Pending) {
		t.Fatal("partial committed")
	}
	input.Uses[len(input.Uses)-1].ExpectedRevision = "99"
	if w := call(input, "s"); w.Code != http.StatusConflict {
		t.Fatal("stale member", w.Code, w.Body.String())
	}
	input.Uses[len(input.Uses)-1] = snap.Pending[len(snap.Pending)-1].PendingReviewUse
	first := call(input, "r")
	if first.Code != http.StatusOK {
		t.Fatal(first.Code, first.Body.String())
	}
	replay := call(input, "r")
	if replay.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatal("receipt changed", replay.Code, replay.Body.String())
	}
	if pendingCount(t, e, d.ID) != 0 {
		t.Fatal("whole rejection left pending")
	}
}
func TestAgentDonationReadAndSchemas(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	ctx := context.Background()
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	defer tx.Rollback()
	for _, tool := range []string{"donations", "donation_models", "review_history"} {
		raw, _ := json.Marshal(map[string]any{"donation_id": d.ID})
		body, err := e.service.AgentRead(ctx, tx, actor, tool, raw, allowAgent)
		if err != nil {
			t.Fatal(tool, err)
		}
		if tool == "donations" && (!bytes.Contains(body, []byte(`"signature"`)) || !bytes.Contains(body, []byte(`"uses"`))) {
			t.Fatal("chat cannot construct pinned review", string(body))
		}
	}
	for name, raw := range AgentOperationSchemas() {
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil || v["type"] != "object" {
			t.Fatal(name, err)
		}
	}
}

func TestAgentPendingRejectionPreservesApprovedVersionsAndSettings(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	ctx := context.Background()
	op := agentReviewOperation(t, e, d, "approve")
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var owner int64
	e.store.DB().QueryRow(`SELECT user_id FROM donations WHERE id=?`, d.ID).Scan(&owner)
	uses := ownerUsesForTest(t, e, owner, parseTestID(t, d.ID))
	stageUseForTest(t, e, uses[0])
	var approved int64
	e.store.DB().QueryRow(`SELECT current_approved_revision_id FROM donation_uses WHERE id=?`, uses[0].ID).Scan(&approved)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,token_reserve=77 WHERE id=?`, d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	op = agentReviewOperation(t, e, d, "reject")
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	raw, err := e.service.AgentRead(ctx, tx, actor, "review_history", json.RawMessage(`{"donation_id":"`+d.ID+`"}`), allowAgent)
	if err != nil || !bytes.Contains(raw, []byte(`"kind":"service"`)) {
		t.Fatal("service history missing", string(raw), err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var status string
	var after, reserve int64
	var enabled bool
	e.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, d.ID).Scan(&status)
	e.store.DB().QueryRow(`SELECT current_approved_revision_id FROM donation_uses WHERE id=?`, uses[0].ID).Scan(&after)
	e.store.DB().QueryRow(`SELECT enabled,token_reserve FROM donation_keys WHERE id=?`, d.Keys[0].ID).Scan(&enabled, &reserve)
	if status != "approved" || after != approved || enabled || reserve != 77 {
		t.Fatal("prior approval or settings changed", status, after, enabled, reserve)
	}
}
