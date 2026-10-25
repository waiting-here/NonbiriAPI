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
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestAgentDonationHonorsTemporaryBanExpiry(t *testing.T) {
	e, _, d := agentReviewFixture(t)
	level := int64(6)
	id := e.seedUser(t, "expired-ban-steward", &level, false)
	actor := agentplan.Actor{ID: id, Role: "level6"}
	ctx := context.Background()
	op := agentReviewOperation(t, e, d, "approve")
	now, err := e.service.nowUnix()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		until   any
		allowed bool
	}{{"permanent", nil, false}, {"active", now + 1, false}, {"expired", now, true}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, tc.until, id); err != nil {
				t.Fatal(err)
			}
			tx, err := e.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, readErr := e.service.AgentRead(ctx, tx, actor, "donations", json.RawMessage(`{}`), allowAgent)
			_, previewErr := e.service.AgentPreview(ctx, tx, actor, op, allowAgent)
			_, applyErr := e.service.AgentApply(ctx, tx, actor, op, allowAgent)
			for _, err := range []error{readErr, previewErr, applyErr} {
				if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrForbidden) {
					t.Fatal("ban authority", err)
				}
			}
		})
	}
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
	projected, err := e.service.GetAdmin(ctx, parseTestID(t, d.ID))
	if err != nil || projected.FirstApprovalOrigin != "manual" {
		t.Fatalf("prior approval origin changed: %s, err=%v", projected.FirstApprovalOrigin, err)
	}
}

