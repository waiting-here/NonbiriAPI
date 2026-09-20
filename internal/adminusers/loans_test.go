package adminusers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestLoanHistoryManagementParityAndFinalRole(t *testing.T) {
	f, steward := newStewardUsersFixture(t)
	user := f.seedUser("loan-history", false)
	managed := stewardUserRequest(t, f, steward, user, http.MethodGet, routeUserLoans, "?page=1&page_size=10", "", "")
	r := httptest.NewRequest(http.MethodGet, "/admin/api/users/"+strconv.FormatInt(user, 10)+"/loans?page=1&page_size=10", nil)
	r.SetPathValue("id", strconv.FormatInt(user, 10))
	w := httptest.NewRecorder()
	f.registrar.handler(http.MethodGet, routeUserLoans)(w, r, AdminPrincipal{UserID: f.adminID})
	if managed.Code != 200 || w.Code != 200 || managed.Body.String() != w.Body.String() {
		t.Fatal("management history differs", managed.Code, managed.Body, w.Code, w.Body)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=1 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	denied := stewardUserRequest(t, f, steward, user, http.MethodGet, routeUserLoans, "", "", "")
	if denied.Code != 403 {
		t.Fatal("stale steward role accepted", denied.Code, denied.Body)
	}
	f.auth.err = authz.ErrForbidden
	w = httptest.NewRecorder()
	f.registrar.handler(http.MethodGet, routeUserLoans)(w, r, AdminPrincipal{UserID: f.adminID})
	if w.Code != 403 {
		t.Fatal("final admin authority ignored", w.Code, w.Body)
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if f.registrar.handler(method, roleSteward.route(routeUserLoans)) != nil {
			t.Fatal("history granted write authority", method)
		}
	}
}
