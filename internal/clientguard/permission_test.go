package clientguard

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestRuleGrantRevocationBetweenAdmissionAndEffectsPreventsBan(t *testing.T) {
	f := newGuardFixture(t)
	f.exec(`UPDATE caller_keys SET key_hash=NULL,key_created_at=NULL,display_head='',display_tail='',generation=generation+1 WHERE user_id=?`, f.user)
	actor := f.makeUser()
	f.exec(`UPDATE users SET level=5 WHERE id=?`, actor)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["risk.manage"] = true
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	f.exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, actor, string(permissions), string(limits), testNow, testNow)
	rule := f.rule("configured rule", "Client/", true, nil)
	f.exec(`UPDATE client_rule_auto_bans SET actor_user_id=? WHERE rule_id=?`, actor, rule)
	f.exec(`UPDATE risk_client_rules SET updated_by_role='level6',updated_by_user_id=? WHERE id=?`, actor, rule)
	f.beforeGate = func() {
		policy.Permissions["risk.manage"] = false
		permissions, _ := json.Marshal(policy.Permissions)
		f.exec(`UPDATE steward_permissions SET revision=2,permissions=? WHERE user_id=?`, string(permissions), actor)
	}
	decision, err := f.service.CheckCall(f.context("self", "Client/1"), f.user, "provider/model", testNow)
	if err != nil || decision.Banned || f.gateCalls != 1 || f.gameCalls != 0 || f.scalar(`SELECT is_banned FROM users WHERE id=?`, f.user) != 0 {
		t.Fatalf("revoked policy applied effects: %+v %v gates=%d games=%d", decision, err, f.gateCalls, f.gameCalls)
	}
}
