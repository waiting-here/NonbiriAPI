package donation

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"net/http"
	"testing"
)

func TestUnifiedStandardDonationRequiresEveryActiveSource(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "unified-reviewer", &five, false)
	owner := e.seedUser(t, "unified-donor", nil, false)
	_, a := e.seedEndpointKey(t, owner, 'A')
	_, b := e.seedEndpointKey(t, owner, 'B')
	d := e.createDonation(t, owner, a, b)
	id := parseTestID(t, d.ID)
	ka, kb := parseTestID(t, d.Keys[0].ID), parseTestID(t, d.Keys[1].ID)
	main := controlModel(t, e, "scope-main", true)
	other := controlModel(t, e, "scope-other", true)
	hidden := controlModel(t, e, "scope-nonmain", false)
	e.seedBinding(t, main, ka, "model-a", 0)
	browse := authz.WithStewardPermission(context.Background(), "donations.browse")
	review := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: ka, Enabled: true}, {DonationKeyID: kb, Enabled: true}}}
	mutation := donationMutation(t, 'R', http.MethodPost, routeStewardReview, []int64{id}, review)
	mark := donationMutation(t, 'H', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	deny := func() {
		t.Helper()
		if _, err := e.service.GetSteward(browse, actor, id); !errors.Is(err, ErrNotFound) {
			t.Fatal("partial donation detail", err)
		}
		if _, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, review); !errors.Is(err, ErrNotFound) {
			t.Fatal("pending approval bypass", err)
		}
		if _, err := e.service.ProcessSteward(context.Background(), actor, id, mark, 1); !errors.Is(err, ErrNotFound) {
			t.Fatal("handling bypass", err)
		}
		if _, err := e.service.KeysStewardPage(charityscope.WithModel(browse, main), actor, id, pagination.Default()); !errors.Is(err, ErrNotFound) {
			t.Fatal("selected model bypass", err)
		}
	}
	deny()
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0,failure_disabled=1 WHERE id=?`, kb); err != nil {
		t.Fatal(err)
	}
	deny()
	e.seedBinding(t, main, kb, "model-b", 1)
	for _, ctx := range []context.Context{browse, charityscope.WithModel(browse, other)} {
		keys, err := e.service.KeysStewardPage(ctx, actor, id, pagination.Default())
		if err != nil || len(keys.Data) != 2 {
			t.Fatal("qualified pending whole donation", keys, err)
		}
	}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := charityscope.Scope{ActorID: actor, ActorLevel: 5, ModelID: other, Trainee: true, DonationScope: authz.DonationScopeStandard}
	if err := scope.RequireKey(context.Background(), tx, id, ka, donationTestNow, true); err != nil {
		t.Fatal("qualified custom add denied", err)
	}
	tx.Rollback()
	if _, err := e.service.KeysStewardPage(charityscope.WithModel(browse, hidden), actor, id, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("nonmain target widened", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET failure_disabled=0 WHERE id=?`, kb); err != nil {
		t.Fatal(err)
	}
	first, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, review)
	if err != nil {
		t.Fatal("qualified pending approval", err)
	}
	again, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, review)
	if err != nil || !again.Replayed || string(first.Body) != string(again.Body) {
		t.Fatal("review snapshot replay", err)
	}
	if _, err := e.service.ProcessSteward(context.Background(), actor, id, mark, 1); err != nil {
		t.Fatal("qualified handling", err)
	}
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["donations.review"] = false
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.service.ReviewSteward(context.Background(), actor, id, mutation, review); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("action revoke replay", err)
	}
	p.Permissions["donations.review"] = true
	writeDonationPolicy(t, e, actor, p)
	if _, err := e.store.DB().Exec(`DELETE FROM charity_model_bindings WHERE charity_model_id=? AND id IN (SELECT binding_id FROM charity_binding_targets WHERE donation_key_id=?)`, main, kb); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.GetSteward(browse, actor, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("lost qualification detail", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, donationTestNow, kb); err != nil {
		t.Fatal(err)
	}
	future := int64(donationTestNow + 3600)
	futurePtr := &future
	restore := KeyManagementInput{ExpectedRevision: 2, ExpiresAt: &futurePtr}
	restoreMutation := donationMutation(t, 'X', http.MethodPatch, routeStewardKey, []int64{id, kb}, restore)
	if _, err := e.service.ManageKeySession(authz.WithStewardPermission(context.Background(), "donations.manage_keys"), actor, id, kb, restoreMutation, restore); !errors.Is(err, ErrConflict) {
		t.Fatal("expired unqualified source restored", err)
	}
	var savedExpiry int64
	if err := e.store.DB().QueryRow(`SELECT expires_at FROM donation_keys WHERE id=?`, kb).Scan(&savedExpiry); err != nil || savedExpiry != donationTestNow {
		t.Fatal("expiry changed on rejected restore", savedExpiry, err)
	}
	if _, err := e.service.GetSteward(browse, actor, id); err != nil {
		t.Fatal("expired source still counted", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, donationTestNow, ka); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.GetSteward(browse, actor, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("zero active sources qualified", err)
	}
}

func qualifyStandardDonation(t *testing.T, e *donationTestEnv, id string) {
	t.Helper()
	model := controlModel(t, e, "qualified-"+id, true)
	rows, err := e.store.DB().Query(`SELECT id FROM donation_keys WHERE donation_id=? ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var key int64
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, key)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for i, key := range ids {
		e.seedBinding(t, model, key, "qualified-model", i)
	}
}

func TestAutomationReviewUsesUnifiedDonationScope(t *testing.T) {
	e := newDonationTestEnv(t)
	e.service.roleAuth = unifiedAutomationAuth{e.auth}
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "automation-scope-member", &five, false)
	_, physical := e.seedEndpointKey(t, actor, 'O')
	d := e.createDonation(t, actor, physical)
	id := parseTestID(t, d.ID)
	input := ReviewInput{Decision: "approve", ExpectedRevision: 1, KeySettings: []KeySetting{{DonationKeyID: parseTestID(t, d.Keys[0].ID), Enabled: true}}}
	tx, err := e.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = e.service.ApproveOwnNewInTransaction(authz.WithStewardPermission(context.Background(), "donations.manage_keys"), tx, actor, id, input)
	tx.Rollback()
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("automation approved out-of-scope custom source", err)
	}
	var status string
	if err := e.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, id).Scan(&status); err != nil || status != "pending" {
		t.Fatal(status, err)
	}
}

// This fixture models the live route authorizer accepting an authorized level 5.
// The older generic fixture intentionally permits only the original level 6 role.
type unifiedAutomationAuth struct{ *donationTestAuth }

func (a unifiedAutomationAuth) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.AuthorizeTraineeMutation(ctx, tx, id)
}
