package riskaudit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestConfiguredStewardRuleUsesActualAuditLevelAndLiveGrant(t *testing.T) {
	f := newAuditFixture(t)
	id := f.user(5)
	p := authz.DefaultStewardPermissions(5)
	p.Permissions["risk.manage"] = true
	permissions, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	f.exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, id, string(permissions), string(limits), f.now, f.now)
	ctx := authz.WithStewardPermission(context.Background(), "risk.manage")
	actor := Actor{UserID: id}
	rule, err := f.repository.PutRule(ctx, actor, Rule{Name: "Configured rule", Status: "suspected", Enabled: true, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "Client/"}}}, true)
	if err != nil || rule.CreatedByRole != "level5" || rule.UpdatedByRole != "level5" {
		t.Fatalf("actual audit: %+v %v", rule, err)
	}
	p.Permissions["risk.manage"] = false
	permissions, _ = json.Marshal(p.Permissions)
	f.exec(`UPDATE steward_permissions SET permissions=?,revision=2 WHERE user_id=?`, string(permissions), id)
	if _, err := f.repository.PutRule(ctx, actor, rule, false); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked edit accepted", err)
	}
}
