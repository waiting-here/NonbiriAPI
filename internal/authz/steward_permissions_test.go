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
	if !p.Permissions["ai.chat"] || p.Permissions["ai.review.read"] || p.Permissions["ai.memory.edit"] || len(p.Permissions) != 56 {
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
		"/ai/memories/chat": "ai.chat", "/ai/memories/auto_review": "ai.chat", "/ai/personal-memory": "ai.chat",
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
	if StewardRoutePermission("PUT", "/api/steward/ai/memories/chat") != "unconfigured" || StewardRoutePermission("PUT", "/api/steward/ai/personal-memory") != "ai.chat" {
		t.Fatal("memory write scope mapping")
	}
	if StewardRoutePermission("POST", "/api/steward/ai/reviews") != "unconfigured" {
		t.Fatal("review write mapping")
	}
}

func TestModelQuotaRoutesRequireRecurringPermission(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		for _, path := range []string{"/api/steward/donations/{id}/keys/{keyId}/model-limits", "/api/steward/donations/1/keys/2/model-limits"} {
			if got := StewardRoutePermission(method, path); got != "donations.recurring" {
				t.Fatal(method, path, got)
			}
		}
	}
}

func TestCharityConfigurationPermissionsRemainIndependent(t *testing.T) {
	keys := []string{"charity.request_adaptation", "charity.affinity", "charity.price_templates.manage"}
	for _, key := range keys {
		if DefaultStewardPermissions(5).Permissions[key] || !DefaultStewardPermissions(6).Permissions[key] {
			t.Fatal("default", key)
		}
	}
	p := DefaultStewardPermissions(5)
	for key := range p.Permissions {
		p.Permissions[key] = false
	}
	for _, key := range keys {
		delete(p.Permissions, key)
	}
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	if _, err := DecodeStewardPermissions(5, 1, string(grants), string(limits), "standard"); err == nil {
		t.Fatal("old catalog accepted without upgrade")
	}
	p = DefaultStewardPermissions(5)
	for key := range p.Permissions {
		p.Permissions[key] = false
	}
	for _, key := range keys {
		p.Permissions[key] = true
		switch key {
		case "charity.request_adaptation":
			if !p.AllowsOperation("charity.request_adaptation.read") || !p.AllowsOperation("charity.read") || p.AllowsOperation("model_fields") {
				t.Fatal("adaptation implication")
			}
		case "charity.affinity":
			if !p.AllowsOperation("charity.read") || !p.AllowsOperation("model_fields") || p.AllowsOperation("charity.request_adaptation") {
				t.Fatal("affinity implication")
			}
		case "charity.price_templates.manage":
			if !p.AllowsOperation("charity.price_templates.read") || p.AllowsOperation("charity.read") {
				t.Fatal("template implication")
			}
		}
		p.Permissions[key] = false
	}
	p.Permissions["charity.model_settings"] = true
	if !p.AllowsOperation("charity.request_adaptation.read") || p.AllowsOperation("charity.request_adaptation") {
		t.Fatal("old read authority changed")
	}
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/api/steward/charity-models/1/request-adaptation", "charity.request_adaptation.read"},
		{"PUT", "/api/steward/charity-models/1/request-adaptation", "charity.request_adaptation"},
		{"PUT", "/api/steward/charity-models/1/bindings/2/request-adaptation", "charity.request_adaptation"},
		{"GET", "/api/steward/charity-price-templates", "charity.price_templates.read"},
		{"POST", "/api/steward/charity-price-templates", "charity.price_templates.manage"},
		{"PUT", "/api/steward/charity-price-templates/1", "charity.price_templates.manage"},
		{"DELETE", "/api/steward/charity-price-templates/1", "charity.price_templates.manage"},
	} {
		if got := StewardRoutePermission(tc.method, tc.path); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestRiskConfigurationPermissionIsIndependent(t *testing.T) {
	if DefaultStewardPermissions(5).Permissions["risk.configure"] || !DefaultStewardPermissions(6).Permissions["risk.configure"] {
		t.Fatal("audit threshold defaults")
	}
	for _, grant := range []string{"", "risk.read", "risk.manage", "risk.configure"} {
		p := DefaultStewardPermissions(0)
		if grant != "" {
			p.Permissions[grant] = true
		}
		for _, tc := range []struct {
			method, path, operation string
			allowed                 bool
		}{
			{"GET", "config", "risk.config.read", grant == "risk.read" || grant == "risk.configure"},
			{"PUT", "config", "risk.configure", grant == "risk.configure"},
			{"GET", "users", "risk.read", grant == "risk.read"},
			{"POST", "scans", "risk.manage", grant == "risk.manage"},
		} {
			operation := StewardRoutePermission(tc.method, "/api/steward/abuse-audit/"+tc.path)
			if operation != tc.operation || p.AllowsOperation(operation) != tc.allowed {
				t.Fatalf("grant=%q route=%+v operation=%q", grant, tc, operation)
			}
		}
	}
}

func TestResourceManagementPermissionRoutes(t *testing.T) {
	if StewardRoutePermission("GET", "/api/steward/users/1/resources") != "users.read" || StewardRoutePermission("PATCH", "/api/steward/users/1/resources") != "users.resources" {
		t.Fatal("resource route authority drift")
	}
	if DefaultStewardPermissions(5).Permissions["users.resources"] || !DefaultStewardPermissions(6).Permissions["users.resources"] {
		t.Fatal("resource defaults drift")
	}
}
