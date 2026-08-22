package logapi

// HTTP-level tests for the redesigned user log screen, the bounded options
// endpoint, the administrator CSV/JSON export, and the level-5 steward
// full-site log route: cross-user isolation over real session middlewares,
// strict query-parameter rejection, CSV formula injection defense, fail-closed
// export bounds, and the steward de-privacy projection behind its live level-5
// gate.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/steward"
)

func userGet(t *testing.T, env *testEnv, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := newUserMount(t, env)
	r := stationRequest(http.MethodGet, path, host.StationUser)
	r.AddCookie(userCookie(t, env))
	return do(t, h, r)
}

func adminGet(t *testing.T, env *testEnv, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, path, host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	return do(t, h, r)
}

func TestUserLogsOwnRowsOnly(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	seedLog(t, env, env.user.ID, "u-own-1", "p/m1", 200, 1700000100, "")
	seedLog(t, env, env.user.ID, "u-own-2", "p/m2", 502, 1700000200, "boom")
	seedLog(t, env, user2.ID, "u-other-1", "q/m1", 200, 1700000300, "")

	rec := userGet(t, env, "/api/logs")
	assertOK(t, rec)
	rows, hasMore := decodeLogs(t, rec)
	if len(rows) != 2 || hasMore {
		t.Fatalf("rows = %d hasMore=%v, want 2/false", len(rows), hasMore)
	}
	for _, row := range rows {
		if id := int64(row["id"].(float64)); id != 1 && id != 2 {
			t.Fatalf("foreign row leaked: %v", row)
		}
	}
	if rows[0]["model"] != "p/m2" || rows[0]["error_code"] != "upstream" || rows[0]["error_source"] != "platform" {
		t.Fatalf("newest-first projection wrong: %v", rows[0])
	}

	// Frozen user row shape: own model and notes present, no user_id.
	assertJSONKeys(t, mustJSON(t, rows[0]), "id", "route_kind", "model", "endpoint_key_id",
		"key_note", "endpoint_note", "endpoint_base_url", "upstream_model_id", "status_code",
		"duration_ms", "started_at", "completed_at", "uncached_input_tokens",
		"cache_write_input_tokens", "cache_read_input_tokens", "output_tokens",
		"prompt_tokens", "completion_tokens", "total_tokens", "usage_unknown",
		"error_code", "error_source", "error_diag", "attempt_id")
	for _, forbidden := range []string{"user_id", "note"} {
		if _, ok := rows[0][forbidden]; ok {
			t.Errorf("user log row carries forbidden key %q", forbidden)
		}
	}

	// Filters work on the user station.
	rec = userGet(t, env, "/api/logs?status=502")
	assertOK(t, rec)
	rows, _ = decodeLogs(t, rec)
	if len(rows) != 1 || rows[0]["id"].(float64) != 2 {
		t.Fatalf("status filter rows = %v", rows)
	}
	rec = userGet(t, env, "/api/logs?model=p%2Fm1")
	assertOK(t, rec)
	rows, _ = decodeLogs(t, rec)
	if len(rows) != 1 {
		t.Fatalf("model filter rows = %v", rows)
	}
	rec = userGet(t, env, fmt.Sprintf("/api/logs?page_size=1&page=1"))
	assertOK(t, rec)
	rows, hasMore = decodeLogs(t, rec)
	if len(rows) != 1 || !hasMore {
		t.Fatalf("paged rows = %v hasMore=%v", rows, hasMore)
	}
}

func TestUserLogsStrictQueryMatrix(t *testing.T) {
	env := newTestEnv(t)
	bad := []string{
		"/api/logs?unknown=1",
		"/api/logs?user_id=1",           // ownership comes from the session only
		"/api/logs?endpoint_base_url=x", // admin-only filter
		"/api/logs?upstream_model=x",    // admin-only filter
		"/api/logs?page=1&page=2",       // repeated
		"/api/logs?page_size=0",
		"/api/logs?page_size=-3",
		"/api/logs?page_size=abc",
		"/api/logs?page=abc",
		"/api/logs?page=0",
		"/api/logs?status=99",
		"/api/logs?status=600",
		"/api/logs?from=-1",
		"/api/logs?from=30&to=20",
		"/api/logs?model=",
		"/api/logs?model=%09tab",     // control character
		"/api/logs/options?filter=x", // options accepts no parameter at all
		"/api/logs/options?models=a&models=b",
	}
	for _, path := range bad {
		rec := userGet(t, env, path)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body=%s", path, rec.Code, rec.Body.String())
			continue
		}
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
}

