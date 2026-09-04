package adminusers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const adminUsersTestNow = int64(2_000_000_000)

type testCursorKeys struct{}

func (testCursorKeys) DeriveGenerationTwoSubkey([]byte) ([]byte, error) {
	return []byte("0123456789abcdef0123456789abcdef"), nil
}

type testAdminAuth struct {
	err   error
	calls int
}

func (auth *testAdminAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error {
	auth.calls++
	return auth.err
}

type testInvalidator struct{ users []int64 }

func (sink *testInvalidator) InvalidateUserAuthority(userID int64) {
	sink.users = append(sink.users, userID)
}

type registeredRoute struct {
	method, pattern string
	handler         AuthorizedAdminHandler
}

type testRegistrar struct {
	routes []registeredRoute
	failAt string
}

func (registrar *testRegistrar) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	if registrar.failAt == method+" "+pattern {
		return errors.New("register failure")
	}
	registrar.routes = append(registrar.routes, registeredRoute{method: method, pattern: pattern, handler: handler})
	return nil
}

func (registrar *testRegistrar) handler(method, pattern string) AuthorizedAdminHandler {
	for _, route := range registrar.routes {
		if route.method == method && route.pattern == pattern {
			return route.handler
		}
	}
	return nil
}

type adminUsersFixture struct {
	t           *testing.T
	store       *db.Store
	service     *Service
	registrar   *testRegistrar
	auth        *testAdminAuth
	invalidator *testInvalidator
	adminID     int64
	nextSecret  uint64
}

