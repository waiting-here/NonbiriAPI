package rewards

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

// Response encoding must not retain the reader, including rejected requests.
type readerResponse struct {
	*httptest.ResponseRecorder
	t *testing.T
	s *Service
}

func (w readerResponse) WriteHeader(status int) {
	w.t.Helper()
	if n := w.s.config.ReadDatabase.Stats().InUse; n != 0 {
		w.t.Fatalf("response retained %d readers", n)
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestRewardReaderPreviewHistoryAndHTTPRelease(t *testing.T) {
	f := setup(t)
	users, admins := userRoutes{}, adminRoutes{}
	if err := RegisterRoutes(users, admins, f.s); err != nil {
		t.Fatal(err)
	}
	writer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	preview, err := f.s.Preview(ctx, f.admin, input("broadcast"))
	if err != nil || preview.Matched != 2 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err = f.s.History(ctx, f.user, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.List(ctx, f.admin, ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/rewards/assets", "/admin/api/rewards/assets"} {
		w := readerResponse{httptest.NewRecorder(), t, f.s}
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		if handler, ok := users[http.MethodGet+" "+path]; ok {
			handler(w, request, limitedactivities.UserPrincipal{UserID: f.user})
		} else {
			admins[http.MethodGet+" "+path](w, request, limitedactivities.AdminPrincipal{UserID: f.admin})
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("UPDATE users SET is_banned=1 WHERE id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.History(ctx, f.user, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("banned history: %v", err)
	}
	w := readerResponse{httptest.NewRecorder(), t, f.s}
	users["GET /api/rewards/assets"](w, httptest.NewRequest(http.MethodGet, "/api/rewards/assets", nil).WithContext(ctx), limitedactivities.UserPrincipal{UserID: f.user})
	if w.Code != http.StatusForbidden {
		t.Fatalf("banned assets: %d", w.Code)
	}
}