func decodeAgentReadResult(t *testing.T, raw json.RawMessage, count int) []map[string]json.RawMessage {
	t.Helper()
	var result struct {
		Data  []map[string]json.RawMessage `json:"data"`
		Total string                       `json:"total"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != count || result.Total != strconv.Itoa(count) || bytes.Contains(raw, []byte(`"has_more"`)) {
		t.Fatalf("incomplete result: count=%d total=%s want=%d", len(result.Data), result.Total, count)
	}
	return result.Data
}

func TestAgentDonationCompleteReadsAndCatalogFilters(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	var owner int64
	if err := e.store.DB().QueryRow(`SELECT user_id FROM donations WHERE id=?`, d.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 102; i++ {
		result, err := tx.Exec(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at,terminal_at) VALUES(?,'deleted',1,'retained history',?,?,?)`, owner, e.clock.Load(), e.clock.Load(), e.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if _, err = tx.Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'pending',1,?,?)`, id, e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 105; i++ {
		model := fmt.Sprintf("model-%03d", i)
		if _, err = tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, d.Keys[0].ID, model, fmt.Sprintf("Display %03d", i), e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO donation_reviews(donation_id,reviewer_user_id,reviewer_role,action,note,created_at) VALUES(?,?,'admin','note_update','retained decision',?)`, d.ID, actor.ID, e.clock.Load()+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		tool  string
		count int
	}{{"donations", 103}, {"donation_models", 105}, {"review_history", 105}} {
		started := time.Now()
		raw, err := e.service.AgentRead(t.Context(), tx, actor, tc.tool, json.RawMessage(`{}`), allowAgent)
		if err != nil {
			t.Fatal(tc.tool, err)
		}
		items := decodeAgentReadResult(t, raw, tc.count)
		t.Logf("%s complete rows=%d bytes=%d elapsed=%s", tc.tool, len(items), len(raw), time.Since(started))
		if tc.tool == "donations" {
			var pending int
			for _, item := range items {
				if string(item["id"]) == `"`+d.ID+`"` {
					if len(item["signature"]) == 0 || len(item["uses"]) <= 2 {
						t.Fatal("pending detail missing from list")
					}
					pending++
				} else if len(item["signature"]) > 0 {
					t.Fatal("terminal donation has pending signature")
				}
			}
			if pending != 1 {
				t.Fatal("pending donation lost")
			}
			for _, item := range items {
				var uses []Use
				if err := json.Unmarshal(item["uses"], &uses); err != nil {
					t.Fatal(err)
				}
				for _, use := range uses {
					for _, revision := range []*UseRevision{use.Pending, use.Approved, use.Latest} {
						if revision != nil && (revision.DisplayHead != "" || revision.DisplayTail != "") {
							t.Fatal("credential hints in uses")
						}
					}
				}
				if bytes.Contains(item["keys"], []byte(`"display_head"`)) || bytes.Contains(item["keys"], []byte(`"display_tail"`)) {
					t.Fatal("credential hints in keys")
				}
			}
		} else {
			for _, item := range items {
				if string(item["donation_id"]) != `"`+d.ID+`"` {
					t.Fatal("catalog/history parent missing")
				}
			}
		}
	}
	for _, query := range []string{`{"query":"MODEL-103"}`, `{"query":"Display 103","source":"manual","donation_key_id":"` + d.Keys[0].ID + `"}`} {
		raw, err := e.service.AgentRead(t.Context(), tx, actor, "donation_models", json.RawMessage(query), allowAgent)
		if err != nil {
			t.Fatal(err)
		}
		items := decodeAgentReadResult(t, raw, 1)
		if string(items[0]["model"]) != `"model-103"` {
			t.Fatal("wrong model filter", string(raw))
		}
	}
	for _, query := range []string{`{"source":"automatic"}`, `{"query":"absent"}`, `{"donation_key_id":"9223372036854775807"}`} {
		raw, err := e.service.AgentRead(t.Context(), tx, actor, "donation_models", json.RawMessage(query), allowAgent)
		if err != nil {
			t.Fatal(err)
		}
		decodeAgentReadResult(t, raw, 0)
	}
	for tool, schema := range AgentReadSchemas() {
		var shape struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(schema, &shape); err != nil {
			t.Fatal(err)
		}
		if shape.Properties["page"] != nil || shape.Properties["page_size"] != nil || len(shape.Required) > 0 {
			t.Fatal("read schema retains UI steps", tool, string(schema))
		}
		for _, bad := range []string{`{"page":1}`, `{"page_size":100}`} {
			if _, err := e.service.AgentRead(t.Context(), tx, actor, tool, json.RawMessage(bad), allowAgent); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal("obsolete pagination accepted", tool, err)
			}
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = e.service.AgentRead(canceled, tx, actor, "donations", json.RawMessage(`{}`), allowAgent); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestAgentDonationCompleteChildrenKeepScopeAndRetention(t *testing.T) {
	e, actorID, _ := sourceReadFixture(t)
	actor := agentplan.Actor{ID: actorID, Role: "level5"}
	var visible string
	if err := e.store.DB().QueryRow(`SELECT donation_id FROM donation_keys WHERE canonical_base_url='https://tagged.example.test/v1'`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) SELECT id,'scope-model','scope-model',1,?,? FROM donation_keys`, e.clock.Load(), e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO donation_reviews(donation_id,reviewer_user_id,reviewer_role,action,note,created_at) SELECT id,?,'admin','note_update','scope decision',? FROM donations`, actorID, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read := func(tool, args string) json.RawMessage {
		t.Helper()
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		raw, err := e.service.AgentRead(t.Context(), tx, actor, tool, json.RawMessage(args), allowAgent)
		if err != nil {
			t.Fatal(tool, err)
		}
		return raw
	}
	for _, tc := range []struct {
		tool, args string
		count      int
	}{{"donations", `{}`, 1}, {"donation_models", `{}`, 3}, {"review_history", `{"decision":"note_update"}`, 1}} {
		raw := read(tc.tool, tc.args)
		items := decodeAgentReadResult(t, raw, tc.count)
		for _, item := range items {
			field := "donation_id"
			if tc.tool == "donations" {
				field = "id"
			}
			if string(item[field]) != `"`+visible+`"` {
				t.Fatal("scope leak", tc.tool, string(raw))
			}
		}
		if bytes.Contains(raw, []byte(`"reviewer_id":"`)) || bytes.Contains(raw, []byte(`"owner":{"`)) || bytes.Contains(raw, []byte(`"endpoint_key_id":"`)) {
			t.Fatal("identity redaction lost", tc.tool, string(raw))
		}
	}
	if _, err = e.store.DB().Exec(`UPDATE donations SET status='deleted',terminal_at=? WHERE id=?`, e.clock.Load()-terminalRetention-1, visible); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"donations", "donation_models", "review_history"} {
		decodeAgentReadResult(t, read(tool, `{}`), 0)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.DonationScope = authz.DonationScopeNone
	writeDonationPolicy(t, e, actorID, policy)
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, tool := range []string{"donations", "donation_models", "review_history"} {
		if _, err = e.service.AgentRead(t.Context(), tx, actor, tool, json.RawMessage(`{}`), allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
			t.Fatal("none scope admitted", tool, err)
		}
	}
}

func TestAgentIntelligenceKeepsCompleteCatalog(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 4101; i++ {
		model := fmt.Sprintf("complete-catalog-%04d", i)
		if _, err = tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, d.Keys[0].ID, model, model, e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
	raw, signature, err := e.service.AgentIntelligence(t.Context(), tx, parseTestID(t, d.ID))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Models []struct {
			Model string `json:"model"`
		} `json:"models"`
		Signature string `json:"signature"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 4101 || result.Models[4100].Model != "complete-catalog-4100" || signature == "" || result.Signature != signature || bytes.Contains(raw, []byte(`"models_truncated"`)) {
		t.Fatal("incomplete initial intelligence", len(result.Models), signature)
	}
	t.Logf("initial intelligence complete models=%d bytes=%d", len(result.Models), len(raw))
	for i := 4101; i < 5001; i++ {
		model := fmt.Sprintf("complete-catalog-%04d", i)
		if _, err = tx.Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, d.Keys[0].ID, model, model, e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
	if raw, signature, err = e.service.AgentIntelligence(t.Context(), tx, parseTestID(t, d.ID)); !agentplan.IsReadLimit(err) || raw != nil || signature == "" {
		t.Fatal("oversized initial intelligence did not fail completely with its version", len(raw), signature, err)
	}
	if raw, err = e.service.AgentRead(t.Context(), tx, actor, "donation_models", json.RawMessage(`{}`), allowAgent); !agentplan.IsReadLimit(err) || raw != nil {
		t.Fatal("oversized model list returned partial data", len(raw), err)
	}
	if _, err = tx.Exec(`UPDATE donation_key_manual_models SET display_name=? WHERE donation_key_id=?`, strings.Repeat("界", 512), d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	// This selection has only 1000 rows; its legal UTF-8 display names exceed
	// the byte budget independently of the item limit.
	if raw, err = e.service.AgentRead(t.Context(), tx, actor, "donation_models", json.RawMessage(`{"query":"complete-catalog-0"}`), allowAgent); !agentplan.IsReadLimit(err) || raw != nil {
		t.Fatal("oversized catalog bytes returned partial data", len(raw), err)
	}
	raw, err = e.service.AgentRead(t.Context(), tx, actor, "donation_models", json.RawMessage(`{"query":"complete-catalog-0000"}`), allowAgent)
	if err != nil {
		t.Fatal("narrow catalog retry failed", err)
	}
	decodeAgentReadResult(t, raw, 1)
}
