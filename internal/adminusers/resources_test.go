package adminusers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
)

func TestManagedResourcesAuthorityReplayAndAudit(t *testing.T) {
	f, manager := newStewardUsersFixture(t)
	member := f.seedUser("resource-member", false)
	trainee := f.seedUser("resource-trainee", false)
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", trainee); err != nil {
		t.Fatal(err)
	}
	history := managementlog.New(f.store.DB(), f.store.ReadDB())
	for i := range f.registrar.routes {
		route := &f.registrar.routes[i]
		original, method, pattern := route.handler, route.method, route.pattern
		route.handler = func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
			history.Wrap(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := managementlog.SetIdentity(r.Context(), f.store.DB(), p.UserID, managementlog.SourceWeb); err != nil {
					t.Fatal(err)
				}
				original(w, r, p)
			})).ServeHTTP(w, r)
		}
	}
	read := stewardUserRequest(t, f, trainee, member, "GET", routeUserResources, "", "", "")
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"lake:coins"`) || strings.Contains(read.Body.String(), `"id":"general"`) {
		t.Fatal(read.Code, read.Body)
	}
	body := `{"asset":"lake:coins","direction":"increase","quantity":"10","expected_quantity":"0","reason":"Correct missing event grant"}`
	write := func(actor, target int64, body, key string, want int) string {
		t.Helper()
		r := stewardUserRequest(t, f, actor, target, "PATCH", routeUserResources, "", body, key)
		if r.Code != want {
			t.Fatalf("resource PATCH=%d want %d: %s", r.Code, want, r.Body)
		}
		return r.Body.String()
	}
	key := "resource-adjust-first-0001"
	write(trainee, member, body, key, 403)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["users.resources"] = true
	put := func(revision, key string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"expected_revision": revision, "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope})
		r := stewardUserRequest(t, f, manager, trainee, "PUT", routeStewardPermissions, "", string(b), key)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body)
		}
	}
	put("0", "resource-policy-grant-0001")
	first := write(trainee, member, body, key, 200)
	if again := write(trainee, member, body, key, 200); again != first {
		t.Fatal("replay response changed")
	}
	write(trainee, member, body, "resource-adjust-stale-0001", 409)
	write(trainee, member, strings.Replace(body, "lake:coins", "general", 1), "resource-core-forbidden-0001", 400)
	write(trainee, trainee, body, "resource-self-forbidden-0001", 403)
	write(trainee, manager, body, "resource-peer-forbidden-0001", 403)
	policy.Permissions["users.resources"] = false
	put("1", "resource-policy-revoke-0001")
	write(trainee, member, body, key, 403)
	write(manager, trainee, body, "resource-six-adjust-0001", 200)
	admin := f.request("PATCH", routeUserResources, fmt.Sprintf("/admin/api/users/%d/resources", member), strings.Replace(body, `"expected_quantity":"0"`, `"expected_quantity":"10"`, 1), member, "resource-admin-adjust-0001")
	if admin.Code != 200 {
		t.Fatal(admin.Code, admin.Body)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM management_operations WHERE action='resources.update' AND outcome='succeeded'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("audit/replay count", count, err)
	}

	var auditJSON string
	if err := f.store.DB().QueryRow(`SELECT i.changes FROM management_operation_items i JOIN management_operations o ON o.id=i.operation_id WHERE o.action='resources.update' AND o.outcome='succeeded' ORDER BY o.id LIMIT 1`).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	var changes []managementlog.Change
	if err := json.Unmarshal([]byte(auditJSON), &changes); err != nil {
		t.Fatal(err)
	}
	foundAmount, foundReason := false, false
	for _, change := range changes {
		if change.Field == "lake:coins" && change.Before != nil && change.After != nil && *change.Before == "0" && *change.After == "10" {
			foundAmount = true
		}
		if change.Field == "reason" && change.After != nil && *change.After == "Correct missing event grant" {
			foundReason = true
		}
	}
	if !foundAmount || !foundReason {
		t.Fatal("missing exact adjustment audit", auditJSON)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", member); err != nil {
		t.Fatal(err)
	}
	policy.Permissions["users.resources"] = true
	put("2", "resource-policy-regrant-0001")
	write(trainee, member, body, key, 403)
	if _, err := f.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", member); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO user_deletion_markers(user_id) VALUES(?)", member); err != nil {
		t.Fatal(err)
	}
	write(trainee, member, body, key, 404)
}

func TestManagedResourcesReceiptFailureRollsBackInventoryAndLedger(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("atomic-resource-owner", false)
	for index, asset := range []string{"sketch_paper", "gacha:permanent", "lake:coins"} {
		body, _ := json.Marshal(rewards.ResourceAdjustment{Asset: asset, Direction: "increase", Quantity: "1", ExpectedQuantity: "0", Reason: "rollback test"})
		key := fmt.Sprintf("atomic-resource-adjust-%04d", index)
		if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_resource_receipt BEFORE UPDATE ON idempotency_records WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
			t.Fatal(err)
		}
		failed := f.request("PATCH", routeUserResources, fmt.Sprintf("/admin/api/users/%d/resources", user), string(body), user, key)
		if failed.Code != 500 {
			t.Fatal(failed.Code, failed.Body)
		}
		items, err := f.service.managedResources(t.Context(), f.adminID, user, roleAdmin)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == asset && item.Quantity != "0" {
				t.Fatal("failed transaction changed inventory", item)
			}
		}
		if _, err := f.store.DB().Exec("DROP TRIGGER fail_resource_receipt"); err != nil {
			t.Fatal(err)
		}
		retry := f.request("PATCH", routeUserResources, fmt.Sprintf("/admin/api/users/%d/resources", user), string(body), user, key)
		if retry.Code != 200 {
			t.Fatal("retry after rollback", retry.Code, retry.Body)
		}
	}
}
