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

func TestAuditThresholdsUseLiveIndependentPermissionAndRevision(t *testing.T) {
	f := newAuditFixture(t)
	ctx := t.Context()
	admin := Actor{Admin: true, UserID: f.admin}
	levelSix := Actor{UserID: f.user(6)}
	trainee := Actor{UserID: f.user(5)}
	config, err := f.repository.GetConfig(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	config.SharedIPUsers = 5
	saved, err := f.repository.UpdateConfig(ctx, levelSix, config)
	if err != nil || saved.SharedIPUsers != 5 || saved.Revision != config.Revision+1 {
		t.Fatal("level six configuration", saved, err)
	}
	policy := authz.DefaultStewardPermissions(0)
	policy.Permissions["risk.manage"] = true
	writePolicy := func() {
		t.Helper()
		grants, _ := json.Marshal(policy.Permissions)
		limits, _ := json.Marshal(policy.Limits)
		f.exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'none',?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, trainee.UserID, string(grants), string(limits), f.now, f.now)
	}
	writePolicy()
	// A caller's operation context cannot substitute a different grant for config writes.
	manageCtx := authz.WithStewardPermission(ctx, "risk.manage")
	if _, err := f.repository.UpdateConfig(manageCtx, trainee, saved); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("scan permission changed thresholds", err)
	}
	policy.Permissions["risk.manage"] = false
	policy.Permissions["risk.configure"] = true
	writePolicy()
	current, err := f.repository.GetConfig(ctx, trainee)
	if err != nil || current != saved {
		t.Fatal("configure-only read", current, err)
	}
	current.SharedIPUsers = 2
	saved, err = f.repository.UpdateConfig(ctx, trainee, current)
	if err != nil || saved.SharedIPUsers != 2 {
		t.Fatal("configure-only write", saved, err)
	}
	if _, err = f.repository.UpdateConfig(ctx, trainee, current); !errors.Is(err, ErrConflict) {
		t.Fatal("stale configuration accepted", err)
	}
	if _, err := f.repository.SharedIPs(ctx, trainee, Window{From: f.now - 600, To: f.now, Limit: 10}, ""); !errors.Is(err, ErrForbidden) && !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("configuration permission exposed audit identities", err)
	}
	if _, err := f.repository.CreateScan(authz.WithStewardPermission(ctx, "risk.manage"), trainee, scanInput("config_only_scan")); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("configuration permission started a scan", err)
	}
	policy.Permissions["risk.configure"] = false
	policy.Permissions["risk.read"] = true
	writePolicy()
	if _, err := f.repository.GetConfig(ctx, trainee); err != nil {
		t.Fatal("read-only configuration", err)
	}
	if _, err := f.repository.UpdateConfig(ctx, trainee, saved); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked configuration permission accepted", err)
	}
	if actual, err := f.repository.GetConfig(ctx, admin); err != nil || actual != saved {
		t.Fatal("denied or conflicting write changed configuration", actual, err)
	}
}
