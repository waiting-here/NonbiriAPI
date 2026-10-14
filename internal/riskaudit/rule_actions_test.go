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

func TestAutoBanBindingManagementSharesRevision(t *testing.T) {
	for _, role := range []string{"admin", "level6"} {
		t.Run(role, func(t *testing.T) {
			f := newAuditFixture(t)
			ctx := context.Background()
			actor := Actor{Admin: true, UserID: f.admin}
			if role == "level6" {
				actor = Actor{UserID: f.user(6)}
			}
			rule := actionRule("bound")
			duration := int64(3600)
			rule.AutoBan = &AutoBan{Enabled: false, DurationSeconds: &duration}
			created, err := f.repository.PutRule(ctx, actor, rule, true)
			if err != nil || created.AutoBan == nil || created.AutoBan.Enabled || created.Revision != 1 {
				t.Fatalf("create %+v %v", created, err)
			}
			// Either manager may maintain a rule originally created by the other.
			editor := Actor{UserID: f.user(6)}
			if role == "level6" {
				editor = Actor{Admin: true, UserID: f.admin}
			}
			created.Conditions[0].Value = "Changed/"
			updated, err := f.repository.PutRuleWithAction(ctx, editor, created, false, ActionMutation{})
			if err != nil || updated.AutoBan == nil || updated.AutoBan.Enabled || updated.Revision != 2 || *updated.AutoBan.DurationSeconds != duration {
				t.Fatalf("preserve binding %+v %v", updated, err)
			}
			if _, err := f.repository.PutRule(ctx, actor, created, false); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale update: %v", err)
			}
			updated.AutoBan = &AutoBan{Enabled: true}
			updated, err = f.repository.PutRule(ctx, actor, updated, false)
			if err != nil || updated.Revision != 3 || updated.AutoBan == nil || !updated.AutoBan.Enabled || updated.AutoBan.DurationSeconds != nil {
				t.Fatalf("enable permanent binding %+v %v", updated, err)
			}
			var actionRevision, actionActor, ruleActor int64
			var actorRole string
			if err := f.store.DB().QueryRow("SELECT b.revision,b.actor_user_id,r.updated_by_user_id,r.updated_by_role FROM client_rule_auto_bans b JOIN risk_client_rules r ON r.id=b.rule_id WHERE r.id=?", updated.ID).Scan(&actionRevision, &actionActor, &ruleActor, &actorRole); err != nil || actionRevision != updated.Revision || actionActor != actor.UserID || ruleActor != actor.UserID || actorRole != role {
				t.Fatalf("binding audit: %d %d %d %s %v", actionRevision, actionActor, ruleActor, actorRole, err)
			}
			unbound, err := f.repository.PutRuleWithAction(ctx, actor, updated, false, ActionMutation{Present: true})
			if err != nil || unbound.AutoBan != nil || unbound.Revision != 4 {
				t.Fatalf("explicit unbind %+v %v", unbound, err)
			}
			unbound.AutoBan = &AutoBan{Enabled: false, DurationSeconds: &duration}
			rebound, err := f.repository.PutRule(ctx, actor, unbound, false)
			if err != nil || rebound.Revision != 5 || rebound.AutoBan == nil || rebound.AutoBan.Enabled {
				t.Fatalf("rebind disabled %+v %v", rebound, err)
			}
			if err := f.repository.DeleteRule(ctx, actor, rebound.ID, 4); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale deletion: %v", err)
			}
			if err := f.repository.DeleteRule(ctx, actor, rebound.ID, rebound.Revision); err != nil {
				t.Fatalf("delete bound rule: %v", err)
			}
			var rules, bindings int
			if err := f.store.DB().QueryRow("SELECT (SELECT COUNT(*) FROM risk_client_rules),(SELECT COUNT(*) FROM client_rule_auto_bans)").Scan(&rules, &bindings); err != nil || rules != 0 || bindings != 0 {
				t.Fatalf("delete residue: %d %d %v", rules, bindings, err)
			}
		})
	}
}

func TestAutoBanCapacityAndActionParsing(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{UserID: f.user(6)}
	for i := 0; i < MaxAutoBanRules; i++ {
		rule := actionRule("bounded")
		rule.AutoBan = &AutoBan{Enabled: true}
		if _, err := f.repository.PutRule(ctx, actor, rule, true); err != nil {
			t.Fatalf("binding %d: %v", i, err)
		}
	}
	rule := actionRule("over capacity")
	rule.AutoBan = &AutoBan{Enabled: true}
	if _, err := f.repository.PutRule(ctx, actor, rule, true); !errors.Is(err, ErrConflict) {
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

func TestAutoBanBindingRejectsLostAuthorityWithoutWrites(t *testing.T) {
	for _, state := range []string{"ordinary", "level5", "demoted", "banned", "revoked", "replaced"} {
		t.Run(state, func(t *testing.T) {
			f := newAuditFixture(t)
			ctx := context.Background()
			actor := Actor{UserID: f.user(6)}
			admin := Actor{Admin: true, UserID: f.admin}
			rule := actionRule("existing")
			rule.AutoBan = &AutoBan{Enabled: true}
			existing, err := f.repository.PutRule(ctx, admin, rule, true)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "ordinary":
				actor = Actor{UserID: f.user(1)}
			case "level5":
				actor = Actor{UserID: f.user(5)}
			case "demoted":
				f.exec("UPDATE users SET level=5 WHERE id=?", actor.UserID)
			case "banned":
				f.exec("UPDATE users SET is_banned=1 WHERE id=?", actor.UserID)
			case "revoked":
				f.exec("DELETE FROM sessions WHERE user_id=?", actor.UserID)
			case "replaced":
				f.exec("UPDATE sessions SET cred_gen='replaced' WHERE user_id=?", actor.UserID)
			}
			if _, err := f.repository.PutRule(ctx, actor, rule, true); !errors.Is(err, ErrForbidden) {
				t.Fatalf("create allowed: %v", err)
			}
			existing.Name = "unauthorized edit"
			if _, err := f.repository.PutRule(ctx, actor, existing, false); !errors.Is(err, ErrForbidden) {
				t.Fatalf("edit allowed: %v", err)
			}
			if _, err := f.repository.PutRuleWithAction(ctx, actor, existing, false, ActionMutation{Present: true}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("unbind allowed: %v", err)
			}
			if err := f.repository.DeleteRule(ctx, actor, existing.ID, existing.Revision); !errors.Is(err, ErrForbidden) {
				t.Fatalf("delete allowed: %v", err)
			}
			rules, err := f.repository.Rules(ctx, admin)
			if err != nil || len(rules) != 1 || rules[0].Name != "existing" || rules[0].Revision != 1 || rules[0].AutoBan == nil || !rules[0].AutoBan.Enabled {
				t.Fatalf("denial wrote data: %+v %v", rules, err)
			}
		})
	}
}
