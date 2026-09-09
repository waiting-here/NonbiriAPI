package adminalerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestNumberedAlertsCountFilteredSnapshotAndClampAfterResolve(t *testing.T) {
	env := newAlertTestEnvironment(t)
	ids := make([]int64, 26)
	for i := range ids {
		ids[i] = env.seedAlert(t, string(KindForwardError), "page alert", "", nil, alertTestNow-10, i%2 == 1)
	}
	handler := env.handler(t, http.MethodGet, routeAlerts)
	for _, size := range []int{10, 20, 50, 100} {
		page := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet,
			routeAlerts+"?page=2147483647&page_size="+strconv.Itoa(size), nil, "", env.adminID, nil))
		pages := (len(ids)-1)/size + 1
		count := len(ids) - (pages-1)*size
		if page.Pagination == nil || page.Pagination.TotalItems != "26" || page.Pagination.Page != strconv.Itoa(pages) ||
			page.Pagination.TotalPages != strconv.Itoa(pages) || page.Pagination.PageSize != size || page.NextCursor != nil || len(page.Data) != count {
			t.Fatalf("size %d page: %+v", size, page)
		}
		if page.Data[len(page.Data)-1].ID != alertIDString(ids[0]) {
			t.Fatalf("size %d lost stable last row: %+v", size, page.Data)
		}
	}
	query := routeAlerts + "?resolved=false&page=2&page_size=10"
	page := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet, query, nil, "", env.adminID, nil))
	if page.Pagination.TotalItems != "13" || page.Pagination.TotalPages != "2" {
		t.Fatalf("unresolved count includes resolved rows: %+v", page.Pagination)
	}
	requireAlertIDs(t, page.Data, ids[4], ids[2], ids[0])
	for _, id := range []int64{ids[0], ids[2], ids[4]} {
		if _, err := env.repository.SetResolved(context.Background(), env.adminID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	page = decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet, query, nil, "", env.adminID, nil))
	if page.Pagination.Page != "1" || page.Pagination.TotalItems != "10" || len(page.Data) != 10 {
		t.Fatalf("resolving the last page did not clamp: %+v", page)
	}
	if !env.authorizer.usedTx {
		t.Fatal("page count escaped final authorized transaction")
	}
}

func TestNumberedAlertAuthorizationAndLegacyShape(t *testing.T) {
	env := newAlertTestEnvironment(t)
	handler := env.handler(t, http.MethodGet, routeAlerts)
	empty := decodeAlertPage(t, invokeAlertHandler(t, handler, http.MethodGet,
		routeAlerts+"?page=99", nil, "", env.adminID, nil))
	if empty.Data == nil || len(empty.Data) != 0 || empty.NextCursor != nil || empty.Pagination == nil ||
		*empty.Pagination != (pagination.Metadata{Page: "1", PageSize: 20, TotalItems: "0", TotalPages: "1"}) {
		t.Fatalf("empty page: %+v", empty)
	}
	response := invokeAlertHandler(t, handler, http.MethodGet, routeAlerts+"?limit=10", nil, "", env.adminID, nil)
	var legacy map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &legacy) != nil || len(legacy) != 2 || legacy["pagination"] != nil {
		t.Fatalf("legacy shape changed: %s", response.Body.String())
	}
	env.authorizer.forced = authz.ErrForbidden
	denied := invokeAlertHandler(t, handler, http.MethodGet, routeAlerts+"?page=1", nil, "", env.adminID, nil)
	requireErrorCode(t, denied, http.StatusForbidden, httperr.CodeForbidden)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(denied.Body.Bytes(), &body); err != nil || body["pagination"] != nil || body["data"] != nil {
		t.Fatalf("revoked authority leaked a page: %s", denied.Body.String())
	}
	page := pagination.Default()
	if _, err := env.repository.List(context.Background(), env.adminID, ListQuery{Numbered: &page}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("direct list bypassed revocation: %v", err)
	}
}

func TestNumberedAlertsRejectMixedOrMalformedWindows(t *testing.T) {
	env := newAlertTestEnvironment(t)
	handler := env.handler(t, http.MethodGet, routeAlerts)
	for _, query := range []string{
		"page=1&cursor=", "page_size=10&limit=10", "page=1&page=2", "page=0", "page=01", "page=-1",
		"page=2147483648", "page_size=15", "page_size=010", "page=1&resolved=0", "page=1&resolved=true&resolved=false",
	} {
		t.Run(query, func(t *testing.T) {
			requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, routeAlerts+"?"+query, nil, "", env.adminID, nil),
				http.StatusBadRequest, httperr.CodeInvalidRequest)
		})
	}
	page := pagination.Default()
	for _, query := range []ListQuery{{Numbered: &page, Cursor: "opaque"}, {Numbered: &page, Limit: 10}, {Numbered: &pagination.Request{Page: 0, Size: 20}}} {
		if _, err := env.repository.List(context.Background(), env.adminID, query); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("direct mixed window accepted: %+v, %v", query, err)
		}
	}
}
