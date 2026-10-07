package adminusers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestEndpointOverviewUserFilterKeepsGroupTotalsAndBindsCursor(t *testing.T) {
	f := newAdminUsersFixture(t)
	a, b := f.seedUser("filter-a", false), f.seedUser("filter-b", false)
	for _, host := range []string{"a", "b"} {
		f.seedEndpoint(a, "https://"+host+".filter.example/v1", true, 2)
	}
	f.seedEndpoint(b, "https://a.filter.example/v1", true, 1)
	f.seedEndpoint(b, "https://other.example/v1", true, 1)
	requested := pagination.Request{Page: 1, Size: 10}
	query := EndpointOverviewQuery{Q: "filter.example", Tag: "untagged", UserID: a, Page: &requested}
	page, err := f.service.EndpointOverview(context.Background(), f.adminID, query)
	if err != nil {
		t.Fatal(err)
	}
	requireAdminPage(t, page.Pagination, 1, 10, 2)
	if len(page.Data) != 2 || page.Data[0].UserCount != "2" || page.Data[0].EndpointCount != "2" || page.Data[0].KeyCount != "3" || len(page.Data[0].Users) != 2 {
		t.Fatalf("filtered group lost its site totals: %+v", page)
	}
	query.Page, query.Limit = nil, 1
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, query)
	if err != nil || page.NextCursor == nil {
		t.Fatalf("cursor page: %+v %v", page, err)
	}
	query.Cursor = *page.NextCursor
	query.UserID = b
	if _, err := f.service.EndpointOverview(context.Background(), f.adminID, query); err == nil {
		t.Fatal("cursor accepted for another user filter")
	}
	query.UserID = a
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, query)
	if err != nil || len(page.Data) != 1 || page.Data[0].BaseURL != "https://b.filter.example/v1" {
		t.Fatalf("filtered continuation: %+v %v", page, err)
	}
	query.Cursor, query.UserID = "", 9223372036854775807
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, query)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("nonexistent user: %+v %v", page, err)
	}
}

func TestEndpointOverviewUserFilterHTTP(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("http-filter", false)
	f.seedEndpoint(user, "https://http-filter.example", true, 1)
	for _, raw := range []string{"0", "-1", "01", "+1", "1.5", "9223372036854775808", "", "1&user_id=2"} {
		response := f.request(http.MethodGet, routeEndpointOverview, "https://admin.example"+routeEndpointOverview+"?user_id="+raw, "", 0, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("user_id=%q: %d", raw, response.Code)
		}
	}
	response := f.request(http.MethodGet, routeEndpointOverview, fmt.Sprintf("https://admin.example%s?user_id=%d&page=1&page_size=20", routeEndpointOverview, user), "", 0, "")
	var page Page[EndpointOverview]
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Data[0].Users[0].UserID != strconv.FormatInt(user, 10) {
		t.Fatalf("valid filter: %d %s", response.Code, response.Body.String())
	}
}
