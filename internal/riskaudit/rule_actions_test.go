package riskaudit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type ruleUserRoutes struct{ paths map[string]bool }

func (r *ruleUserRoutes) RegisterUserRoute(method, path string, _ resources.AuthorizedUserHandler) error {
	r.paths[method+" "+path] = true
	return nil
}

func actionRule(name string) Rule {
	return Rule{Name: name, Status: "suspected", Enabled: true, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "Example/"}}}
}

func TestAutoBanBindingProtectsWholeRuleAndSharesRevision(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	admin := Actor{Admin: true, UserID: f.admin}
	steward := Actor{UserID: f.user(6)}
	ordinary, err := f.repository.PutRule(ctx, steward, actionRule("ordinary"), true)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.AutoBan != nil {
		t.Fatal("ordinary rule gained a binding")
	}
	bound := actionRule("bound")
	duration := int64(3600)
	bound.AutoBan = &AutoBan{Enabled: false, DurationSeconds: &duration}
	bound, err = f.repository.PutRule(ctx, admin, bound, true)
	if err != nil || bound.AutoBan == nil || bound.Revision != 1 || bound.AutoBan.Enabled {
		t.Fatalf("create %+v %v", bound, err)
	}
	bound.Conditions[0].Value = "Changed/"
	if _, err := f.repository.PutRule(ctx, steward, bound, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("steward changed disabled binding: %v", err)
	}
	if err := f.repository.DeleteRule(ctx, steward, bound.ID, bound.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("steward deleted disabled binding: %v", err)
	}
	if _, err := f.repository.PutRuleWithAction(ctx, steward, ordinary, false, ActionMutation{Present: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("steward unbound action: %v", err)
	}
	updated, err := f.repository.PutRuleWithAction(ctx, admin, bound, false, ActionMutation{})
	if err != nil || updated.Revision != 2 || updated.AutoBan == nil || updated.AutoBan.Enabled {
		t.Fatalf("preserve binding %+v %v", updated, err)
	}
	var actionRevision int64
	if err := f.store.DB().QueryRow(`SELECT revision FROM client_rule_auto_bans WHERE rule_id=?`, bound.ID).Scan(&actionRevision); err != nil || actionRevision != updated.Revision {
		t.Fatalf("binding revision %d %v", actionRevision, err)
	}
	if _, err := f.repository.PutRule(ctx, admin, bound, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	unbound, err := f.repository.PutRuleWithAction(ctx, admin, updated, false, ActionMutation{Present: true})
	if err != nil || unbound.AutoBan != nil || unbound.Revision != 3 {
		t.Fatalf("explicit unbind %+v %v", unbound, err)
	}
	unbound.Name = "steward may edit"
	if _, err := f.repository.PutRule(ctx, steward, unbound, false); err != nil {
		t.Fatalf("steward edit after unbind: %v", err)
	}
	if err := f.repository.DeleteRule(ctx, steward, bound.ID, 4); err != nil {
		t.Fatalf("steward delete after unbind: %v", err)
	}
}

func TestAutoBanCapacityAndActionParsing(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	admin := Actor{Admin: true, UserID: f.admin}
	for i := 0; i < MaxAutoBanRules; i++ {
		rule := actionRule("bounded")
		rule.AutoBan = &AutoBan{Enabled: true}
		if _, err := f.repository.PutRule(ctx, admin, rule, true); err != nil {
			t.Fatalf("binding %d: %v", i, err)
		}
	}
	rule := actionRule("over capacity")
	rule.AutoBan = &AutoBan{Enabled: true}
	if _, err := f.repository.PutRule(ctx, admin, rule, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("101st binding: %v", err)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM risk_client_rules`).Scan(&count); err != nil || count != MaxAutoBanRules {
		t.Fatalf("partial creation %d %v", count, err)
	}
	for _, raw := range []string{`{"enabled":true}`, `{"enabled":true,"duration_seconds":0}`, `{"enabled":true,"duration_seconds":315360001}`, `{"enabled":true,"duration_seconds":1.2}`, `{"enabled":true,"duration_seconds":null,"extra":1}`} {
		if _, err := parseAction([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`null`, `{"enabled":false,"duration_seconds":null}`, `{"enabled":true,"duration_seconds":315360000}`} {
		if _, err := parseAction([]byte(raw)); err != nil {
			t.Fatalf("rejected %s: %v", raw, err)
		}
	}
}

func TestAutoBanEnabledRequiresBoolean(t *testing.T) {
	if _, err := parseAction([]byte(`{"enabled":null,"duration_seconds":null}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("null enabled accepted: %v", err)
	}
}

func TestRuleBanReceiptIsExactAndAdminOnly(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	admin := Actor{Admin: true, UserID: f.admin}
	steward := Actor{UserID: f.user(6)}
	user := f.user(1)
	rule, err := f.repository.PutRule(ctx, admin, actionRule("evidence"), true)
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO client_rule_ban_receipts(request_id,user_id,rules_json,banned_until,created_at,expires_at) VALUES(?,?,?,NULL,?,?)`, requestID, user, `[{"id":"`+rule.ID+`","revision":1}]`, f.now, f.now+7776000)
	receipt, err := f.repository.RuleBanReceipt(ctx, admin, requestID)
	if err != nil || receipt.UserID != user || receipt.Source != "client_rule" || len(receipt.Rules) != 1 || receipt.Rules[0].ID != rule.ID || receipt.BannedUntil != nil {
		t.Fatalf("admin receipt %+v %v", receipt, err)
	}
	if _, err := f.repository.RuleBanReceipt(ctx, steward, requestID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("steward read receipt: %v", err)
	}
	if _, err := f.repository.RuleBanReceipt(ctx, admin, "malformed"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed ID: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/abuse-audit/client-rule-ban-receipts/"+requestID, nil)
	req.SetPathValue("id", requestID)
	w := httptest.NewRecorder()
	serve(f.repository, "rule_ban_receipt", admin, w, req)
	if w.Code != 200 {
		t.Fatalf("admin endpoint %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	serve(f.repository, "rule_ban_receipt", steward, w, req)
	if w.Code != 403 {
		t.Fatalf("steward endpoint %d", w.Code)
	}
	userRoutes := &ruleUserRoutes{paths: make(map[string]bool)}
	if err := RegisterStewardRoutes(userRoutes, f.repository); err != nil {
		t.Fatal(err)
	}
	if userRoutes.paths["GET /api/steward/abuse-audit/client-rule-ban-receipts/{id}"] {
		t.Fatal("admin receipt route exposed to steward")
	}
	f.now += 7776000
	f.exec(`UPDATE sessions SET last_seen_at=?,expires_at=?,absolute_expires_at=? WHERE user_id=?`, f.now, f.now+600, f.now+1200, f.admin)
	if _, err := f.repository.RuleBanReceipt(ctx, admin, requestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired receipt: %v", err)
	}
}
