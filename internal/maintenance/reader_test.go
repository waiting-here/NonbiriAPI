package maintenance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type releasedReaderResponse struct {
	*httptest.ResponseRecorder
	check func()
}

func (w releasedReaderResponse) WriteHeader(status int) {
	w.check()
	w.ResponseRecorder.WriteHeader(status)
}

func (w releasedReaderResponse) Write(body []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(body)
}

func TestMaintenanceReaderAvoidsOccupiedWriterAndKeepsFinalAuthority(t *testing.T) {
	store := openMaintenanceStore(t)
	store.DB().SetMaxOpenConns(1)
	service, gate := newMaintenanceService(t, NewRegistry())
	if _, err := service.PrepareListener(t.Context(), store.ReadDB()); err != nil {
		t.Fatal(err)
	}
	admin := insertMaintenancePrincipal(t, store.DB(), "", "reader-admin", true, false)
	steward := insertMaintenancePrincipal(t, store.DB(), "reader-steward", "reader-steward", false, true)
	capture := &maintenanceRouteCapture{}
	if err := RegisterRoutes(capture, capture, HTTPOptions{Database: store.DB(), ReadDatabase: store.ReadDB(), Service: service}); err != nil {
		t.Fatal(err)
	}
	disabled := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminDisableRoute], http.MethodPost, adminDisableRoute,
		`{"expected_revision":"1","reason":"open"}`, strings.Repeat("R", 22), admin)
	if disabled.Code != http.StatusOK || gate.Enabled() {
		t.Fatalf("disable: %d %s", disabled.Code, disabled.Body)
	}
	held, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := store.DB().Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	read := func(handler AuthorizedHTTPHandler, path string, principal maintenancePrincipal) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		handler(releasedReaderResponse{rec, func() {
			if store.ReadDB().Stats().InUse != 0 {
				t.Fatal("response retained reader")
			}
		}}, req, HTTPPrincipal{Actor: principal.Actor})
		return rec
	}
	for _, entry := range []struct {
		handler   AuthorizedHTTPHandler
		path      string
		principal maintenancePrincipal
	}{
		{capture.admin[http.MethodGet+" "+adminStateRoute], adminStateRoute, admin},
		{capture.steward[http.MethodGet+" "+stewardStateRoute], stewardStateRoute, steward},
	} {
		rec := read(entry.handler, entry.path, entry.principal)
		if rec.Code != http.StatusOK || decodeMaintenanceState(t, rec) != (stateWire{Enabled: false, Revision: "2"}) {
			t.Fatalf("read: %d %s", rec.Code, rec.Body)
		}
	}
	state, err := LoadState(ctx, store.ReadDB())
	if err != nil {
		t.Fatal(err)
	}
	transition := Transition{gate: gate, state: state}
	if err := transition.ObserveAfterCommit(ctx, store.ReadDB()); err != nil {
		t.Fatal(err)
	}
	api := httpAPI{database: store.DB(), reader: store.ReadDB(), service: service}
	if err := api.observeCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := held.ExecContext(ctx, `UPDATE users SET level=4 WHERE id=?`, steward.ID); err != nil {
		t.Fatal(err)
	}
	if rec := read(capture.steward[http.MethodGet+" "+stewardStateRoute], stewardStateRoute, steward); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted steward retained authority: %d", rec.Code)
	}
	if _, err := held.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, admin.Actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if rec := read(capture.admin[http.MethodGet+" "+adminStateRoute], adminStateRoute, admin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked administrator retained authority: %d", rec.Code)
	}
	if store.DB().Stats().WaitCount != before || store.ReadDB().Stats().InUse != 0 {
		t.Fatal("read or post-commit observation waited for writer or retained reader")
	}
}