func newAdminUsersFixture(t *testing.T) *adminUsersFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "adminusers.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fixture := &adminUsersFixture{t: t, store: store, auth: &testAdminAuth{}, invalidator: &testInvalidator{}, registrar: &testRegistrar{}}
	service, err := NewService(ServiceConfig{
		Database: store.DB(), CursorKeys: testCursorKeys{}, FinalAuth: fixture.auth,
		Invalidator: fixture.invalidator, Now: func() time.Time { return time.Unix(adminUsersTestNow, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = service
	fixture.adminID = fixture.seedUser("administrator", true)
	if err := RegisterRoutes(fixture.registrar, fixture.service); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *adminUsersFixture) seedUser(label string, admin bool) int64 {
	fixture.t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	one := db.U128{}
	one[15] = 1
	adminValue := 0
	if admin {
		adminValue = 1
	}
	result, err := tx.Exec(`
INSERT INTO users(
 discord_id,username,avatar,guild_nick,guild_avatar_url,is_admin,donation_credit_mag,
 total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"discord-"+label, label, "avatar-"+label, "nick-"+label, "https://guild.example/"+label,
		adminValue, zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), adminUsersTestNow, adminUsersTestNow)
	if err != nil {
		fixture.t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(context.Background(), tx, userID, adminUsersTestNow); err != nil {
		fixture.t.Fatal(err)
	}
	if !admin {
		hash := sha256.Sum256([]byte(label))
		if _, err := tx.Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,0,?,'nbk_','tail',?,?)`, userID, hash[:], adminUsersTestNow, adminUsersTestNow); err != nil {
			fixture.t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
	return userID
}

func (fixture *adminUsersFixture) revision(userID int64) string {
	fixture.t.Helper()
	var raw []byte
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM users WHERE id=?`, userID).Scan(&raw); err != nil {
		fixture.t.Fatal(err)
	}
	value, err := db.DecodeU128(raw)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return value.Decimal()
}

func (fixture *adminUsersFixture) request(method, pattern, target, body string, userID int64, key string) *httptest.ResponseRecorder {
	fixture.t.Helper()
	handler := fixture.registrar.handler(method, pattern)
	if handler == nil {
		fixture.t.Fatalf("missing route %s %s", method, pattern)
	}
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if userID > 0 {
		request.SetPathValue("id", strconv.FormatInt(userID, 10))
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, AdminPrincipal{UserID: fixture.adminID})
	return recorder
}

func (fixture *adminUsersFixture) addSession(userID int64, token string) {
	fixture.t.Helper()
	if _, err := fixture.store.DB().Exec(`
INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
VALUES(?,?,?,?,?,?,?)`, token, userID, adminUsersTestNow, adminUsersTestNow+3600, adminUsersTestNow+7200, adminUsersTestNow, "generation"); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *adminUsersFixture) seedEndpoint(userID int64, baseURL string, enabled bool, keys int) {
	fixture.t.Helper()
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}
	result, err := fixture.store.DB().Exec(`
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private note',?,1,?,?)`, userID, baseURL, enabledValue, adminUsersTestNow, adminUsersTestNow)
	if err != nil {
		fixture.t.Fatal(err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		fixture.t.Fatal(err)
	}
	for index := 0; index < keys; index++ {
		fixture.nextSecret++
		contextID := make([]byte, 16)
		binary.BigEndian.PutUint64(contextID[8:], fixture.nextSecret)
		secretResult, err := fixture.store.DB().Exec(`
INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','sealed-test-value',?)`, contextID, baseURL, adminUsersTestNow)
		if err != nil {
			fixture.t.Fatal(err)
		}
		secretID, _ := secretResult.LastInsertId()
		fingerprint := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", endpointID, index)))
		if _, err := fixture.store.DB().Exec(`
INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','private key note',1,0,1,?,?)`, endpointID, secretID, fingerprint[:], adminUsersTestNow, adminUsersTestNow); err != nil {
			fixture.t.Fatal(err)
		}
	}
}

func u128FromBig(t *testing.T, value *big.Int) []byte {
	t.Helper()
	encoded, err := db.U128FromBig(value)
	if err != nil {
		t.Fatal(err)
	}
	return db.EncodeU128(encoded)
}

func responseCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

func TestRegisterRoutesOwnsOnlyFrozenCorrectiveSurface(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	if len(fixture.registrar.routes) != 8 {
		t.Fatalf("registered routes=%d", len(fixture.registrar.routes))
	}
	want := map[string]bool{
		"GET " + routeUsers: true, "GET " + routeUser: true, "PATCH " + routeUser: true,
		"POST " + routeBan: true, "POST " + routeUnban: true, "GET " + routeUsage: true,
		"GET " + routeActivity: true, "GET " + routeEndpointOverview: true,
	}
	for _, route := range fixture.registrar.routes {
		key := route.method + " " + route.pattern
		if !want[key] {
			t.Fatalf("unexpected route %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 || fixture.registrar.handler(http.MethodDelete, routeUser) != nil {
		t.Fatalf("missing=%v or DELETE was registered", want)
	}

	failing := &testRegistrar{failAt: "GET " + routeUsage}
	if err := RegisterRoutes(failing, fixture.service); err == nil || !strings.Contains(err.Error(), "GET "+routeUsage) {
		t.Fatalf("registration error=%v", err)
	}
}

func TestAdminUserProjectionStrictQueryAndBoundCursor(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	firstID := fixture.seedUser("用户_alpha", false)
	secondID := fixture.seedUser("用户_beta", false)
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	large := new(big.Int).Sub(max, big.NewInt(2))
	if _, err := fixture.store.DB().Exec(`
UPDATE users SET donation_credit_mag=?,total_requests=?,total_uncached_input_tokens=?,
 total_cache_write_input_tokens=?,total_cache_read_input_tokens=?,total_output_tokens=?,
 total_unknown_usage_requests=?,is_banned=1,banned_reason='expired',banned_until=?,lang='en'
WHERE id=?`, u128FromBig(t, big.NewInt(1250)), u128FromBig(t, max), u128FromBig(t, large),
		u128FromBig(t, big.NewInt(2)), u128FromBig(t, big.NewInt(3)), u128FromBig(t, max),
		u128FromBig(t, big.NewInt(4)), adminUsersTestNow-1, firstID); err != nil {
		t.Fatal(err)
	}

	recorder := fixture.request(http.MethodGet, routeUsers, "https://admin.example/admin/api/users?q="+url.QueryEscape("用户")+"&limit=1", "", 0, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page Page[AdminUser]
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != strconv.FormatInt(firstID, 10) || page.NextCursor == nil {
		t.Fatalf("page=%+v", page)
	}
	user := page.Data[0]
	if user.IsAdmin || user.IsBanned || user.BannedReason != "" || user.BannedUntil != nil || user.DiscordID == nil || user.AvatarURL == nil {
		t.Fatalf("identity projection=%+v", user)
	}
	if user.Usage.TotalRequests != max.String() || user.Usage.TotalPromptTokens != new(big.Int).Add(large, big.NewInt(5)).String() || user.DonationCredit != "1.25" {
		t.Fatalf("wide projection=%+v", user)
	}
	encoded, _ := json.Marshal(user)
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil || len(fields) != 26 {
		t.Fatalf("AdminUser fields=%v", fields)
	}
	var usageFields map[string]json.RawMessage
	_ = json.Unmarshal(fields["usage"], &usageFields)
	var levelFields map[string]json.RawMessage
	_ = json.Unmarshal(fields["level"], &levelFields)
	if len(usageFields) != 8 || len(levelFields) != 4 {
		t.Fatalf("usage=%v level=%v", usageFields, levelFields)
	}

	next := fixture.request(http.MethodGet, routeUsers, "https://admin.example/admin/api/users?q="+url.QueryEscape("用户")+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", 0, "")
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), strconv.FormatInt(secondID, 10)) {
		t.Fatalf("next status=%d body=%s", next.Code, next.Body.String())
	}
	mismatch := fixture.request(http.MethodGet, routeUsers, "https://admin.example/admin/api/users?q=alpha&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", 0, "")
	if mismatch.Code != http.StatusBadRequest || responseCode(t, mismatch) != "invalid_request" {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
	tampered := fixture.request(http.MethodGet, routeUsers, "https://admin.example/admin/api/users?q="+url.QueryEscape("用户")+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor+"A"), "", 0, "")
	if tampered.Code != http.StatusBadRequest {
		t.Fatalf("tampered cursor status=%d body=%s", tampered.Code, tampered.Body.String())
	}

	for name, target := range map[string]string{
		"duplicate": "https://admin.example/admin/api/users?limit=1&limit=2",
		"unknown":   "https://admin.example/admin/api/users?extra=1",
		"empty q":   "https://admin.example/admin/api/users?q=",
		"control q": "https://admin.example/admin/api/users?q=%0A",
	} {
		t.Run(name, func(t *testing.T) {
			got := fixture.request(http.MethodGet, routeUsers, target, "", 0, "")
			if got.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
			}
		})
	}
	bodyOnGet := fixture.request(http.MethodGet, routeUser, "https://admin.example/admin/api/users/1", "{}", firstID, "")
	if bodyOnGet.Code != http.StatusBadRequest {
		t.Fatalf("GET body status=%d", bodyOnGet.Code)
	}
	missing := fixture.request(http.MethodGet, routeUser, "https://admin.example/admin/api/users/999999", "", 999999, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestProfileAndEconomyCASReplayLedgerAndRollback(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	userID := fixture.seedUser("mutation", false)
	profileKey := "ABCDEFGHIJKLMNOPQRSTUV"
	profileBody := `{"mode":"profile","expected_revision":"1","endpoint_limit":null,"rpm_limit":"100","lang":"en","level":5}`
	profile := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", profileBody, userID, profileKey)
	if profile.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", profile.Code, profile.Body.String())
	}
	var projected AdminUser
	if err := json.Unmarshal(profile.Body.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Revision != "2" || projected.RPMLimit == nil || *projected.RPMLimit != "100" || projected.Level.Manual == nil || *projected.Level.Manual != 5 {
		t.Fatalf("profile=%+v", projected)
	}
	if fmt.Sprint(fixture.invalidator.users) != fmt.Sprint([]int64{userID}) {
		t.Fatalf("invalidations=%v", fixture.invalidator.users)
	}
	semanticReplayBody := strings.Replace(profileBody, `"profile"`, `"\u0070rofile"`, 1)
	replay := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", semanticReplayBody, userID, profileKey)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), profile.Body.Bytes()) {
		t.Fatalf("profile replay=%d %s", replay.Code, replay.Body.String())
	}
	if len(fixture.invalidator.users) != 1 {
		t.Fatalf("replay invalidations=%v", fixture.invalidator.users)
	}
	mismatch := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", strings.Replace(profileBody, `"lang":"en"`, `"lang":"zh"`, 1), userID, profileKey)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("profile mismatch=%d %s", mismatch.Code, mismatch.Body.String())
	}

	for name, body := range map[string]string{
		"mixed union":   `{"mode":"profile","expected_revision":"2","lang":"en","amount":"1"}`,
		"empty profile": `{"mode":"profile","expected_revision":"2"}`,
		"raw balance":   `{"mode":"economy","expected_revision":"2","target":"balance","direction":"increase","amount":"1","reason":"ok","after":"2"}`,
		"number limit":  `{"mode":"profile","expected_revision":"2","rpm_limit":100}`,
		"duplicate":     `{"mode":"profile","mode":"economy","expected_revision":"2","lang":"en"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, userID, "ABCDEFGHIJKLMNOPQRSTU0")
			if got.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
			}
		})
	}

	economyKey := "ABCDEFGHIJKLMNOPQRSTU1"
	economyBody := `{"mode":"economy","expected_revision":"2","target":"balance","direction":"decrease","amount":"1.25","reason":"manual correction"}`
	economy := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", economyBody, userID, economyKey)
	if economy.Code != http.StatusOK {
		t.Fatalf("economy status=%d body=%s", economy.Code, economy.Body.String())
	}
	if json.Unmarshal(economy.Body.Bytes(), &projected) != nil || projected.Balance != "-1.25" || projected.Revision != "3" {
		t.Fatalf("economy projection=%+v", projected)
	}
	var operations int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='admin_user_adjustment'`).Scan(&operations); err != nil || operations != 1 {
		t.Fatalf("operations=%d err=%v", operations, err)
	}
	tx, err := fixture.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	userAccount, userErr := ledger.UserAccount(context.Background(), tx, userID)
	externalAccount, externalErr := ledger.CodedAccount(context.Background(), tx, "external")
	_ = tx.Rollback()
	if userErr != nil || externalErr != nil || new(big.Int).Add(userAccount.Balance.Big(), externalAccount.Balance.Big()).Sign() != 0 {
		t.Fatalf("ledger conservation user=%s external=%s errors=%v/%v", userAccount.Balance.Decimal(), externalAccount.Balance.Decimal(), userErr, externalErr)
	}
	economyReplay := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", economyBody, userID, economyKey)
	if economyReplay.Code != http.StatusOK || !bytes.Equal(economyReplay.Body.Bytes(), economy.Body.Bytes()) {
		t.Fatalf("economy replay=%d %s", economyReplay.Code, economyReplay.Body.String())
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='admin_user_adjustment'`).Scan(&operations); err != nil || operations != 1 {
		t.Fatalf("replay operations=%d err=%v", operations, err)
	}

	donation := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1",
		`{"mode":"economy","expected_revision":"3","target":"donation_credit","direction":"decrease","amount":"0.001","reason":"cannot underflow"}`,
		userID, "ABCDEFGHIJKLMNOPQRSTU2")
	if donation.Code != http.StatusConflict || fixture.revision(userID) != "3" {
		t.Fatalf("donation underflow=%d revision=%s body=%s", donation.Code, fixture.revision(userID), donation.Body.String())
	}
	cas := fixture.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1",
		`{"mode":"economy","expected_revision":"1","target":"balance","direction":"increase","amount":"1","reason":"stale"}`,
		userID, "ABCDEFGHIJKLMNOPQRSTU3")
	if cas.Code != http.StatusConflict {
		t.Fatalf("CAS status=%d body=%s", cas.Code, cas.Body.String())
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='admin_user_adjustment'`).Scan(&operations); err != nil || operations != 1 {
		t.Fatalf("rollback operations=%d err=%v", operations, err)
	}

	t.Run("MAX primitive accepted", func(t *testing.T) {
		maximum := newAdminUsersFixture(t)
		id := maximum.seedUser("maximum", false)
		got := maximum.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1",
			`{"mode":"economy","expected_revision":"1","target":"balance","direction":"increase","amount":"9000000000000","reason":"maximum"}`,
			id, "ABCDEFGHIJKLMNOPQRSTU4")
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"balance":"9000000000000"`) {
			t.Fatalf("MAX status=%d body=%s", got.Code, got.Body.String())
		}
	})

	t.Run("donation promotion invalidates authority once after commit", func(t *testing.T) {
		promoted := newAdminUsersFixture(t)
		id := promoted.seedUser("promotion", false)
		for level, threshold := range map[int]string{2: "1000", 3: "2000", 4: "3000"} {
			if _, err := promoted.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, threshold, "level_threshold_"+strconv.Itoa(level)+"_milli"); err != nil {
				t.Fatal(err)
			}
		}
		body := `{"mode":"economy","expected_revision":"1","target":"donation_credit","direction":"increase","amount":"3","reason":"promotion"}`
		got := promoted.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, id, "PROMOTEPROMOTEPROMOTE12")
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"automatic":4`) || fmt.Sprint(promoted.invalidator.users) != fmt.Sprint([]int64{id}) {
			t.Fatalf("promotion status=%d invalidations=%v body=%s", got.Code, promoted.invalidator.users, got.Body.String())
		}
		replay := promoted.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1", body, id, "PROMOTEPROMOTEPROMOTE12")
		if replay.Code != http.StatusOK || len(promoted.invalidator.users) != 1 {
			t.Fatalf("promotion replay status=%d invalidations=%v body=%s", replay.Code, promoted.invalidator.users, replay.Body.String())
		}
	})

	t.Run("capacity failure rolls back ledger and revision", func(t *testing.T) {
		capacity := newAdminUsersFixture(t)
		id := capacity.seedUser("capacity", false)
		if _, err := capacity.store.DB().Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		got := capacity.request(http.MethodPatch, routeUser, "https://admin.example/admin/api/users/1",
			`{"mode":"economy","expected_revision":"1","target":"balance","direction":"increase","amount":"1","reason":"capacity"}`,
			id, "ABCDEFGHIJKLMNOPQRSTU5")
		if got.Code != http.StatusServiceUnavailable || capacity.revision(id) != "1" {
			t.Fatalf("capacity status=%d revision=%s body=%s", got.Code, capacity.revision(id), got.Body.String())
		}
		tx, _ := capacity.store.DB().Begin()
		account, err := ledger.UserAccount(context.Background(), tx, id)
		_ = tx.Rollback()
		if err != nil || account.Balance.Big().Sign() != 0 {
			t.Fatalf("balance=%v err=%v", account.Balance.Decimal(), err)
		}
	})
}

func TestBanUnbanAtomicRevocationReplayAndFinalAuthorization(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	userID := fixture.seedUser("ban", false)
	fixture.addSession(userID, "user-session")
	key := "BANBANBANBANBANBANBANB1"
	body := `{"expected_revision":"1","reason":"policy violation","duration_seconds":3600}`
	ban := fixture.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", body, userID, key)
	if ban.Code != http.StatusNoContent || ban.Body.Len() != 0 {
		t.Fatalf("ban status=%d body=%s", ban.Code, ban.Body.String())
	}
	var sessions, generation int
	var keyHash []byte
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, userID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT generation,key_hash FROM caller_keys WHERE user_id=?`, userID).Scan(&generation, &keyHash); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || generation != 1 || keyHash != nil || fixture.revision(userID) != "2" || fmt.Sprint(fixture.invalidator.users) != fmt.Sprint([]int64{userID}) {
		t.Fatalf("sessions=%d generation=%d key=%x revision=%s invalidations=%v", sessions, generation, keyHash, fixture.revision(userID), fixture.invalidator.users)
	}
	replay := fixture.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", body, userID, key)
	if replay.Code != http.StatusNoContent {
		t.Fatalf("replay=%d body=%s", replay.Code, replay.Body.String())
	}
	if err := fixture.store.DB().QueryRow(`SELECT generation FROM caller_keys WHERE user_id=?`, userID).Scan(&generation); err != nil || generation != 1 || len(fixture.invalidator.users) != 1 {
		t.Fatalf("replay generation=%d invalidations=%v err=%v", generation, fixture.invalidator.users, err)
	}
	different := fixture.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", strings.Replace(body, "3600", "3601", 1), userID, key)
	if different.Code != http.StatusConflict {
		t.Fatalf("different replay=%d body=%s", different.Code, different.Body.String())
	}
	unbanKey := "UNBANUNBANUNBANUNBAN12"
	unbanBody := `{"expected_revision":"2"}`
	unban := fixture.request(http.MethodPost, routeUnban, "https://admin.example/admin/api/users/1/unban", unbanBody, userID, unbanKey)
	if unban.Code != http.StatusNoContent || fixture.revision(userID) != "3" {
		t.Fatalf("unban=%d revision=%s body=%s", unban.Code, fixture.revision(userID), unban.Body.String())
	}
	unbanReplay := fixture.request(http.MethodPost, routeUnban, "https://admin.example/admin/api/users/1/unban", unbanBody, userID, unbanKey)
	if unbanReplay.Code != http.StatusNoContent || fixture.revision(userID) != "3" {
		t.Fatalf("unban replay=%d revision=%s body=%s", unbanReplay.Code, fixture.revision(userID), unbanReplay.Body.String())
	}
	var banned int
	if err := fixture.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, userID).Scan(&banned); err != nil || banned != 0 {
		t.Fatalf("banned=%d err=%v", banned, err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT generation,key_hash FROM caller_keys WHERE user_id=?`, userID).Scan(&generation, &keyHash); err != nil || generation != 1 || keyHash != nil {
		t.Fatalf("unban caller key generation=%d hash=%x err=%v", generation, keyHash, err)
	}

	t.Run("final authorization denial leaves state untouched", func(t *testing.T) {
		denied := newAdminUsersFixture(t)
		id := denied.seedUser("denied", false)
		denied.addSession(id, "denied-session")
		denied.auth.err = authz.ErrForbidden
		got := denied.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban",
			`{"expected_revision":"1","reason":"denied","duration_seconds":null}`, id, "DENIEDDENIEDDENIEDDENI1")
		if got.Code != http.StatusForbidden || denied.revision(id) != "1" || len(denied.invalidator.users) != 0 {
			t.Fatalf("status=%d revision=%s invalidations=%v body=%s", got.Code, denied.revision(id), denied.invalidator.users, got.Body.String())
		}
		var count int
		if err := denied.store.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("sessions=%d err=%v", count, err)
		}
	})

	t.Run("caller generation exhaustion rolls back account and session", func(t *testing.T) {
		exhausted := newAdminUsersFixture(t)
		id := exhausted.seedUser("exhausted", false)
		exhausted.addSession(id, "exhausted-session")
		if _, err := exhausted.store.DB().Exec(`DELETE FROM caller_keys WHERE user_id=?`, id); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte("exhausted"))
		if _, err := exhausted.store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,?,?,'head','tail',?,?)`, id, int64(math.MaxInt64), hash[:], adminUsersTestNow, adminUsersTestNow); err != nil {
			t.Fatal(err)
		}
		got := exhausted.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban",
			`{"expected_revision":"1","reason":"rollback","duration_seconds":null}`, id, "EXHAUSTEXHAUSTEXHAUST1")
		if got.Code != http.StatusInternalServerError || exhausted.revision(id) != "1" || len(exhausted.invalidator.users) != 0 {
			t.Fatalf("status=%d revision=%s invalidations=%v body=%s", got.Code, exhausted.revision(id), exhausted.invalidator.users, got.Body.String())
		}
		var count, isBanned int
		if err := exhausted.store.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := exhausted.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, id).Scan(&isBanned); err != nil || count != 1 || isBanned != 0 {
			t.Fatalf("sessions=%d banned=%d err=%v", count, isBanned, err)
		}
	})
}

func TestUsageActivityAndEndpointOverviewContracts(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	first := fixture.seedUser("observe-one", false)
	second := fixture.seedUser("observe-two", false)
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	if _, err := fixture.store.DB().Exec(`
UPDATE site_usage_totals SET total_requests=?,total_uncached_input_tokens=?,total_cache_write_input_tokens=?,
 total_cache_read_input_tokens=?,total_output_tokens=?,total_unknown_usage_requests=? WHERE id=1`,
		u128FromBig(t, max), u128FromBig(t, max), u128FromBig(t, max), u128FromBig(t, max), u128FromBig(t, max), u128FromBig(t, max)); err != nil {
		t.Fatal(err)
	}
	site := fixture.request(http.MethodGet, routeUsage, "https://admin.example/admin/api/usage?group_by=site", "", 0, "")
	if site.Code != http.StatusOK {
		t.Fatalf("site usage=%d body=%s", site.Code, site.Body.String())
	}
	var summary UsageSummary
	if json.Unmarshal(site.Body.Bytes(), &summary) != nil || summary.TotalRequests != max.String() || summary.TotalPromptTokens != new(big.Int).Mul(max, big.NewInt(3)).String() {
		t.Fatalf("site summary=%+v", summary)
	}
	badSite := fixture.request(http.MethodGet, routeUsage, "https://admin.example/admin/api/usage?group_by=site&limit=1", "", 0, "")
	if badSite.Code != http.StatusBadRequest {
		t.Fatalf("site limit=%d", badSite.Code)
	}
	users := fixture.request(http.MethodGet, routeUsage, "https://admin.example/admin/api/usage?group_by=user&limit=1", "", 0, "")
	var usagePage Page[AdminUserUsage]
	if users.Code != http.StatusOK || json.Unmarshal(users.Body.Bytes(), &usagePage) != nil || len(usagePage.Data) != 1 || usagePage.Data[0].UserID != strconv.FormatInt(first, 10) || usagePage.NextCursor == nil {
		t.Fatalf("user usage=%d page=%+v body=%s", users.Code, usagePage, users.Body.String())
	}
	userNext := fixture.request(http.MethodGet, routeUsage, "https://admin.example/admin/api/usage?group_by=user&limit=1&cursor="+url.QueryEscape(*usagePage.NextCursor), "", 0, "")
	if userNext.Code != http.StatusOK || !strings.Contains(userNext.Body.String(), strconv.FormatInt(second, 10)) {
		t.Fatalf("user usage next=%d body=%s", userNext.Code, userNext.Body.String())
	}

	if _, err := fixture.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',?)`, adminUsersTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='activities_enabled'`); err != nil {
		t.Fatal(err)
	}
	day, err := fixture.store.SiteDayKeyAt(adminUsersTestNow)
	if err != nil {
		t.Fatal(err)
	}
	zero := db.EncodeU128(db.U128{})
	insertDay := func(day int64, distinct int64) {
		t.Helper()
		value := u128FromBig(t, big.NewInt(distinct))
		if _, err := fixture.store.DB().Exec(`
INSERT INTO site_activity_daily(day,product_active,api_requests,uncached_input_tokens,
 cache_write_input_tokens,cache_read_input_tokens,output_tokens,checkins,console_writes,
 game_active,game_rounds,distinct_product_users,updated_at)
VALUES(?,1,?,?,?,?,?,?,?,1,?,?,?)`, day, value, zero, zero, zero, zero, zero, zero, value, value, adminUsersTestNow); err != nil {
			t.Fatal(err)
		}
	}
	insertDay(day-86400, 4)
	insertDay(day, 5)
	activity := fixture.request(http.MethodGet, routeActivity, "https://admin.example/admin/api/activity?limit=1", "", 0, "")
	var activityPage ActivityPage
	if activity.Code != http.StatusOK || json.Unmarshal(activity.Body.Bytes(), &activityPage) != nil || !activityPage.Enabled || len(activityPage.Data) != 1 || activityPage.Data[0].DistinctProductUsers == nil || *activityPage.Data[0].DistinctProductUsers != "5" || activityPage.NextCursor == nil {
		t.Fatalf("activity=%d page=%+v body=%s", activity.Code, activityPage, activity.Body.String())
	}
	activityNext := fixture.request(http.MethodGet, routeActivity, "https://admin.example/admin/api/activity?limit=1&cursor="+url.QueryEscape(*activityPage.NextCursor), "", 0, "")
	if activityNext.Code != http.StatusOK || json.Unmarshal(activityNext.Body.Bytes(), &activityPage) != nil || len(activityPage.Data) != 1 || activityPage.Data[0].DistinctProductUsers != nil {
		t.Fatalf("activity next=%d page=%+v body=%s", activityNext.Code, activityPage, activityNext.Body.String())
	}
	if _, err := fixture.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='activities_enabled'`); err != nil {
		t.Fatal(err)
	}
	disabled := fixture.request(http.MethodGet, routeActivity, "https://admin.example/admin/api/activity", "", 0, "")
	if disabled.Code != http.StatusOK || disabled.Body.String() != `{"enabled":false,"data":[],"next_cursor":null}`+"\n" {
		t.Fatalf("disabled=%d body=%q", disabled.Code, disabled.Body.String())
	}

	fixture.seedEndpoint(first, "https://literal.example/%_a*?x=y", true, 2)
	fixture.seedEndpoint(first, "https://page.example/a", false, 0)
	fixture.seedEndpoint(second, "https://page.example/a", true, 1)
	fixture.seedEndpoint(second, "https://page.example/b", true, 0)
	for _, literal := range []string{"%", "_", "*", "?"} {
		got := fixture.request(http.MethodGet, routeEndpointOverview, "https://admin.example/admin/api/overview/endpoints?q="+url.QueryEscape(literal), "", 0, "")
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "literal.example") {
			t.Fatalf("literal %q status=%d body=%s", literal, got.Code, got.Body.String())
		}
	}
	overview := fixture.request(http.MethodGet, routeEndpointOverview, "https://admin.example/admin/api/overview/endpoints?q="+url.QueryEscape("page.example")+"&limit=1", "", 0, "")
	var endpointPage Page[EndpointOverview]
	if overview.Code != http.StatusOK || json.Unmarshal(overview.Body.Bytes(), &endpointPage) != nil || len(endpointPage.Data) != 1 || endpointPage.NextCursor == nil {
		t.Fatalf("overview=%d page=%+v body=%s", overview.Code, endpointPage, overview.Body.String())
	}
	row := endpointPage.Data[0]
	if row.UserCount != "2" || row.EndpointCount != "2" || row.KeyCount != "1" || len(row.Users) != 2 || row.Users[0].EnabledCount != "0" || row.Users[1].EnabledCount != "1" {
		t.Fatalf("overview row=%+v", row)
	}
	if strings.Contains(overview.Body.String(), "private") || strings.Contains(overview.Body.String(), "sealed") {
		t.Fatalf("overview leaked private fields: %s", overview.Body.String())
	}
	bound := fixture.request(http.MethodGet, routeEndpointOverview, "https://admin.example/admin/api/overview/endpoints?q=literal&limit=1&cursor="+url.QueryEscape(*endpointPage.NextCursor), "", 0, "")
	if bound.Code != http.StatusBadRequest {
		t.Fatalf("bound cursor=%d body=%s", bound.Code, bound.Body.String())
	}

	t.Run("100 users succeeds and 101 fails the whole group", func(t *testing.T) {
		large := newAdminUsersFixture(t)
		for index := 0; index < 100; index++ {
			id := large.seedUser(fmt.Sprintf("wide-%03d", index), false)
			large.seedEndpoint(id, "https://wide.example/v1", true, 0)
		}
		accepted := large.request(http.MethodGet, routeEndpointOverview, "https://admin.example/admin/api/overview/endpoints", "", 0, "")
		var acceptedPage Page[EndpointOverview]
		if accepted.Code != http.StatusOK || json.Unmarshal(accepted.Body.Bytes(), &acceptedPage) != nil || len(acceptedPage.Data) != 1 || len(acceptedPage.Data[0].Users) != 100 {
			t.Fatalf("100 users status=%d page=%+v body=%s", accepted.Code, acceptedPage, accepted.Body.String())
		}
		lastID := large.seedUser("wide-100", false)
		large.seedEndpoint(lastID, "https://wide.example/v1", true, 0)
		got := large.request(http.MethodGet, routeEndpointOverview, "https://admin.example/admin/api/overview/endpoints", "", 0, "")
		if got.Code != http.StatusRequestEntityTooLarge || responseCode(t, got) != "payload_too_large" {
			t.Fatalf("wide status=%d body=%s", got.Code, got.Body.String())
		}
	})
}
