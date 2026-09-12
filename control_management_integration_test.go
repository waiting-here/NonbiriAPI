package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestStewardManagementRealSessionsAndFinalRoleChecks(t *testing.T) {
	f := newGameWireFixture(t)
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	one, _ := db.ParseU128Decimal("1")
	insert, err := tx.Exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"managed account", zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), f.now, f.now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := insert.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(context.Background(), tx, target, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(context.Background(), tx, target, ledger.Game, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,?)", target, f.now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	read := func(path string, cookies []*http.Cookie) int {
		return testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", cookies, nil).Code
	}
	users := "/api/steward/users"
	announcements := "/api/steward/announcements"
	for _, path := range []string{users, announcements} {
		if status := read(path, f.cookies); status != 403 {
			t.Fatalf("ordinary user %s: %d", path, status)
		}
		if status := read(path, f.adminCookies); status != 401 {
			t.Fatalf("admin cookie crossed station %s: %d", path, status)
		}
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", f.userID); err != nil {
		t.Fatal(err)
	}
	list := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, users+"?level=5&page=1", "", f.cookies, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"total_items":"1"`) {
		t.Fatalf("steward level list: %d %s", list.Code, list.Body)
	}
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost, "Idempotency-Key": strings.Repeat("s", 22)}
	path := fmt.Sprintf("%s/%d", users, target)
	profile := `{"mode":"profile","expected_revision":"1","endpoint_limit":"999","rpm_limit":null,"concurrency_limit":"999"}`
	patched := testApplicationRequest(t, f.app.handler, "PATCH", auditUserHost, path, profile, f.cookies, headers)
	if patched.Code != 200 || !strings.Contains(patched.Body.String(), `"endpoint_limit":"999"`) {
		t.Fatalf("profile: %d %s", patched.Code, patched.Body)
	}
	headers["Idempotency-Key"] = strings.Repeat("g", 22)
	economy := `{"mode":"economy","expected_revision":"2","target":"game_balance","direction":"decrease","amount":"0.005","reason":"Correction"}`
	adjusted := testApplicationRequest(t, f.app.handler, "PATCH", auditUserHost, path, economy, f.cookies, headers)
	if adjusted.Code != 200 || !strings.Contains(adjusted.Body.String(), `"game_balance":"-0.005"`) {
		t.Fatalf("game adjustment: %d %s", adjusted.Code, adjusted.Body)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=5 WHERE id=?", target); err != nil {
		t.Fatal(err)
	}
	replay := testApplicationRequest(t, f.app.handler, "PATCH", auditUserHost, path, economy, f.cookies, headers)
	if replay.Code != 403 {
		t.Fatalf("promoted target replay: %d %s", replay.Code, replay.Body)
	}

	headers["Idempotency-Key"] = strings.Repeat("n", 22)
	createBody := `{"title_zh":"","body_zh":"","title_en":"Managed notice","body_en":"Notice body","severity":"info","pinned":false,"dismissible":true}`
	created := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, announcements, createBody, f.cookies, headers)
	var receipt struct{ ID, Revision string }
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &receipt) != nil || receipt.Revision != "1" {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	headers["Idempotency-Key"] = strings.Repeat("p", 22)
	published := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, announcements+"/"+receipt.ID+"/publish", `{"expected_revision":"1"}`, f.cookies, headers)
	if published.Code != 200 {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	public := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/announcements", "", f.cookies, nil)
	if public.Code != 200 || !strings.Contains(public.Body.String(), "Managed notice") {
		t.Fatalf("public notice: %d %s", public.Code, public.Body)
	}
	admin := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/announcements/"+receipt.ID, "", f.adminCookies, nil)
	if admin.Code != 200 {
		t.Fatalf("admin reads steward notice: %d %s", admin.Code, admin.Body)
	}

	if _, err := f.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", f.userID); err != nil {
		t.Fatal(err)
	}
	headers["Idempotency-Key"] = strings.Repeat("n", 22)
	denied := testApplicationRequest(t, f.app.handler, "POST", auditUserHost, announcements, createBody, f.cookies, headers)
	if denied.Code != 403 {
		t.Fatalf("demoted announcement replay: %d %s", denied.Code, denied.Body)
	}
	for _, path := range []string{users, announcements} {
		if status := read(path, f.cookies); status != 403 {
			t.Fatalf("demoted read %s: %d", path, status)
		}
	}
}