func TestUserLogOptionsBoundedList(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	for i, m := range []string{"a/model", "b/model"} {
		input := db.RequestLogInput{
			AttemptID: fmt.Sprintf("opt-http-%d", i), UserID: env.user.ID, Model: m,
			StatusCode: 200, DurationMs: 1,
			StartedAt:   time.Unix(1700000000+int64(i), 0).UTC(),
			CompletedAt: time.Unix(1700000000+int64(i), 0).UTC(),
		}
		if err := env.store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest: %v", err)
		}
	}
	input := db.RequestLogInput{
		AttemptID: "opt-http-other", UserID: user2.ID, Model: "z/model",
		StatusCode: 200, DurationMs: 1,
		StartedAt: time.Unix(1700000010, 0).UTC(), CompletedAt: time.Unix(1700000010, 0).UTC(),
	}
	if err := env.store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest other: %v", err)
	}

	rec := userGet(t, env, "/api/logs/options")
	assertOK(t, rec)
	var body struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode options: %v; body=%s", err, rec.Body.String())
	}
	if len(body.Models) != 2 || body.Models[0] != "a/model" || body.Models[1] != "b/model" {
		t.Fatalf("options = %v, want own models ascending", body.Models)
	}
}

func TestAdminExportCSVInjectionDefense(t *testing.T) {
	env := newTestEnv(t)
	hostile := []string{
		"=cmd|' /C calc'!A0",
		"+SUM(A1:A2)",
		"-2+3",
		"@IMPORT(x)",
		"\u200bzero width = cmd",
		"\ufeffbom = cmd",
	}
	// Control-character vectors cannot survive the write-path validation or
	// the diagnostic sink; they are covered directly against the sanitizer.
	for _, cell := range []string{"\t=cmd", "\r\n=cmd", "\n=cmd", "\u00a0=cmd"} {
		if got := sanitizeCSVCell(cell); got[0] != '\'' {
			t.Errorf("sanitizeCSVCell(%q) = %q, want quote prefix", cell, got)
		}
	}
	for i, h := range hostile {
		input := db.RequestLogInput{
			AttemptID: fmt.Sprintf("inj-%d", i), UserID: env.user.ID, Model: h,
			UpstreamModelID: h, StatusCode: 502, DurationMs: 1,
			StartedAt:   time.Unix(1700000000+int64(i), 0).UTC(),
			CompletedAt: time.Unix(1700000000+int64(i), 0).UTC(),
			ErrorCode:   "upstream", ErrorDiag: h,
		}
		if err := env.store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest %d: %v", i, err)
		}
	}

	rec := adminGet(t, env, "/admin/api/logs/export.csv")
	assertNoStore(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	body := rec.Body.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != len(hostile)+1 {
		t.Fatalf("csv lines = %d, want %d; body=%s", len(lines), len(hostile)+1, body)
	}
	// Every data cell must be inert: no cell may begin with a formula-trigger
	// character. The sanitizer prefixes such cells with a single quote.
	for _, line := range lines[1:] {
		for _, cell := range strings.Split(line, ",") {
			cell = strings.Trim(cell, "\"")
			if cell == "" {
				continue
			}
			switch cell[0] {
			case '=', '+', '-', '@', '\t', '\r', '\n':
				t.Errorf("unsanitized cell %q in line %s", cell, line)
			}
		}
	}
	if !strings.HasPrefix(lines[0], "id,started_at") {
		t.Errorf("csv header = %q", lines[0])
	}
}

func TestAdminExportJSONShapeAndHeaders(t *testing.T) {
	env := newTestEnv(t)
	seedLog(t, env, env.user.ID, "exp-json-1", "p/m1", 200, 1700000100, "")

	rec := adminGet(t, env, "/admin/api/logs/export.json")
	assertNoStore(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json export: %v; body=%s", err, rec.Body.String())
	}
	if len(body.Data) != 1 {
		t.Fatalf("export rows = %d, want 1", len(body.Data))
	}
	row := body.Data[0]
	if _, ok := row["model"]; ok {
		t.Errorf("json export leaks the user-chosen model name")
	}
	if row["user_id"].(float64) != float64(env.user.ID) || row["route_kind"] != "personal" {
		t.Errorf("export row = %v", row)
	}
}

