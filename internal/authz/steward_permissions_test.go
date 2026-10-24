package authz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestStewardPermissionsLiveRevocationPreservesLevelAndPersonalAuthority(t *testing.T) {
	s := openAuthStore(t)
	level := int64(5)
	id := insertAuthUser(t, s.DB(), "permission-member", "permission-session", "g1", false, &level)
	a := Actor{Kind: ActorUserSession, UserID: id, SessionTokenHash: "permission-session", SessionGeneration: "g1"}
	ctx := WithStewardPermission(context.Background(), "announcements.manage")
	check := func(ctx context.Context, role Role, want error) {
		t.Helper()
		tx := beginAuthTx(t, s.DB())
		defer tx.Rollback()
		p, err := testAuthorizer(nil).Authorize(ctx, tx, a, Requirement{Role: role})
		if !errors.Is(err, want) {
			t.Fatalf("authority=%v want %v", err, want)
		}
		if err == nil && p.EffectiveLevel != 5 {
			t.Fatal("permission changed actual level")
		}
	}
	check(ctx, RoleSteward, ErrPermissionDenied)
	p := DefaultStewardPermissions(5)
	p.Permissions["announcements.manage"] = true
	write := func() {
		t.Helper()
		permissions, _ := json.Marshal(p.Permissions)
		limits, _ := json.Marshal(p.Limits)
		if _, err := s.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1`, id, string(permissions), string(limits), authTestNow, authTestNow); err != nil {
			t.Fatal(err)
		}
	}
	write()
	check(ctx, RoleSteward, nil)
	for key := range p.Permissions {
		p.Permissions[key] = false
	}
	write()
	check(ctx, RoleSteward, ErrPermissionDenied)
	check(context.Background(), RoleUser, nil)
	check(WithStewardPermission(context.Background(), "delegate"), RoleSteward, ErrForbidden)
	if _, err := s.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	tx := beginAuthTx(t, s.DB())
	defer tx.Rollback()
	live, err := ReadStewardPermissions(context.Background(), tx, id)
	if err != nil || !live.Permissions["announcements.manage"] || !live.Permissions["automation"] {
		t.Fatal("level six authority reduced by stored five settings", live, err)
	}
}

func TestStewardPermissionsRejectUnknownMissingAndNumericOverflow(t *testing.T) {
	for _, change := range []func(*StewardPermissions){
		func(p *StewardPermissions) { delete(p.Permissions, "users.read") },
		func(p *StewardPermissions) { p.Permissions["admin"] = true },
		func(p *StewardPermissions) { p.DonationScope = "" },
		func(p *StewardPermissions) { p.DonationScope = "all" },
		func(p *StewardPermissions) { p.Limits["users.rpm_limit_max"] = "4097" },
		func(p *StewardPermissions) { p.Limits["users.rpm_limit_max"] = "01" },
		func(p *StewardPermissions) { p.Limits["users.balance_adjustment_max_milli"] = "9223372036854775808" },
	} {
		p := DefaultStewardPermissions(5)
		change(&p)
		if ValidateStewardPermissions(p) == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	p := DefaultStewardPermissions(5)
	if !p.Permissions["users.donations.read"] || p.Permissions["logs.unredacted"] || p.Permissions["automation"] || p.Permissions["endpoints.read"] || p.Permissions["endpoints.tags"] || p.DonationScope != DonationScopeStandard {
		t.Fatal("default contract drift")
	}
}
