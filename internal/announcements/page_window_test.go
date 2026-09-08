package announcements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestAnnouncementNumberedPagesRespectPublicationExpiryFiltersAndLanguage(t *testing.T) {
	env := newAnnouncementTestEnvironment(t)
	admin := env.seedUser(t, "", "en", true)
	user := env.seedUser(t, "numbered-announcement-user", "en", false)
	const keys = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for i := 0; i < 28; i++ {
		var expiry *int64
		if i < 2 {
			value := announcementTestNow + 10
			expiry = &value
		}
		draft := env.createDraft(t, admin, keys[i], fmt.Sprintf("公告 %d", i), "内容", "", "", expiry)
		if i < 23 {
			env.publish(t, admin, keys[28+i], draft)
		}
	}
	env.clock.Add(11)
	page := pagination.Request{Page: pagination.MaxPage, Size: 10}
	userPage, err := env.service.ListUser(context.Background(), user, PageQuery{Numbered: &page})
	if err != nil || userPage.Pagination == nil || userPage.Pagination.TotalItems != "21" || userPage.Pagination.Page != "3" || len(userPage.Data) != 1 || userPage.NextCursor != nil {
		t.Fatalf("user page=%+v err=%v", userPage, err)
	}
	if userPage.Data[0].EffectiveLanguage != "zh" || userPage.Data[0].FallbackFrom == nil || *userPage.Data[0].FallbackFrom != "en" {
		t.Fatalf("language projection changed: %+v", userPage.Data[0])
	}
	for _, test := range []struct {
		state, total, last string
		rows               int
	}{{"", "28", "3", 8}, {"draft", "5", "1", 5}, {"published", "21", "3", 1}, {"expired", "2", "1", 2}, {"withdrawn", "0", "1", 0}} {
		got, err := env.service.ListAdmin(context.Background(), admin, AdminListQuery{State: test.state, Severity: "important", Numbered: &page})
		if err != nil || got.Pagination == nil || got.Pagination.TotalItems != test.total || got.Pagination.Page != test.last || len(got.Data) != test.rows || got.NextCursor != nil {
			t.Fatalf("state=%s: page=%+v err=%v", test.state, got, err)
		}
		for _, row := range got.Data {
			if test.state != "" && row.State != test.state {
				t.Fatalf("row outside filter: %+v", row)
			}
		}
	}
	env.authorizer.deny.Store(true)
	if _, err := env.service.ListAdmin(context.Background(), admin, AdminListQuery{Numbered: &page}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked administrator page=%v", err)
	}
}

func TestAnnouncementNumberedHTTPStrictWindowsAndLegacyShape(t *testing.T) {
	env := newAnnouncementTestEnvironment(t)
	user := env.seedUser(t, "announcement-page-http", "en", false)
	api := &httpAPI{service: env.service}
	for _, query := range []string{"page=1&cursor=x", "page=1&limit=10", "page=01", "page=0", "page=2147483648", "page_size=30", "page=1&page=2", "page_size="} {
		r := httptest.NewRequest("GET", routeUserAnnouncements+"?"+query, nil)
		w := httptest.NewRecorder()
		api.listUser(w, r, UserPrincipal{UserID: user})
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"page=99&page_size=10", "limit=10"} {
		r := httptest.NewRequest("GET", routeUserAnnouncements+"?"+query, nil)
		w := httptest.NewRecorder()
		api.listUser(w, r, UserPrincipal{UserID: user})
		var body map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		if query == "limit=10" {
			if len(body) != 2 || body["pagination"] != nil {
				t.Fatalf("legacy response changed: %s", w.Body.String())
			}
		} else {
			var meta pagination.Metadata
			if len(body) != 3 || json.Unmarshal(body["pagination"], &meta) != nil || meta.Page != "1" || meta.TotalItems != "0" || meta.TotalPages != "1" || meta.PageSize != 10 {
				t.Fatalf("empty window: %s", w.Body.String())
			}
		}
	}
}
