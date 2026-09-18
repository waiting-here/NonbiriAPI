package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type gameWireFixture struct {
	app                   *application
	store                 *db.Store
	userID, adminID, now  int64
	cookies, adminCookies []*http.Cookie
}

func newGameWireFixture(t *testing.T) gameWireFixture {
	return newGameWireFixtureWithClock(t, nil)
}

func newGameWireFixtureWithClock(t *testing.T, nowFunc func() time.Time) gameWireFixture {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "games-http.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app, err := buildApplicationWithGameClock(auditConfig(), store, vault, nowFunc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	login := testApplicationRequest(t, app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	adminCookies := []*http.Cookie{responseCookieNamed(t, login, auth.AdminSessionCookieName)}
	configuration := testApplicationRequest(t, app.handler, "GET", auditAdminHost, "/admin/api/games/config", "", adminCookies, nil)
	if configuration.Code != 200 {
		t.Fatalf("config: %d %s", configuration.Code, configuration.Body.String())
	}
	assertGameWireSample(t, "games-config.json", configuration.Body.Bytes())
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("G", 22)}
	disable := testApplicationRequest(t, app.handler, "POST", auditAdminHost, "/admin/api/maintenance/disable", `{"expected_revision":"1","reason":"Game integration validation"}`, adminCookies, headers)
	if disable.Code != 200 {
		t.Fatalf("maintenance: %d %s", disable.Code, disable.Body.String())
	}
	headers["Idempotency-Key"] = strings.Repeat("P", 22)
	patch := testApplicationRequest(t, app.handler, "PATCH", auditAdminHost, "/admin/api/games/config", `{"expected_revision":"1","master_enabled":true,"fishing":{"enabled":true}}`, adminCookies, headers)
	if patch.Code != 200 {
		t.Fatalf("patch: %d %s", patch.Code, patch.Body.String())
	}
	seedRootCallerIdentity(t, store)
	var userID, adminID int64
	if err := store.DB().QueryRow(`SELECT id FROM users WHERE discord_id='root-caller'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	wallet, err := ledger.CreateUserAccount(ctx, tx, userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(ctx, tx, userID, ledger.Game, now); err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: op, ActorUserID: adminID, CreatedAt: now}, wallet.ID, external.ID, ledger.AmountFromMilli(1_000_000_000), 0, ledger.Amount{}, "fixture funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	const token = "registered_game_http_session_token_0123456789"
	digest := sha256.Sum256([]byte(token))
	if _, err = tx.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, hex.EncodeToString(digest[:]), userID, now, now+3600, now+3600, now, "game-fixture-generation"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: auth.UserSessionCookieName, Value: token}}
	return gameWireFixture{app: app, store: store, userID: userID, adminID: adminID, now: now, cookies: cookies, adminCookies: adminCookies}
}

// Compare the production host and each module's live projection with the
// existing wire samples. Only the response's live clock is normalized.
func TestRegisteredGamesProductionWireCompatibility(t *testing.T) {
	fixture := newGameWireFixture(t)
	app, cookies, adminCookies, now := fixture.app, fixture.cookies, fixture.adminCookies, fixture.now
	var err error
	snapshot := testApplicationRequest(t, app.handler, "GET", auditUserHost, "/api/games", "", cookies, nil)
	if snapshot.Code != 200 {
		t.Fatalf("snapshot: %d %s", snapshot.Code, snapshot.Body.String())
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(snapshot.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	var serverNow int64
	if err = json.Unmarshal(object["server_now"], &serverNow); err != nil || serverNow < now || serverNow > time.Now().Unix() {
		t.Fatalf("server clock: %d %v", serverNow, err)
	}
	object["server_now"] = json.RawMessage("2000000000")
	body, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	assertGameWireSample(t, "games-snapshot.json", body)
	for _, endpoint := range []struct {
		host, path, want string
		cookies          []*http.Cookie
	}{
		{auditUserHost, "/api/home/game-summary", `{"continue":[],"pending_results":[]}`, cookies},
		{auditAdminHost, "/admin/api/games/active-counts", `{"games":[],"queues":[]}`, adminCookies},
	} {
		response := testApplicationRequest(t, app.handler, "GET", endpoint.host, endpoint.path, "", endpoint.cookies, nil)
		if response.Code != 200 || response.Body.String() != endpoint.want {
			t.Fatalf("%s: %d %s", endpoint.path, response.Code, response.Body.String())
		}
	}
}

func assertGameWireSample(t *testing.T, name string, body []byte) {
	t.Helper()
	previous, err := os.ReadFile(filepath.Join("internal", "game", "fishing", "runtime", "testdata", "contracts", name))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err = json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(previous, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed\ngot: %s\nwant: %s", name, body, previous)
	}
}
