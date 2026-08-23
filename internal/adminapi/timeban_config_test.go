package adminapi

// Tests for the temporal-ban admin API (ban duration presets + custom) and
// the nullable site-timezone config key: null-vs-zero distinction, typed
// validation, and the atomic immutability guard surfaced as a stable
// conflict envelope.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"time"
)

func TestBanWithDurationSetsDeadlineAndClearsAutoFlag(t *testing.T) {
	e := newEnv(t)
	user := e.seedUser(t, "dur")

	before := time.Now().Unix()
	rec := adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/ban", user.ID), map[string]any{
		"reason": "cooling off", "duration_seconds": 3600,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("temporary ban status = %d body=%s", rec.Code, rec.Body.String())
	}
	got, err := e.store.GetUserByID(user.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: (%#v, %v)", got, err)
	}
	if !got.IsBanned || got.AutoBanned {
		t.Fatalf("ban state wrong: %+v", got)
	}
	if got.BannedUntil == nil {
		t.Fatal("deadline not set")
	}
	until := got.BannedUntil.Unix()
	if until < before+3600-5 || until > time.Now().Unix()+3600+5 {
		t.Fatalf("deadline %d not ~now+3600", until)
	}

	// The admin detail projection carries the deadline and provenance.
	var resp struct {
		BannedUntil           *int64 `json:"banned_until"`
		AutoBanned            bool   `json:"auto_banned"`
		CharitySuspendedUntil *int64 `json:"charity_suspended_until"`
	}
	decodeJSON(t, adminGet(t, e, fmt.Sprintf("/admin/api/users/%d", user.ID)), &resp)
	if resp.BannedUntil == nil || *resp.BannedUntil != until || resp.AutoBanned || resp.CharitySuspendedUntil != nil {
		t.Fatalf("detail projection wrong: %+v", resp)
	}
}

func TestBanWithoutDurationIsPermanent(t *testing.T) {
	e := newEnv(t)
	user := e.seedUser(t, "perm")

	cases := []any{
		map[string]any{"reason": "spam"},
		map[string]any{},
		nil,
		map[string]any{"reason": "x", "duration_seconds": nil},
	}
	for _, body := range cases {
		rec := adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/ban", user.ID), body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("ban body %#v status = %d body=%s", body, rec.Code, rec.Body.String())
		}
		got, err := e.store.GetUserByID(user.ID)
		if err != nil || got == nil || !got.IsBanned {
			t.Fatalf("user state after ban body %#v: (%#v, %v)", body, got, err)
		}
		if got.BannedUntil != nil {
			t.Fatalf("permanent ban has deadline after body %#v: %+v", body, got)
		}
		if err := e.store.UnbanUser(user.ID); err != nil {
			t.Fatalf("unban between cases: %v", err)
		}
	}
}

func TestBanDurationValidation(t *testing.T) {
	e := newEnv(t)
	user := e.seedUser(t, "val")

	for _, raw := range []string{
		`{"duration_seconds":0}`,
		`{"duration_seconds":-3600}`,
		`{"duration_seconds":3600.5}`,
		`{"duration_seconds":"3600"}`,
		`{"duration_seconds":true}`,
		`{"duration_seconds":99999999999}`,
	} {
		req := stationRequest(http.MethodPost, fmt.Sprintf("/admin/api/users/%d/ban", user.ID), host.StationAdmin, []byte(raw))
		rec := do(t, e.mount(t, nil), withCookie(req, e.adminCookie(t)))
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
	got, _ := e.store.GetUserByID(user.ID)
	if got == nil || got.IsBanned {
		t.Fatalf("rejected bans must not mutate the user: %#v", got)
	}
}

func TestSiteTimezoneConfigNullVsZero(t *testing.T) {
	e := newEnv(t)

	// Unset projects as JSON null.
	var cfg map[string]any
	decodeJSON(t, adminGet(t, e, "/admin/api/site-config"), &cfg)
	value, ok := cfg["site_timezone_offset_minutes"]
	if !ok {
		t.Fatal("timezone key missing from site-config GET")
	}
	if value != nil {
		t.Fatalf("unset timezone projected as %v, want null", value)
	}

	// Explicit zero is a number, never null.
	decodeJSON(t, adminPatch(t, e, nil, "/admin/api/site-config/site_timezone_offset_minutes", map[string]any{"value": 0}), &struct{}{})
	cfg = map[string]any{}
	decodeJSON(t, adminGet(t, e, "/admin/api/site-config"), &cfg)
	if num, ok := cfg["site_timezone_offset_minutes"].(float64); !ok || num != 0 {
		t.Fatalf("explicit UTC projected as %v, want number 0", cfg["site_timezone_offset_minutes"])
	}
}

func TestSiteTimezonePatchValidation(t *testing.T) {
	e := newEnv(t)

	for _, raw := range []string{
		`{"value":45}`,         // not a 30-minute multiple
		`{"value":-721}`,       // below range
		`{"value":841}`,        // above range
		`{"value":30.5}`,       // non-integral
		`{"value":"30"}`,       // string
		`{"value":true}`,       // boolean
		`{"value":null}`,       // clearing is never allowed
		`{"value":4294967296}`, // must not wrap to UTC on 32-bit builds
		`{"value":9007199254740993}`,
	} {
		req := stationRequest(http.MethodPatch, "/admin/api/site-config/site_timezone_offset_minutes", host.StationAdmin, []byte(raw))
		rec := do(t, e.mount(t, nil), withCookie(req, e.adminCookie(t)))
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
	for _, valid := range []int{-720, 840, -330} {
		rec := adminPatch(t, e, nil, "/admin/api/site-config/site_timezone_offset_minutes", map[string]any{"value": valid})
		if rec.Code != http.StatusOK {
			t.Fatalf("valid value %d status = %d body=%s", valid, rec.Code, rec.Body.String())
		}
	}
}

func TestSiteTimezonePatchConflictOnceDataExists(t *testing.T) {
	e := newEnv(t)

	if _, err := e.store.DB().Exec(`INSERT INTO checkins (user_id, day, award, operation_id, created_at)
		VALUES (1, 0, 1, 'sys.checkin.1.0', 0)`); err != nil {
		t.Fatalf("insert checkin: %v", err)
	}

	req := stationRequest(http.MethodPatch, "/admin/api/site-config/site_timezone_offset_minutes", host.StationAdmin, []byte(`{"value":60}`))
	rec := do(t, e.mount(t, nil), withCookie(req, e.adminCookie(t)))
	assertErr(t, rec, http.StatusConflict, "conflict")

	// The stored value is unchanged.
	if got, err := e.store.SiteTimezoneOffsetMinutes(); err == nil && got == 60 {
		t.Fatalf("refused patch changed the offset")
	}
}
