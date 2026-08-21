package adminapi

// Tests for GET /admin/api/activity: the disabled status while the site
// timezone offset is unset, k-anonymity suppression of distinct-user counts
// at the wire boundary, pagination clamps, strict query validation, and the
// admin-session-only boundary.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

type activityPage struct {
	Enabled bool           `json:"enabled"`
	Data    []activityWire `json:"data"`
	HasMore bool           `json:"has_more"`
}

type activityWire struct {
	Day                   int64  `json:"day"`
	ProductActive         bool   `json:"product_active"`
	APIRequests           int64  `json:"api_requests"`
	UncachedInputTokens   int64  `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64  `json:"cache_read_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	Checkins              int64  `json:"checkins"`
	ConsoleWrites         int64  `json:"console_writes"`
	GameActive            bool   `json:"game_active"`
	GameRounds            int64  `json:"game_rounds"`
	DistinctProductUsers  *int64 `json:"distinct_product_users"`
}

func getActivity(t *testing.T, e *env, path string) *httptest.ResponseRecorder {
	t.Helper()
	return adminGet(t, e, path)
}

func decodeActivity(t *testing.T, rec *httptest.ResponseRecorder) activityPage {
	t.Helper()
	var page activityPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode activity response: %v body=%s", err, rec.Body.String())
	}
	return page
}

func TestAdminActivityDisabledWithoutTimezone(t *testing.T) {
	e := newEnv(t)
	rec := getActivity(t, e, "/admin/api/activity")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	page := decodeActivity(t, rec)
	if page.Enabled || len(page.Data) != 0 || page.HasMore {
		t.Fatalf("disabled response = %+v", page)
	}
}

func TestAdminActivityKAnonymityBoundary(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetSiteTimezoneOffsetMinutes(330); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	ctx := context.Background()
	at := time.Now().UTC()

	// Four active users: the distinct count must be suppressed to null.
	users := make([]*db.User, 0, 5)
	for i := 0; i < 5; i++ {
		users = append(users, e.seedUser(t, "activity-"+string(rune('a'+i))))
	}
	for _, u := range users[:4] {
		if err := e.store.RecordActivity(ctx, u.ID, at, db.ActivityDelta{APIRequests: 1}); err != nil {
			t.Fatalf("RecordActivity: %v", err)
		}
	}
	page := decodeActivity(t, getActivity(t, e, "/admin/api/activity"))
	if !page.Enabled || len(page.Data) != 1 {
		t.Fatalf("page = %+v", page)
	}
	row := page.Data[0]
	if row.DistinctProductUsers != nil {
		t.Fatalf("k=4 must suppress distinct to null, got %d", *row.DistinctProductUsers)
	}
	if row.APIRequests != 4 {
		t.Fatalf("api_requests=%d, want 4 (aggregates are never suppressed)", row.APIRequests)
	}

	// The fifth distinct user crosses the threshold: the count becomes numeric.
	if err := e.store.RecordActivity(ctx, users[4].ID, at, db.ActivityDelta{APIRequests: 1}); err != nil {
		t.Fatalf("RecordActivity fifth: %v", err)
	}
	page = decodeActivity(t, getActivity(t, e, "/admin/api/activity"))
	row = page.Data[0]
	if row.DistinctProductUsers == nil || *row.DistinctProductUsers != 5 {
		t.Fatalf("k=5 must report the count numerically, got %+v", row.DistinctProductUsers)
	}
}

func TestAdminActivityPaginationAndStrictQuery(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetSiteTimezoneOffsetMinutes(0); err != nil { // explicit UTC
		t.Fatalf("set timezone: %v", err)
	}
	u := e.seedUser(t, "activity-paging")
	ctx := context.Background()
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := e.store.RecordActivity(ctx, u.ID, base.Add(time.Duration(i)*24*time.Hour), db.ActivityDelta{APIRequests: 1}); err != nil {
			t.Fatalf("RecordActivity day %d: %v", i, err)
		}
	}

	rec := getActivity(t, e, "/admin/api/activity?page=1&page_size=2")
	page := decodeActivity(t, rec)
	if len(page.Data) != 2 || !page.HasMore {
		t.Fatalf("page_size=2 page=%+v", page)
	}
	// Newest day first.
	if page.Data[0].Day <= page.Data[1].Day {
		t.Fatalf("days not ordered DESC: %d then %d", page.Data[0].Day, page.Data[1].Day)
	}
	rec = getActivity(t, e, "/admin/api/activity?page=2&page_size=2")
	page = decodeActivity(t, rec)
	if len(page.Data) != 1 || page.HasMore {
		t.Fatalf("last page=%+v", page)
	}

	for _, bad := range []string{
		"/admin/api/activity?unknown=1",
		"/admin/api/activity?page=0",
		"/admin/api/activity?page=abc",
		"/admin/api/activity?page=1&page_size=0",
		"/admin/api/activity?page=1&page_size=-5",
		"/admin/api/activity?page=1&page=2",
	} {
		rec := getActivity(t, e, bad)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s, want invalid_request", bad, rec.Code, rec.Body.String())
		}
	}
	// page_size above the cap is clamped, not rejected.
	rec = getActivity(t, e, "/admin/api/activity?page_size=5000")
	if rec.Code != http.StatusOK {
		t.Fatalf("clamp status=%d", rec.Code)
	}
}

func TestAdminActivityRequiresAdminSession(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-act")

	// No identity on the admin station.
	rec := do(t, e.mount(t, nil), stationRequest(http.MethodGet, "/admin/api/activity", host.StationAdmin, nil))
	assertErr(t, rec, http.StatusUnauthorized, "unauthorized")

	// A normal user session never authorizes the route.
	userHandler := e.mountUser(t, NewHandler(HandlerDeps{Store: e.store}))
	rec = do(t, userHandler, withCookie(stationRequest(http.MethodGet, "/admin/api/activity", host.StationUser, nil), e.userCookie(t, u.ID)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user session status=%d, want 403", rec.Code)
	}
}
