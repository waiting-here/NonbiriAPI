package creditapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestHistoryQueryClosedBoundedAndCanonical(t *testing.T) {
	for _, query := range []string{"", "page=9223372036854775807&page_size=100", "category=charity&direction=expense&from=0&to=253402300799"} {
		if _, err := parseFilter(query); err != nil {
			t.Fatalf("valid query %q: %v", query, err)
		}
	}
	for _, query := range []string{"page=0", "page=-1", "page=01", "page=1&page=2", "page=9223372036854775808", "page_size=10", "page_size=101", "user_id=2", "category=private", "category=", "direction=both", "from=9&to=9", "from=10&to=9", "from=-1", "to=253402300800", "anchor=op_bad", "%zz=x", strings.Repeat("a", 2049)} {
		if _, err := parseFilter(query); err == nil {
			t.Fatalf("accepted %q", query)
		}
	}
}

func TestHistoryHandlerRejectsBodiesAndMissingIdentityBeforeRead(t *testing.T) {
	for _, tc := range []struct {
		body   string
		user   int64
		status int
	}{{"x", 1, 400}, {"", 0, 401}} {
		r := httptest.NewRequest(http.MethodGet, "/api/credits/history", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		handler(nil)(w, r, resources.UserPrincipal{UserID: tc.user})
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/headers: %+v", w)
		}
	}
}
