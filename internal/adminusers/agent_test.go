package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"strconv"
	"testing"
)

func allowAgent(context.Context, *sql.Tx, agentplan.Actor, string) error { return nil }
func parseAgentTest(t *testing.T, id int64, kind, body string) AgentOperation {
	t.Helper()
	p, err := ParseAgentOperation(agentplan.Operation{Kind: kind, TargetID: strconv.FormatInt(id, 10), Input: json.RawMessage(body)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestAgentUserTransactionRollbackAndOrderedVersions(t *testing.T) {
	f := newAdminUsersFixture(t)
	id := f.seedUser("agent-atomic", false)
	actor := agentplan.Actor{ID: f.adminID, Role: "admin"}
	first := parseAgentTest(t, id, "users.profile", `{"expected_revision":"1","level":4,"rpm_limit":"33"}`)
	stale := parseAgentTest(t, id, "users.economy", `{"expected_revision":"1","target":"balance","direction":"increase","amount":"4.125","reason":"test adjustment"}`)
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, finish, err := f.service.AgentApply(t.Context(), tx, actor, first, "discord-agent-atomic", allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	_, failed, err := f.service.AgentApply(t.Context(), tx, actor, stale, "", allowAgent)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second operation=%v", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	failed(false)
	finish(false)
	if got := f.revision(id); got != "1" || len(f.invalidator.users) != 0 {
		t.Fatalf("rollback revision=%s invalidations=%v", got, f.invalidator.users)
	}
	second := parseAgentTest(t, id, "users.economy", `{"expected_revision":"2","target":"balance","direction":"increase","amount":"4.125","reason":"test adjustment"}`)
	tx, err = f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, finish, err = f.service.AgentApply(t.Context(), tx, actor, first, "discord-agent-atomic", allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	raw, secondFinish, err := f.service.AgentApply(t.Context(), tx, actor, second, "", allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.invalidator.users) != 0 {
		t.Fatal("invalidated before commit")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	secondFinish(true)
	finish(true)
	var user AdminUser
	if err = json.Unmarshal(raw, &user); err != nil {
		t.Fatal(err)
	}
	if user.Revision != "3" || user.Balance != "4.125" || len(f.invalidator.users) != 1 {
		t.Fatalf("result=%s invalidations=%v", raw, f.invalidator.users)
	}
}
func TestAgentUserLiveHierarchyLimitsAndRevocation(t *testing.T) {
	f := newAdminUsersFixture(t)
	id := f.seedUser("agent-target", false)
	steward := f.seedUser("agent-steward", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	actor := agentplan.Actor{ID: steward, Role: "level5"}
	op := parseAgentTest(t, id, "users.profile", `{"expected_revision":"1","rpm_limit":"30"}`)
	if _, err := f.service.AgentPreflight(t.Context(), actor, []AgentOperation{op}, allowAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	tx, _ := f.store.DB().BeginTx(t.Context(), nil)
	_, finish, err := f.service.AgentApply(t.Context(), tx, actor, op, "", allowAgent)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("promoted target=%v", err)
	}
	tx.Rollback()
	finish(false)
	if _, err = f.store.DB().Exec("UPDATE users SET level=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Limits["users.rpm_limit_max"] = "20"
	flags, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err = f.store.DB().Exec("INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',?,?)", steward, string(flags), string(limits), adminUsersTestNow, adminUsersTestNow); err != nil {
		t.Fatal(err)
	}
	tx, _ = f.store.DB().BeginTx(t.Context(), nil)
	_, finish, err = f.service.AgentApply(t.Context(), tx, actor, op, "", allowAgent)
	if !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatalf("live bound=%v", err)
	}
	tx.Rollback()
	finish(false)
	revoke := parseAgentTest(t, id, "users.caller_key_revoke", `{"expected_generation":"0"}`)
	tx, _ = f.store.DB().BeginTx(t.Context(), nil)
	_, finish, err = f.service.AgentApply(t.Context(), tx, actor, revoke, "", allowAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	finish(true)
	var active bool
	if err = f.store.DB().QueryRow("SELECT key_hash IS NOT NULL FROM caller_keys WHERE user_id=?", id).Scan(&active); err != nil || active || len(f.invalidator.users) != 1 {
		t.Fatalf("key active=%v err=%v invalidations=%v", active, err, f.invalidator.users)
	}
}

func TestAgentUserActiveBanAndActualDelegationBoundaries(t *testing.T) {
	f := newAdminUsersFixture(t)
	id := f.seedUser("agent-banned", false)
	steward := f.seedUser("agent-policy-actor", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=1,banned_reason='test' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["users.profile"] = true
	flags, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := f.store.DB().Exec("INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',?,?)", steward, string(flags), string(limits), adminUsersTestNow, adminUsersTestNow); err != nil {
		t.Fatal(err)
	}
	actor := agentplan.Actor{ID: steward, Role: "level6"}
	op := parseAgentTest(t, id, "users.profile", `{"expected_revision":"1","lang":"en"}`)
	tx, _ := f.store.DB().BeginTx(t.Context(), nil)
	_, finish, err := f.service.AgentApply(t.Context(), tx, actor, op, "", allowAgent)
	if !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatalf("active ban prerequisite=%v", err)
	}
	tx.Rollback()
	finish(false)
	// An asserted level 6 label does not grant delegation to an actual level 5.
	policy.Revision = "0"
	body, _ := json.Marshal(map[string]any{"expected_revision": "0", "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope})
	delegate, err := ParseAgentOperation(agentplan.Operation{Kind: "users.permissions", TargetID: strconv.FormatInt(id, 10), Input: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.AgentPreflight(t.Context(), actor, []AgentOperation{delegate}, allowAgent); err == nil {
		t.Fatal("actual level 5 delegated")
	}
	for _, kind := range []string{"users.profile", "users.mfa_reset"} {
		if _, err = ParseAgentOperation(agentplan.Operation{Kind: kind, TargetID: strconv.FormatInt(id, 10), Input: json.RawMessage(`{"expected_revision":"1","password":"never accepted"}`)}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("credential field accepted by %s", kind)
		}
	}
}
