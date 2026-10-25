package authz

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestAIPermissionsDefaultsCurrentFormatAndRoutes(t *testing.T) {
	p := DefaultStewardPermissions(5)
	if !p.Permissions["ai.chat"] || p.Permissions["ai.review.read"] || p.Permissions["ai.memory.edit"] || len(p.Permissions) != 51 {
		t.Fatal("AI defaults", p)
	}
	for key := range p.Permissions {
		p.Permissions[key] = false
	}
	for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
		delete(p.Permissions, key)
	}
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	if _, err := DecodeStewardPermissions(5, 1, string(grants), string(limits), "none"); err == nil {
		t.Fatal("runtime accepted unconverted stored policy")
	}
	if ValidateStewardPermissions(p) == nil {
		t.Fatal("PUT accepted incomplete catalog")
	}
	current := DefaultStewardPermissions(5)
	currentBody, _ := json.Marshal(current.Permissions)
	for _, malformed := range []string{
		strings.Replace(string(currentBody), `"ai.chat":true`, `"ai.chat":null`, 1),
		strings.Replace(string(currentBody), `"ai.chat":true`, `"ai.chat":true,"ai.chat":false`, 1),
		strings.Replace(string(currentBody), `"ai.chat":true`, `"unknown":true`, 1),
	} {
		if _, err := DecodeStewardPermissions(5, 1, malformed, string(limits), "standard"); err == nil {
			t.Fatal("malformed current row accepted")
		}
	}
	for path, want := range map[string]string{
		"/ai/chat": "ai.chat", "/ai/chat/sessions/1/messages": "ai.chat", "/ai/chat/plans/1/approve": "ai.chat",
		"/ai/reviews": "ai.review.read", "/ai/reviews/1": "ai.review.read",
		"/ai/memories/chat": "ai.memory.edit", "/ai/memories/auto_review": "ai.memory.edit",
		"/ai/config": "unconfigured", "/ai/requests": "unconfigured", "/ai/usage": "unconfigured", "/ai/archives": "unconfigured", "/ai/cleanup": "unconfigured",
	} {
		if got := StewardRoutePermission("GET", "/api/steward"+path); got != want {
			t.Fatal(path, got, want)
		}
	}
	if StewardRoutePermission("POST", "/api/steward/donations/1/pending-review") != "donations.review" {
		t.Fatal("whole pending review mapping")
	}
	if StewardRoutePermission("PUT", "/api/steward/ai/chat/messages/1") != "ai.chat" {
		t.Fatal("chat message edit mapping")
	}
	if StewardRoutePermission("PUT", "/api/steward/ai/memories/chat") != "ai.memory.edit" {
		t.Fatal("memory write mapping")
	}
	if StewardRoutePermission("POST", "/api/steward/ai/reviews") != "unconfigured" {
		t.Fatal("review write mapping")
	}
}