func TestAdminExportStrictParamsAndFailClosed(t *testing.T) {
	env := newTestEnv(t)

	// The export is unpaginated: paging parameters are invalid_request.
	for _, path := range []string{
		"/admin/api/logs/export.csv?page=1",
		"/admin/api/logs/export.json?page_size=20",
		"/admin/api/logs/export.csv?format=csv",
		"/admin/api/logs/export.csv?unknown=1",
	} {
		rec := adminGet(t, env, path)
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Over-bound selection fails closed with payload_too_large and no
	// partial file: nothing is written before the whole payload passed its
	// bounds.
	total := db.MaxLogExportRows + 1
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < total; i++ {
		if _, err := tx.Exec(
			`INSERT INTO request_logs (attempt_id, user_id, status_code, started_at, completed_at) VALUES (?, ?, 200, ?, ?)`,
			fmt.Sprintf("bulk-exp-%d", i), env.user.ID, base.Add(time.Duration(i)*time.Second).Unix(),
			base.Add(time.Duration(i)*time.Second).Unix()); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	for _, path := range []string{"/admin/api/logs/export.csv", "/admin/api/logs/export.json"} {
		rec := adminGet(t, env, path)
		assertErr(t, rec, http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)
		if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
			t.Errorf("%s: error body is not a JSON envelope: %.80s", path, rec.Body.String())
		}
	}

	// Filters carry over to the export: an exact user_id filter selects only
	// that user's rows.
	user2 := seedSecondUser(t, env)
	seedLog(t, env, user2.ID, "exp-filtered-1", "p/m1", 200, 1700000100, "")
	rec := adminGet(t, env, "/admin/api/logs/export.csv?user_id="+fmt.Sprint(user2.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered export status = %d; body=%s", rec.Code, rec.Body.String())
	}
	lines := strings.Split(strings.TrimRight(rec.Body.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("filtered export lines = %d, want header + 1 row", len(lines))
	}
}

func TestStewardLogsRoute(t *testing.T) {
	env := newTestEnv(t)
	if StewardLogsPath != "/api/steward/logs" {
		t.Fatalf("StewardLogsPath = %q", StewardLogsPath)
	}
	user2 := seedSecondUser(t, env)

	// Three rows across two users: the steward must see the FULL site (all
	// users), newest first. The charity row carries the donor's upstream URL
	// and model; the steward projection must blank them.
	seedLog(t, env, env.user.ID, "steward-own-personal", "p/own", 200, 1700000100, "")
	seedLog(t, env, user2.ID, "steward-other-personal", "p/other", 502, 1700000200, "boom")
	seedLog(t, env, env.user.ID, "steward-charity", "", 200, 1700000300, "")
	if _, err := env.store.DB().Exec(`UPDATE request_logs SET endpoint_base_url='https://own.example/v1', upstream_model_id='own/up' WHERE attempt_id='steward-own-personal'`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`UPDATE request_logs SET endpoint_base_url='https://other.example/v1', upstream_model_id='other/up' WHERE attempt_id='steward-other-personal'`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`UPDATE request_logs SET route_kind='charity', endpoint_base_url='https://donor.example/v1', upstream_model_id='donor/up-model' WHERE attempt_id='steward-charity'`); err != nil {
		t.Fatal(err)
	}

	// Build the real steward frame with the logs sub-handler registered, exactly
	// as main.go mounts it.
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: env.store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	t.Cleanup(func() { _ = userAuth.Close() })
	frame := steward.New(steward.Deps{UserAuth: userAuth, Store: env.store})
	frame.Handle(http.MethodGet, StewardLogsPath, StewardLogsSub(env.store))

	stewardGet := func(t *testing.T, station host.Station, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := stationRequest(http.MethodGet, path, station)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		return do(t, frame.Handler(), r)
	}
	userCookieVal := func(t *testing.T, uid int64) *http.Cookie {
		t.Helper()
		tok, _, err := env.store.CreateUserSession(uid)
		if err != nil {
			t.Fatalf("CreateUserSession: %v", err)
		}
		return &http.Cookie{Name: auth.UserSessionCookieName, Value: tok}
	}
	setLevel := func(t *testing.T, uid int64, level *int) {
		t.Helper()
		if _, err := env.store.SetUserManualLevel(uid, level); err != nil {
			t.Fatalf("SetUserManualLevel: %v", err)
		}
	}
	codeOf := func(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
		t.Helper()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		return rec.Code, env.Error.Code
	}

	// Authorization matrix.
	cookie := userCookieVal(t, env.user.ID)
	if code, c := codeOf(t, stewardGet(t, host.StationUser, nil, StewardLogsPath)); code != http.StatusUnauthorized || c != "unauthorized" {
		t.Fatalf("anonymous = (%d, %s), want 401 unauthorized", code, c)
	}
	if code, c := codeOf(t, stewardGet(t, host.StationUser, cookie, StewardLogsPath)); code != http.StatusForbidden || c != "forbidden" {
		t.Fatalf("level<5 = (%d, %s), want 403 forbidden", code, c)
	}
	// Admin station is refused before identity (the prefix is user-station only).
	if code, c := codeOf(t, stewardGet(t, host.StationAdmin, cookie, StewardLogsPath)); code != http.StatusForbidden || c != "forbidden" {
		t.Fatalf("admin station = (%d, %s), want 403 forbidden", code, c)
	}
	// An administrator session on the user station is not a steward identity.
	adminTok, _, _ := env.store.CreateAdminSession(env.admin.ID)
	adminCookie := &http.Cookie{Name: auth.UserSessionCookieName, Value: adminTok}
	if code := stewardGet(t, host.StationUser, adminCookie, StewardLogsPath).Code; code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("admin session on user station = %d, want 401/403", code)
	}

	// Grant level 5 on the SAME session: the very next request opens the route.
	five := 5
	setLevel(t, env.user.ID, &five)
	rec := stewardGet(t, host.StationUser, cookie, StewardLogsPath)
	assertOK(t, rec)
	rows, hasMore := decodeLogs(t, rec)
	if len(rows) != 3 || hasMore {
		t.Fatalf("rows = %d hasMore=%v, want 3/false (full-site, newest first)", len(rows), hasMore)
	}
	// Newest first: steward-charity (1700000300), other-personal, own-personal.
	if rows[0]["route_kind"] != "charity" || rows[1]["route_kind"] != "personal" || rows[2]["route_kind"] != "personal" {
		t.Fatalf("row order wrong: %v %v %v", rows[0]["route_kind"], rows[1]["route_kind"], rows[2]["route_kind"])
	}

	// The charity row never reveals donor resources: endpoint_key_id=0,
	// endpoint_base_url="", upstream_model_id="". The personal rows keep their
	// own endpoint/upstream model.
	charityRow := rows[0]
	if charityRow["endpoint_key_id"].(float64) != 0 || charityRow["endpoint_base_url"] != "" || charityRow["upstream_model_id"] != "" {
		t.Fatalf("charity row leaks donor resource: %v", charityRow)
	}
	if charityRow["user_id"].(float64) != float64(env.user.ID) {
		t.Fatalf("charity row user_id = %v, want consumer %d", charityRow["user_id"], env.user.ID)
	}
	if charityRow["route_kind"] != "charity" || charityRow["status_code"].(float64) != 200 {
		t.Fatalf("charity row metadata wrong: %v", charityRow)
	}
	otherRow := rows[1]
	if otherRow["endpoint_base_url"] != "https://other.example/v1" || otherRow["upstream_model_id"] != "other/up" {
		t.Fatalf("personal row lost its own endpoint/upstream: %v", otherRow)
	}
	if otherRow["user_id"].(float64) != float64(user2.ID) {
		t.Fatalf("other row user_id = %v, want %d", otherRow["user_id"], user2.ID)
	}

	// Frozen de-privacy boundary: no user-chosen platform model name, no note,
	// no Discord identity ever appears on the steward projection.
	for _, row := range rows {
		for _, forbidden := range []string{"model", "note", "key_note", "endpoint_note", "discord_id", "username"} {
			if _, ok := row[forbidden]; ok {
				t.Errorf("steward row carries forbidden key %q: %v", forbidden, row)
			}
		}
	}

	// Filters and pagination work on the steward route too.
	rec = stewardGet(t, host.StationUser, cookie, StewardLogsPath+"?status=200")
	assertOK(t, rec)
	rows, _ = decodeLogs(t, rec)
	if len(rows) != 2 {
		t.Fatalf("status=200 rows = %d, want 2", len(rows))
	}
	rec = stewardGet(t, host.StationUser, cookie, StewardLogsPath+"?page_size=1&page=1")
	assertOK(t, rec)
	rows, hasMore = decodeLogs(t, rec)
	if len(rows) != 1 || !hasMore {
		t.Fatalf("paged rows = %d hasMore=%v, want 1/true", len(rows), hasMore)
	}
	rec = stewardGet(t, host.StationUser, cookie, StewardLogsPath+"?unknown=1")
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Live demotion: reset the manual level on the SAME session and the very
	// next request is already forbidden (no session refresh, no caching).
	setLevel(t, env.user.ID, nil)
	if code, c := codeOf(t, stewardGet(t, host.StationUser, cookie, StewardLogsPath)); code != http.StatusForbidden || c != "forbidden" {
		t.Fatalf("demoted = (%d, %s), want 403 forbidden", code, c)
	}

	// The route is NOT reachable through the plain user/admin log handler
	// trees (which have no steward frame): it 404s there, never bypassing the
	// level-5 gate.
	if rec := userGet(t, env, StewardLogsPath); rec.Code != http.StatusNotFound {
		t.Fatalf("plain user mount = %d, want 404 (steward route not on the user tree)", rec.Code)
	}
}
