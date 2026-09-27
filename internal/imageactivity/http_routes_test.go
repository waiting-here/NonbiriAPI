package imageactivity

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type muxRoutes struct {
	mux         *http.ServeMux
	user, admin int64
}

func (m muxRoutes) RegisterUserRoute(method, path string, handler AuthorizedUserHandler) error {
	m.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, UserPrincipal{UserID: m.user})
	})
	return nil
}

func (m muxRoutes) RegisterAdminRoute(method, path string, handler AuthorizedAdminHandler) error {
	m.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, AdminPrincipal{UserID: m.admin})
	})
	return nil
}

func TestModelCapabilityAndRefreshRoutesShareRealMux(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	routes := muxRoutes{mux: http.NewServeMux(), user: f.user, admin: f.admin}
	if err := RegisterRoutes(routes, routes, f.service); err != nil {
		t.Fatal(err)
	}
	var refreshID string
	if err := f.database.QueryRow("SELECT operation_id FROM image_model_refreshes LIMIT 1").Scan(&refreshID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/models/" + f.model + "/capabilities", http.StatusOK},
		{"/models/refresh/" + refreshID, http.StatusOK},
		{"/models/" + f.model + "/unknown", http.StatusNotFound},
		{"/models/refresh/capabilities", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, adminPrefix+tc.path, nil).WithContext(f.ctx(f.admin))
		out := httptest.NewRecorder()
		routes.mux.ServeHTTP(out, req)
		if out.Code != tc.status {
			t.Fatalf("GET %s: status %d, want %d: %s", tc.path, out.Code, tc.status, out.Body.String())
		}
	}
}
