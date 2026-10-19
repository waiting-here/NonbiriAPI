package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestTraineeUserManagementRealSessionAndRevokedReplay(t *testing.T) {
	f := newGameWireFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	zero, one := db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{15: 1})
	result, err := tx.Exec(`INSERT INTO users(discord_id,username,auto_level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('synthetic-managed-member','Managed member',4,?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, one, f.now, f.now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(ctx, tx, target, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(ctx, tx, target, ledger.Game, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.authRuntime.IdentityContinuity().BindUserTx(ctx, tx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,?)", target, f.now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	base := fmt.Sprintf("/api/steward/users/%d", target)
	call := func(method, path, body, key string, want int) {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		got := testApplicationRequest(t, f.app.handler, method, auditUserHost, path, body, f.cookies, headers)
		if got.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, got.Code, want, got.Body)
		}
	}
	for _, path := range []string{"/api/steward/users?account_state=all&page=1&page_size=20", base, base + "/loans", base + "/penalties"} {
		call("GET", path, "", "", http.StatusOK)
	}
	call("PATCH", base, `{"mode":"profile","expected_revision":"1","lang":"en"}`, strings.Repeat("P", 22), http.StatusForbidden)
	body := `{"expected_revision":"1","reason":"Account review","duration_seconds":null}`
	key := strings.Repeat("B", 22)
	call("POST", base+"/ban", body, key, http.StatusNoContent)
	call("POST", base+"/ban", body, key, http.StatusNoContent)
	call("POST", base+"/unban", `{"expected_revision":"2"}`, strings.Repeat("U", 22), http.StatusForbidden)
	exec("UPDATE users SET level=5 WHERE id=?", target)
	call("POST", base+"/ban", body, key, http.StatusForbidden)
	exec("UPDATE users SET level=NULL WHERE id=?", target)
	exec("UPDATE users SET level=4 WHERE id=?", f.userID)
	call("GET", base, "", "", http.StatusForbidden)
	call("POST", base+"/ban", body, key, http.StatusForbidden)
	exec("UPDATE users SET level=5,is_banned=1 WHERE id=?", f.userID)
	call("GET", base, "", "", http.StatusForbidden)
	call("POST", base+"/ban", body, key, http.StatusForbidden)
	exec("UPDATE users SET is_banned=0 WHERE id=?", f.userID)
	exec("UPDATE site_config SET value='1' WHERE key='mfa_required_level_5'")
	call("GET", base, "", "", http.StatusForbidden)
	call("POST", base+"/ban", body, key, http.StatusForbidden)
	exec("UPDATE site_config SET value='0' WHERE key='mfa_required_level_5'")
	call("GET", base, "", "", http.StatusOK)
	exec("DELETE FROM sessions WHERE user_id=?", f.userID)
	call("GET", base, "", "", http.StatusUnauthorized)
	call("POST", base+"/ban", body, key, http.StatusUnauthorized)
}
