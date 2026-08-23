package adminapi

// Administrator site-config tests for the check-in keys: the three-way enum
// switch validation, the award-bound pair cross-validation (both directions,
// atomic no-write rejection), the documented defaults on GET, and the generic
// PATCH path for the non-pair keys.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminSiteConfigCheckinKeysDefaults(t *testing.T) {
	e := newEnv(t)

	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	for key, want := range map[string]string{
		KeyCheckinMode:          "disabled",
		KeyCheckinAwardMinMilli: "40000000",
		KeyCheckinAwardMaxMilli: "60000000",
		KeyCreditsCapMilli:      "250000000",
	} {
		if got, ok := cfg[key]; !ok || got != want {
			t.Fatalf("GET %s = (%v, %v), want %q", key, got, ok, want)
		}
	}
}

func TestAdminSiteConfigCheckinModeEnum(t *testing.T) {
	e := newEnv(t)

	patch := func(value any) *httptest.ResponseRecorder {
		return adminPatch(t, e, nil, "/admin/api/site-config/"+KeyCheckinMode, map[string]any{"value": value})
	}
	// Only the three member strings are accepted.
	for _, valid := range []string{"enabled", "level_gated", "disabled"} {
		rec := patch(valid)
		if rec.Code != http.StatusOK {
			t.Fatalf("set %q status=%d body=%s", valid, rec.Code, rec.Body.String())
		}
	}
	for _, bad := range []any{"Enabled", "sometimes", "", 1, true, nil, []string{"enabled"}} {
		assertErr(t, patch(bad), http.StatusBadRequest, "invalid_request")
	}

	// The stored value projects back; a manually corrupted row reads as the
	// disabled default (fail closed).
	if rec := patch("level_gated"); rec.Code != http.StatusOK {
		t.Fatalf("re-set level_gated status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	if cfg[KeyCheckinMode] != "level_gated" {
		t.Fatalf("stored mode projection = %v", cfg[KeyCheckinMode])
	}
	if _, err := e.store.DB().Exec(`UPDATE site_config SET value='sometimes' WHERE key=?`, KeyCheckinMode); err != nil {
		t.Fatalf("corrupt mode: %v", err)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	cfg = map[string]any{}
	decodeJSON(t, rec, &cfg)
	if cfg[KeyCheckinMode] != "disabled" {
		t.Fatalf("corrupt mode projection = %v, want disabled", cfg[KeyCheckinMode])
	}
}

func TestAdminSiteConfigCheckinAwardPair(t *testing.T) {
	e := newEnv(t)

	patchKey := func(key string, value any) *httptest.ResponseRecorder {
		return adminPatch(t, e, nil, "/admin/api/site-config/"+key, map[string]any{"value": value})
	}

	// Non-canonical, negative, or non-string values are invalid_request.
	for _, bad := range []any{"-5", "007", "+5", "1e3", 100, 0.5, true, nil} {
		assertErr(t, patchKey(KeyCheckinAwardMinMilli, bad), http.StatusBadRequest, "invalid_request")
	}

	// Cross-validation, direction 1: min above the (default) max is a 409
	// with nothing written and no console-write activity row.
	assertErr(t, patchKey(KeyCheckinAwardMinMilli, "60000001"), http.StatusConflict, "conflict")
	var stored any
	if err := e.store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, KeyCheckinAwardMinMilli).Scan(&stored); err == nil {
		t.Fatalf("rejected min write leaked stored=%v", stored)
	}
	if n := countConfigTestRows(t, e, `SELECT COUNT(*) FROM user_activity_daily`); n != 0 {
		t.Fatalf("rejected write recorded activity: %d rows", n)
	}

	// A valid pair lands both sides.
	if rec := patchKey(KeyCheckinAwardMinMilli, "100"); rec.Code != http.StatusOK {
		t.Fatalf("set min status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := patchKey(KeyCheckinAwardMaxMilli, "200"); rec.Code != http.StatusOK {
		t.Fatalf("set max status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Cross-validation, direction 2: max below the stored min is a 409 and
	// keeps the previous value.
	assertErr(t, patchKey(KeyCheckinAwardMaxMilli, "99"), http.StatusConflict, "conflict")
	var maxStored string
	if err := e.store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, KeyCheckinAwardMaxMilli).Scan(&maxStored); err != nil || maxStored != "200" {
		t.Fatalf("rejected max rewrite leaked stored=(%q, %v)", maxStored, err)
	}
	// min == max is a legitimate pair.
	if rec := patchKey(KeyCheckinAwardMaxMilli, "100"); rec.Code != http.StatusOK {
		t.Fatalf("set max=min status=%d body=%s", rec.Code, rec.Body.String())
	}

	// GET projects the stored canonical strings.
	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	if cfg[KeyCheckinAwardMinMilli] != "100" || cfg[KeyCheckinAwardMaxMilli] != "100" {
		t.Fatalf("pair projection = %v / %v", cfg[KeyCheckinAwardMinMilli], cfg[KeyCheckinAwardMaxMilli])
	}
}

func TestAdminSiteConfigCreditsCapGeneric(t *testing.T) {
	e := newEnv(t)

	// credits_cap_milli rides the generic path (no runtime singleton).
	if rec := adminPatch(t, e, nil, "/admin/api/site-config/"+KeyCreditsCapMilli, map[string]any{"value": "0"}); rec.Code != http.StatusOK {
		t.Fatalf("set cap=0 status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	if cfg[KeyCreditsCapMilli] != "0" {
		t.Fatalf("cap projection = %v, want \"0\"", cfg[KeyCreditsCapMilli])
	}
	for _, bad := range []any{"-1", "1.5", 100, nil} {
		assertErr(t, adminPatch(t, e, nil, "/admin/api/site-config/"+KeyCreditsCapMilli, map[string]any{"value": bad}),
			http.StatusBadRequest, "invalid_request")
	}
	// A manually corrupted cap row projects as the documented default.
	if _, err := e.store.DB().Exec(`UPDATE site_config SET value='oops' WHERE key=?`, KeyCreditsCapMilli); err != nil {
		t.Fatalf("corrupt cap: %v", err)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	cfg = map[string]any{}
	decodeJSON(t, rec, &cfg)
	if cfg[KeyCreditsCapMilli] != "250000000" {
		t.Fatalf("corrupt cap projection = %v, want default 250000000", cfg[KeyCreditsCapMilli])
	}
}

func countConfigTestRows(t *testing.T, e *env, query string) int {
	t.Helper()
	var n int
	if err := e.store.DB().QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}
