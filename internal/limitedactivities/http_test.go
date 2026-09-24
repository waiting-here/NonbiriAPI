package limitedactivities

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type userRoutes map[string]AuthorizedUserHandler

func (r userRoutes) RegisterUserRoute(method, path string, h AuthorizedUserHandler) error {
	r[method+" "+path] = h
	return nil
}

type adminRoutes map[string]AuthorizedAdminHandler

func (r adminRoutes) RegisterAdminRoute(method, path string, h AuthorizedAdminHandler) error {
	r[method+" "+path] = h
	return nil
}
func TestHTTPRejectsAmbiguousBodiesAndReturnsSafeReceipts(t *testing.T) {
	f := newFixture(t)
	f.open(t, "10")
	f.fund(t, f.user, 10000)
	users, admins := userRoutes{}, adminRoutes{}
	if e := RegisterRoutes(users, admins, f.service); e != nil {
		t.Fatal(e)
	}
	handler := users["POST "+userExchangeRoute]
	for _, body := range []string{`{"asset":"sketch_paper","quantity":1}`, `{"asset":"sketch_paper","quantity":"1","quantity":"2"}`, `{"asset":"sketch_paper","quantity":"1","prompt":"unexpected"}`, `{"asset":"sketch_paper","quantity":null}`, `[]`, `{"asset":"sketch_paper"}`, `{"asset":"sketch_paper","quantity":"1"}{}`} {
		request := httptest.NewRequest(http.MethodPost, userExchangeRoute, strings.NewReader(body)).WithContext(f.ctx(f.user))
		request.Header.Set("Idempotency-Key", key(1))
		response := httptest.NewRecorder()
		handler(response, request, UserPrincipal{f.user})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s accepted: %d %s", body, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, userExchangeRoute, strings.NewReader(`{"asset":"sketch_paper","quantity":"1"}`)).WithContext(f.ctx(f.user))
	request.Header.Set("Idempotency-Key", key(2))
	response := httptest.NewRecorder()
	handler(response, request, UserPrincipal{f.user})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"user_id", "secret", "upstream", "request_hash"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("unsafe field %s", forbidden)
		}
	}
	for _, path := range []string{userWalletRoute + "?user_id=1", userDirectoryRoute + "?hidden=true"} {
		request = httptest.NewRequest(http.MethodGet, path, nil).WithContext(f.ctx(f.user))
		response = httptest.NewRecorder()
		handler := users["GET "+strings.Split(path, "?")[0]]
		handler(response, request, UserPrincipal{f.user})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query override accepted: %s", path)
		}
	}
}
