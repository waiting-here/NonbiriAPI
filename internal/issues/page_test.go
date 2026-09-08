package issues

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestIssueNumberedPagesFilterOwnerAndLiveReportReason(t *testing.T) {
	env := newIssueTestEnvironment(t)
	owner := env.seedUser(t, "numbered-owner")
	other := env.seedUser(t, "numbered-other")
	ids := make([]string, 23)
	for i := range ids {
		ids[i] = seedNumberedIssue(t, env, owner, uint64(100+i), "endpoint", "current", 0)
	}
	for i := range 4 {
		seedNumberedIssue(t, env, other, uint64(200+i), "endpoint", "current", 0)
	}
	endpointID := env.seedEndpoint(t, owner)
	keyID := env.seedEndpointKey(t, endpointID)
	hidden := seedNumberedIssue(t, env, owner, 999, "endpoint_key", "current", 0)
	if _, err := env.store.DB().Exec(`UPDATE user_issues SET resource_ref=? WHERE id=?`, strconv.FormatInt(keyID, 10), hidden); err != nil {
		t.Fatal(err)
	}
	env.insertReportReason(t, keyID)
	if _, err := env.store.DB().Exec(`INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at) VALUES(?,1,1,NULL,?)`, owner, env.clock.Load()); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	for _, size := range []int{10, 20, 50, 100} {
		request := pagination.Request{Page: pagination.MaxPage, Size: size}
		got, err := env.service.List(t.Context(), owner, ListQuery{State: "current", Numbered: &request})
		pages := (len(ids)-1)/size + 1
		start := (pages - 1) * size
		if err != nil || got.Pagination == nil || got.Pagination.TotalItems != "23" || got.Pagination.Page != strconv.Itoa(pages) ||
			got.Pagination.PageSize != size || got.Pagination.TotalPages != strconv.Itoa(pages) ||
			got.NextCursor != nil || !got.ProjectionIncomplete || len(got.Data) != len(ids)-start {
			t.Fatalf("size %d: %+v, %v", size, got, err)
		}
		for i, row := range got.Data {
			if row.ID != ids[start+i] {
				t.Fatalf("same-second ordering or scope: got %s want %s", row.ID, ids[start+i])
			}
		}
	}
}

func TestIssueNumberedHistoryUsesRetentionBoundaryAndClamps(t *testing.T) {
	env := newIssueTestEnvironment(t)
	owner := env.seedUser(t, "numbered-history")
	for i := range 12 {
		seedNumberedIssue(t, env, owner, uint64(100+i), "endpoint", "closed", env.clock.Load()+10)
	}
	for i := range 2 {
		seedNumberedIssue(t, env, owner, uint64(200+i), "endpoint", "closed", env.clock.Load()-int64(i))
	}
	window := pagination.Request{Page: 2, Size: 10}
	got, err := env.service.List(t.Context(), owner, ListQuery{State: "closed", Numbered: &window})
	if err != nil || got.Pagination.TotalItems != "12" || got.Pagination.Page != "2" || len(got.Data) != 2 {
		t.Fatalf("retained history: %+v, %v", got, err)
	}
	env.clock.Add(10)
	got, err = env.service.List(t.Context(), owner, ListQuery{State: "closed", Numbered: &window})
	if err != nil || got.Pagination == nil || *got.Pagination != (pagination.Metadata{Page: "1", PageSize: 10, TotalItems: "0", TotalPages: "1"}) || got.Data == nil || len(got.Data) != 0 {
		t.Fatalf("history visible at retention deadline: %+v, %v", got, err)
	}
	var physical int
	if err := env.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=?`, owner).Scan(&physical); err != nil || physical != 14 {
		t.Fatalf("page GET modified retained storage: %d, %v", physical, err)
	}
}

func TestIssueNumberedHTTPPreservesLegacyAndAuthorization(t *testing.T) {
	env := newIssueTestEnvironment(t)
	owner := env.seedUser(t, "numbered-http")
	routes := &issueTestRoutes{}
	if err := RegisterRoutes(routes, env.service); err != nil {
		t.Fatal(err)
	}
	invoke := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		routes.handlers[http.MethodGet+" "+routeIssues](r, httptest.NewRequest(http.MethodGet, routeIssues+"?"+query, nil), UserPrincipal{UserID: owner})
		return r
	}
	for _, query := range []string{"state=current&page=1", "state=closed&page_size=10"} {
		response := invoke(query)
		var got Page
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.Pagination == nil || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("page HTTP: %d %s", response.Code, response.Body.String())
		}
	}
	legacy := invoke("state=current&limit=10")
	var fields map[string]json.RawMessage
	if legacy.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &fields) != nil || len(fields) != 3 || fields["pagination"] != nil {
		t.Fatalf("legacy shape changed: %s", legacy.Body.String())
	}
	for _, query := range []string{
		"state=current&page=1&cursor=", "state=current&page_size=10&limit=10", "state=current&page=01", "state=current&page=0",
		"state=current&page=2147483648", "state=current&page=1&page=2", "state=current&page_size=30", "state=current&page=1&state=closed",
	} {
		if response := invoke(query); response.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %s", query, response.Body.String())
		}
	}
	window := pagination.Default()
	for _, query := range []ListQuery{
		{State: "current", Numbered: &window, Limit: 10}, {State: "closed", Numbered: &window, Cursor: "old"},
		{State: "current", Numbered: &pagination.Request{Page: 0, Size: 20}},
	} {
		if _, err := env.service.List(t.Context(), owner, query); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("direct invalid query accepted: %+v, %v", query, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := env.service.List(ctx, owner, ListQuery{State: "current", Numbered: &window}); !errors.Is(err, context.Canceled) {
		t.Fatalf("page ignored cancellation: %v", err)
	}
	if _, err := env.service.List(nil, owner, ListQuery{State: "current", Numbered: &window}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil context: %v", err)
	}
	if _, err := env.store.DB().Exec(`DELETE FROM users WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	response := invoke("state=current&page=1")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user page: %d %s", response.Code, response.Body.String())
	}
	fields = nil
	if json.Unmarshal(response.Body.Bytes(), &fields) != nil || fields["pagination"] != nil || fields["data"] != nil {
		t.Fatalf("deleted owner leaked totals: %s", response.Body.String())
	}
}

func seedNumberedIssue(t *testing.T, env *issueTestEnvironment, owner int64, sequence uint64, kind, state string, retain int64) string {
	t.Helper()
	id := issueOpaqueID("iss_", sequence)
	var closedAt, retainUntil any
	seen := env.clock.Load()
	if state == "closed" {
		seen = retain - closedRetention
		closedAt, retainUntil = seen, retain
	}
	if _, err := env.store.DB().Exec(`INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count,closed_at,retain_until
) VALUES(?,?,'resource_validator',?,?,'configuration_invalid',1,?,'configuration_invalid','',?,?,1,?,?)`,
		id, owner, kind, strconv.FormatUint(sequence, 10), state, seen, seen, closedAt, retainUntil); err != nil {
		t.Fatal(err)
	}
	return id
}
