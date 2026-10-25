package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func allowAgent(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
func agentOp(t *testing.T, kind, target string, input any) agentplan.Operation {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return agentplan.Operation{Kind: kind, TargetID: target, Input: raw}
}
func TestAgentCharityModelAndBindingTransactions(t *testing.T) {
	e := newRoutingTestEnv(t)
	admin := e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	actor := agentplan.Actor{ID: admin, Role: "admin"}
	ctx := context.Background()
	_, key, _ := e.seedCandidate(t, owner, 'a', "upstream")
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	create := agentOp(t, "charity.model.create", "", testModelCreate())
	if _, err := e.service.AgentPreview(ctx, tx, actor, create, allowAgent); err != nil {
		t.Fatal(err)
	}
	body, err := e.service.AgentApply(ctx, tx, actor, create, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	var model AdminCharityModel
	if err = json.Unmarshal(body, &model); err != nil {
		t.Fatal(err)
	}
	add := agentOp(t, "charity.binding.add", model.ID, agentBindingInput{ExpectedBindingRevision: "0", Targets: []BindingTargetInput{targetForTest(key, "upstream", contract.NativeServiceChatCompletions)}})
	body, err = e.service.AgentApply(ctx, tx, actor, add, allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	var receipt BindingTargetsReceipt
	json.Unmarshal(body, &receipt)
	update := agentOp(t, "charity.binding.update", model.ID, agentBindingInput{ExpectedBindingRevision: "1", BindingID: receipt.BindingID, Targets: []BindingTargetInput{targetForTest(key, "other-upstream", contract.NativeServiceChatCompletions)}})
	if _, err = e.service.AgentApply(ctx, tx, actor, update, allowAgent); err != nil {
		t.Fatal(err)
	}
	if _, err = e.service.AgentApply(ctx, tx, actor, add, allowAgent); !errors.Is(err, ErrConflict) {
		t.Fatal("stale binding revision", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mid, _ := parsePositiveID(model.ID)
	snap, err := e.service.snapshot(ctx, mid, routingTestNow, false, map[contract.Type]struct{}{contract.TypeOpenAICompatible: {}}, contract.OperationChatCompletions)
	if err != nil || len(snap.candidates) != 1 || snap.candidates[0].UpstreamModelID != "other-upstream" {
		t.Fatal("routing invariant", snap, err)
	}
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	patch := agentOp(t, "charity.model.update", model.ID, map[string]any{"expected_revision": "1", "public_description": "updated", "discount": map[string]any{"start_at": nil}, "token_reserve_credits": nil})
	if _, err = e.service.AgentApply(ctx, tx, actor, patch, allowAgent); err != nil {
		t.Fatal("model fields", err)
	}
	deny := func(context.Context, *sql.Tx, agentplan.Actor, string) error { return authz.ErrPermissionDenied }
	if _, err = e.service.AgentApply(ctx, tx, actor, agentOp(t, "charity.binding.delete", model.ID, agentBindingInput{ExpectedBindingRevision: "2", BindingID: receipt.BindingID}), deny); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("late permission", err)
	}
	tx.Rollback()
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	defer tx.Rollback()
	if _, err = e.service.AgentRead(ctx, tx, actor, "charity_models", json.RawMessage(`{"model_id":"`+model.ID+`"}`), allowAgent); err != nil {
		t.Fatal(err)
	}
	actor.Automatic = true
	if _, err = e.service.AgentApply(ctx, tx, actor, create, allowAgent); !errors.Is(err, ErrForbidden) {
		t.Fatal("automatic created model", err)
	}
}

func TestAgentBindingLateSourceAndTargetScope(t *testing.T) {
	e := newRoutingTestEnv(t)
	admin := e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	five := int64(5)
	steward := e.seedUser(t, false, &five)
	actor := agentplan.Actor{ID: steward, Role: "level5"}
	model := e.createModel(t, 'b')
	_, key, physical := e.seedCandidate(t, owner, 'c', "upstream")
	ctx := context.Background()
	op := agentOp(t, "charity.binding.add", model.ID, agentBindingInput{ExpectedBindingRevision: "0", Targets: []BindingTargetInput{targetForTest(key, "upstream", contract.NativeServiceChatCompletions)}})
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); !errors.Is(err, ErrNotFound) {
		t.Fatal("nonmainstream model", err)
	}
	tx.Rollback()
	actor = agentplan.Actor{ID: admin, Role: "admin"}
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := e.service.AgentPreview(ctx, tx, actor, op, allowAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM donation_key_memberships WHERE endpoint_key_id=?`, physical); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent); err == nil {
		t.Fatal("deleted source accepted")
	}
	tx.Rollback()
}

type agentOwnerAuth struct{}

func (agentOwnerAuth) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	var n int
	return tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? AND is_banned=0`, id).Scan(&n)
}
func TestAgentApprovalThenInvalidBindingRollsBackWholePlan(t *testing.T) {
	e := newRoutingTestEnv(t)
	ctx := context.Background()
	admin := e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'd')
	review, err := secret.NewDonationReview(e.vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.Initialize(ctx, e.store.DB()); err != nil {
		t.Fatal(err)
	}
	ds, err := donation.New(donation.Config{Store: e.store, Review: review, OwnerAuth: agentOwnerAuth{}, RoleAuth: e.auth, Now: func() time.Time { return time.Unix(routingTestNow, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE site_config SET value='1' WHERE key='donation_accept_enabled'`); err != nil {
		t.Fatal(err)
	}
	endpoint, err := testseed.InsertEndpointTx(ctx, tx, testseed.EndpointInput{OwnerUserID: owner, Type: contract.TypeOpenAICompatible, BaseURL: "https://agent.test/v1", At: routingTestNow})
	if err != nil {
		t.Fatal(err)
	}
	identity := make([]byte, 16)
	identity[15] = 99
	c, err := secret.NewGenerationTwoEndpointKeyContext(identity)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := e.vault.SealForGenerationTwoContext([]byte("private-agent-test-key"), c)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.Exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)VALUES(?,'https://agent.test/v1','openai-compatible',?,?)`, identity, envelope, routingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secretID, _ := result.LastInsertId()
	fingerprint := make([]byte, 32)
	fingerprint[31] = 99
	key, err := testseed.InsertEndpointKeyTx(ctx, tx, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Fingerprint: fingerprint, DisplayHead: "head", DisplayTail: "tail", At: routingTestNow})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ds.CreateInTransaction(ctx, tx, owner, donation.CreateInput{Description: "Review atomic plan", OwnershipAuthorized: true, Keys: []donation.CreateKeyInput{{EndpointKeyID: key.KeyID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	id, _ := parsePositiveID(d.ID)
	actor := agentplan.Actor{ID: admin, Role: "admin", Automatic: true}
	tx, _ = e.store.DB().BeginTx(ctx, nil)
	defer tx.Rollback()
	_, sig, err := ds.AgentIntelligence(ctx, tx, id)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := ds.AgentReviewOperations(ctx, tx, id, sig, "approve", "Reviewed")
	if err != nil {
		t.Fatal(err)
	}

	member, _ := parsePositiveID(d.Keys[0].ID)
	planned := agentOp(t, "charity.binding.add", model.ID, agentBindingInput{ExpectedBindingRevision: "0", Targets: []BindingTargetInput{targetForTest(member, "reviewed-model", contract.NativeServiceChatCompletions)}})
	if _, err = e.service.AgentPreview(ctx, tx, actor, planned, allowAgent); err != nil {
		t.Fatal("ordered approval/binding preview rejected", err)
	}
	if _, err = ds.AgentApply(ctx, tx, actor, ops[0], allowAgent); err != nil {
		t.Fatal("approval", err)
	}
	invalid := agentOp(t, "charity.binding.add", model.ID, agentBindingInput{ExpectedBindingRevision: "0", Targets: []BindingTargetInput{targetForTest(999999, "missing", contract.NativeServiceChatCompletions)}})
	if _, err = e.service.AgentApply(ctx, tx, actor, invalid, allowAgent); err == nil {
		t.Fatal("invalid binding succeeded")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var status string
	var pending, audits int
	e.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, id).Scan(&status)
	e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND u.current_pending_revision_id IS NOT NULL`, id).Scan(&pending)
	e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=?`, id).Scan(&audits)
	if status != "pending" || pending == 0 || audits != 0 {
		t.Fatal(fmt.Sprintf("partial plan committed: %s pending=%d audits=%d", status, pending, audits))
	}
}

func TestAgentSchemasPreserveUpstreamNamesAndStrictJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(AgentOperationSchemas()["charity.binding.add"], &schema); err != nil {
		t.Fatal(err)
	}
	target := schema["properties"].(map[string]any)["targets"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if _, numeric := target["upstream_model_id"].(map[string]any)["pattern"]; numeric {
		t.Fatal("upstream model names restricted to numeric IDs")
	}
	if _, err := parseAgentOperation(agentplan.Operation{Kind: "charity.binding.delete", TargetID: "1", Input: json.RawMessage(`{"expected_binding_revision":"1","expected_binding_revision":"2","binding_id":"1"}`)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("duplicate JSON fields accepted", err)
	}
}
