package adminusers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func blacklistRequest(f *adminUsersFixture, add bool, id, reason, key string) *httptest.ResponseRecorder {
	route := routeBlacklist
	body := fmt.Sprintf(`{"discord_id":%q,"reason":%q}`, id, reason)
	target := route
	if !add {
		route = routeBlacklistRemove
		body = ""
		target = strings.ReplaceAll(route, "{discordID}", id)
	}
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.SetPathValue("discordID", id)
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.registrar.handler("POST", route)(w, req, AdminPrincipal{UserID: f.adminID})
	return w
}
func TestBlacklistExistingAccountRevocationReplayAndRemoval(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("blacklist", false)
	const id = "123456789012345678"
	if _, err := f.store.DB().Exec("UPDATE users SET discord_id=? WHERE id=?", id, user); err != nil {
		t.Fatal(err)
	}
	f.addSession(user, "blacklist-session")
	canceled, finalized := 0, 0
	f.service.cancelUserDuelsTx = func(_ context.Context, _ *sql.Tx, target int64, _ string, _ int64) (func(bool), error) {
		if target != user {
			t.Fatal("wrong target")
		}
		canceled++
		return func(committed bool) {
			if committed {
				finalized++
			}
		}, nil
	}
	for range 2 {
		result := blacklistRequest(f, true, id, "policy violation", "AAAAAAAAAAAAAAAAAAAAAA")
		if result.Code != http.StatusNoContent {
			t.Fatalf("add=%d %s", result.Code, result.Body.String())
		}
	}
	var banned, sessions, keys, entries int
	var until sql.NullInt64
	if err := f.store.DB().QueryRow("SELECT is_banned,banned_until FROM users WHERE id=?", user).Scan(&banned, &until); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", user).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM caller_keys WHERE user_id=? AND key_hash IS NOT NULL", user).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM discord_blacklist WHERE discord_id=?", id).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if banned != 1 || until.Valid || sessions != 0 || keys != 0 || entries != 1 || canceled != 1 || finalized != 1 || len(f.invalidator.users) != 1 {
		t.Fatalf("revocation=%d %v %d %d %d cancel=%d finalize=%d invalidations=%v", banned, until, sessions, keys, entries, canceled, finalized, f.invalidator.users)
	}
	body := fmt.Sprintf(`{"expected_revision":%q}`, f.revision(user))
	denied := f.request("POST", routeUnban, "/admin/api/users/1/unban", body, user, "BBBBBBBBBBBBBBBBBBBBBA")
	if denied.Code != http.StatusConflict {
		t.Fatalf("blacklist unban=%d %s", denied.Code, denied.Body.String())
	}
	if _, err := f.store.DB().Exec("UPDATE users SET is_banned=0 WHERE id=?", user); err == nil {
		t.Fatal("database allowed blacklist bypass")
	}
	removed := blacklistRequest(f, false, id, "", "CCCCCCCCCCCCCCCCCCCCCA")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove=%d %s", removed.Code, removed.Body.String())
	}
	if err := f.store.DB().QueryRow("SELECT is_banned FROM users WHERE id=?", user).Scan(&banned); err != nil || banned != 1 {
		t.Fatal("removal implicitly unbanned", err)
	}
	restored := f.request("POST", routeUnban, "/admin/api/users/1/unban", body, user, "DDDDDDDDDDDDDDDDDDDDDA")
	if restored.Code != http.StatusNoContent {
		t.Fatalf("separate unban=%d %s", restored.Code, restored.Body.String())
	}
}

func TestBlacklistRollbackFinalAuthorityAndFutureIdentity(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("rollback", false)
	const id = "123456789012345679"
	if _, err := f.store.DB().Exec("UPDATE users SET discord_id=? WHERE id=?", id, user); err != nil {
		t.Fatal(err)
	}
	f.addSession(user, "retained-session")
	if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_blacklist BEFORE INSERT ON discord_blacklist BEGIN SELECT RAISE(ABORT,'injected failure');END`); err != nil {
		t.Fatal(err)
	}
	response := blacklistRequest(f, true, id, "reason", "EEEEEEEEEEEEEEEEEEEEEA")
	if response.Code < 500 {
		t.Fatal("failure injection ignored")
	}
	var banned, sessions int
	if err := f.store.DB().QueryRow("SELECT is_banned FROM users WHERE id=?", user).Scan(&banned); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", user).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if banned != 0 || sessions != 1 || len(f.invalidator.users) != 0 {
		t.Fatal("partial revocation escaped rollback")
	}
	if _, err := f.store.DB().Exec("DROP TRIGGER reject_blacklist"); err != nil {
		t.Fatal(err)
	}
	f.auth.err = authz.ErrForbidden
	if result := blacklistRequest(f, true, id, "reason", "FFFFFFFFFFFFFFFFFFFFFA"); result.Code != 403 {
		t.Fatal(result.Code)
	}
	f.auth.err = nil
	if result := blacklistRequest(f, true, "123456789012345680", "future identity", "GGGGGGGGGGGGGGGGGGGGGA"); result.Code != 204 {
		t.Fatalf("future=%d %s", result.Code, result.Body.String())
	}
	for _, invalid := range []string{"0", "00123", "-1", "1e3", "18446744073709551616"} {
		if result := blacklistRequest(f, true, invalid, "reason", "HHHHHHHHHHHHHHHHHHHHHA"); result.Code != 400 {
			t.Fatal(invalid, result.Code)
		}
	}
	if err := RegisterStewardRoutes(f.registrar, f.service); err != nil {
		t.Fatal(err)
	}
	if f.registrar.handler("POST", roleSteward.route(routeBlacklist)) != nil {
		t.Fatal("blacklist exposed to stewards")
	}
}
