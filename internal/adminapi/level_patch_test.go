package adminapi

// Level mode of PATCH /admin/api/users/{id} and the level threshold
// site-config keys: the tri-state `level` field (absent / null / 1..5),
// mode-exclusivity against economy adjustments, the administrator-row
// exclusion, the additive level/auto_level projection, and the amount-string
// threshold keys with their same-transaction strictly-increasing chain check.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type levelPatchRow struct {
	Level     *int `json:"level"`
	AutoLevel int  `json:"auto_level"`
}

func storedLevelNull(t *testing.T, e *env, userID int64) bool {
	t.Helper()
	var isNull bool
	if err := e.store.DB().QueryRow(`SELECT level IS NULL FROM users WHERE id=?`, userID).Scan(&isNull); err != nil {
		t.Fatalf("read level null flag: %v", err)
	}
	return isNull
}

func TestAdminUserPatchLevelTriState(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-level")
	path := fmt.Sprintf("/admin/api/users/%d", u.ID)

	// Absent field: unchanged (still automatic, level null).
	var row levelPatchRow
	decodeJSON(t, adminPatch(t, e, nil, path, map[string]any{"lang": "zh"}), &row)
	if row.Level != nil || row.AutoLevel != 1 {
		t.Fatalf("absent level projection = (%+v, %d), want (nil, 1)", row.Level, row.AutoLevel)
	}

	// Integer 1..5 stores a manual override; level 5 included.
	for _, level := range []int{1, 3, 5} {
		decodeJSON(t, adminPatch(t, e, nil, path, map[string]any{"level": level}), &row)
		if row.Level == nil || *row.Level != level {
			t.Fatalf("manual %d projection = %+v", level, row.Level)
		}
	}
	if storedLevelNull(t, e, u.ID) {
		t.Fatalf("manual level did not persist")
	}

	// Explicit JSON null resets to automatic.
	decodeJSON(t, adminPatch(t, e, nil, path, map[string]any{"level": nil}), &row)
	if row.Level != nil {
		t.Fatalf("null reset projection = %+v, want nil", row.Level)
	}
	if !storedLevelNull(t, e, u.ID) {
		t.Fatalf("null reset did not persist")
	}

	// Out-of-range and non-integer values are invalid_request.
	for _, bad := range []any{0, 6, -1, "3", 2.5, true} {
		rec := adminPatch(t, e, nil, path, map[string]any{"level": bad})
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
}

func TestAdminUserPatchLevelModeExclusivity(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-level-mix")
	path := fmt.Sprintf("/admin/api/users/%d", u.ID)

	// An economy request carrying `level` is a mixed-mode request: rejected.
	rec := adminPatch(t, e, nil, path, map[string]any{
		"credits": "5", "operation_id": "mix-level", "reason": "r", "level": 3,
	})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")

	// A profile request carrying an economy field stays rejected (existing
	// behavior re-pinned).
	rec = adminPatch(t, e, nil, path, map[string]any{"lang": "zh", "credits": "5"})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")

	// Nothing leaked into storage from either rejected request.
	if !storedLevelNull(t, e, u.ID) {
		t.Fatalf("rejected mixed request wrote a level")
	}
	var credits int64
	if err := e.store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, u.ID).Scan(&credits); err != nil || credits != 0 {
		t.Fatalf("rejected mixed request wrote credits = (%d, %v)", credits, err)
	}
}

func TestAdminUserPatchLevelAdministratorExcluded(t *testing.T) {
	e := newEnv(t)
	path := fmt.Sprintf("/admin/api/users/%d", e.admin.ID)

	rec := adminPatch(t, e, nil, path, map[string]any{"level": 5})
	assertErr(t, rec, http.StatusForbidden, "forbidden")

	rec = adminPatch(t, e, nil, path, map[string]any{"level": nil})
	assertErr(t, rec, http.StatusForbidden, "forbidden")
}

func TestAdminSiteConfigLevelThresholdKeys(t *testing.T) {
	e := newEnv(t)

	// GET projects the three keys with the canonical default "0".
	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	for _, key := range []string{KeyLevelThreshold2Milli, KeyLevelThreshold3Milli, KeyLevelThreshold4Milli} {
		if got, ok := cfg[key]; !ok || got != "0" {
			t.Fatalf("GET %s = (%v, %v), want \"0\"", key, got, ok)
		}
	}

	patchThreshold := func(key string, value any) *httptest.ResponseRecorder {
		return adminPatch(t, e, nil, "/admin/api/site-config/"+key, map[string]any{"value": value})
	}

	// Canonical non-negative decimal strings are accepted.
	rec2 := patchThreshold(KeyLevelThreshold2Milli, "100")
	if rec2.Code != http.StatusOK {
		t.Fatalf("set t2 status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	// Non-canonical, negative, or non-string values are invalid_request.
	for _, bad := range []any{"-5", "007", "+5", "1e3", 100, 0.5, true, nil} {
		rec := patchThreshold(KeyLevelThreshold3Milli, bad)
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
	// A zero (disabled) value is a plain string.
	rec2 = patchThreshold(KeyLevelThreshold3Milli, "0")
	if rec2.Code != http.StatusOK {
		t.Fatalf("set t3=0 status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// The chain cross-check: enabled thresholds must strictly increase.
	rec2 = patchThreshold(KeyLevelThreshold3Milli, "50")
	assertErr(t, rec2, http.StatusConflict, "conflict")
	// Equal enabled thresholds are also rejected.
	rec2 = patchThreshold(KeyLevelThreshold3Milli, "100")
	assertErr(t, rec2, http.StatusConflict, "conflict")
	// A skip (middle disabled) is fine.
	rec2 = patchThreshold(KeyLevelThreshold4Milli, "999")
	if rec2.Code != http.StatusOK {
		t.Fatalf("skip t4 status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	// The rejected writes left no half-chain: t3 is still disabled.
	var t3 string
	if err := e.store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, KeyLevelThreshold3Milli).Scan(&t3); err != nil || t3 != "0" {
		t.Fatalf("rejected chain write leaked t3=(%q, %v)", t3, err)
	}

	// GET now projects the stored canonical strings.
	rec = adminGet(t, e, "/admin/api/site-config")
	decodeJSON(t, rec, &cfg)
	if cfg[KeyLevelThreshold2Milli] != "100" || cfg[KeyLevelThreshold4Milli] != "999" {
		t.Fatalf("stored projection = %v / %v", cfg[KeyLevelThreshold2Milli], cfg[KeyLevelThreshold4Milli])
	}
}
