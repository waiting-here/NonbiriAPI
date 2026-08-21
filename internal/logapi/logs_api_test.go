package logapi

// HTTP-level tests for the redesigned user log screen, the bounded options
// endpoint, and the administrator CSV/JSON export: cross-user isolation over
// real session middlewares, strict query-parameter rejection, CSV formula
// injection defense, fail-closed export bounds, and the unmounted level5
// steward mount point.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
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

func TestStewardMountPointNotRegistered(t *testing.T) {
	env := newTestEnv(t)
	// The level5 middleware does not exist yet, so no steward route may be
	// reachable through either station's handler tree — not even as an auth
	// failure. The constant documents the future mount point only.
	if StewardLogsPath != "/api/steward/logs" {
		t.Fatalf("StewardLogsPath = %q", StewardLogsPath)
	}
	rec := userGet(t, env, StewardLogsPath)
	if rec.Code == http.StatusOK {
		t.Fatalf("steward path is mounted without its middleware: %d", rec.Code)
	}
	rec = adminGet(t, env, "/admin/api/steward/logs")
	if rec.Code == http.StatusOK {
		t.Fatalf("steward path mounted on the admin station")
	}
}
