package duel_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func TestDuelHistoryAndValidationWhileWriterHeld(t *testing.T) {
	f := newFixture(t, "bidding")
	f.db.SetMaxOpenConns(1)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.s.History(ctx, f.identity(0), duel.PageInput{Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if err = f.s.ValidatePersistedState(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.options.ReadDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}

type responseWithoutReader struct {
	*httptest.ResponseRecorder
	t      *testing.T
	reader *sql.DB
}

func (w responseWithoutReader) WriteHeader(status int) {
	w.t.Helper()
	if n := w.reader.Stats().InUse; n != 0 {
		w.t.Fatalf("response retained %d readers", n)
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestDuelReadHTTPReleasesConnectionBeforeResponse(t *testing.T) {
	f := newFixture(t, "gwent")
	routes := &registrar{mux: http.NewServeMux(), identity: f.identity(0)}
	if err := f.s.RegisterRoutes(routes, routes); err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	writer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for _, path := range []string{"/api/games/gwent/catalog", "/api/games/gwent/leaderboard"} {
		w := responseWithoutReader{httptest.NewRecorder(), t, f.options.ReadDatabase}
		routes.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("UPDATE users SET is_banned=1 WHERE id=?", f.identity(0).UserID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/games/gwent/catalog", "/api/games/gwent/leaderboard", "/api/games/gwent/randomness/invalid"} {
		w := responseWithoutReader{httptest.NewRecorder(), t, f.options.ReadDatabase}
		routes.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("revoked %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
