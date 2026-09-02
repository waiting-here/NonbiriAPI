package home

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	_ "modernc.org/sqlite"
)

type homeAuthorizer struct {
	err    error
	calls  int
	userID int64
	tx     *sql.Tx
}

func (authorizer *homeAuthorizer) AuthorizeUserMutation(_ context.Context, tx *sql.Tx, userID int64) error {
	authorizer.calls++
	authorizer.userID = userID
	authorizer.tx = tx
	return authorizer.err
}

type homeLinkSource struct {
	summaries map[int64]linklink.HomeSummary
	err       error
	calls     int
	tx        *sql.Tx
}

func (source *homeLinkSource) HomeSummaryTx(_ context.Context, tx *sql.Tx, input linklink.HomeSummaryInput) (linklink.HomeSummary, error) {
	source.calls++
	source.tx = tx
	if source.err != nil {
		return linklink.HomeSummary{}, source.err
	}
	if summary, ok := source.summaries[input.UserID]; ok {
		return summary, nil
	}
	return linklink.HomeSummary{Continue: []linklink.HomeContinue{}}, nil
}

type homeRPSSource struct {
	summaries map[int64]rps.HomeSummary
	err       error
	calls     int
	tx        *sql.Tx
}

func (source *homeRPSSource) HomeSummaryTx(_ context.Context, tx *sql.Tx, userID int64) (rps.HomeSummary, error) {
	source.calls++
	source.tx = tx
	if source.err != nil {
		return rps.HomeSummary{}, source.err
	}
	if summary, ok := source.summaries[userID]; ok {
		return summary, nil
	}
	return rps.HomeSummary{Continue: []rps.HomeContinueItem{}, PendingResults: []rps.HomePendingItem{}}, nil
}

func TestReadClosedSourcesOwnershipStableOrderAndNoSideEffects(t *testing.T) {
	database := openHomeDatabase(t)
	fishingContinue := homeOID("fb_", 'A')
	fishingEarly := homeOID("fb_", 'B')
	fishingLate := homeOID("fb_", 'C')
	foreignFishing := homeOID("fb_", 'D')
	nextAttempt := int64(50)
	seedHomeFishing(t, database, fishingContinue, 1, "reserved", 0, &nextAttempt, 10, nil)
	seedHomeFishing(t, database, fishingLate, 1, "committed", 0, nil, 300, nil)
	seedHomeFishing(t, database, fishingEarly, 1, "committed", 0, nil, 100, nil)
	seedHomeFishing(t, database, foreignFishing, 2, "committed", 0, nil, 1, nil)

	linkID := homeOID("ll_", 'A')
	rpsID := homeOID("rps_", 'A')
	authorizer := &homeAuthorizer{}
	links := &homeLinkSource{summaries: map[int64]linklink.HomeSummary{
		1: {Continue: []linklink.HomeContinue{{ResourceID: linkID, State: "active"}}},
	}}
	rpsSource := &homeRPSSource{summaries: map[int64]rps.HomeSummary{
		1: {Continue: []rps.HomeContinueItem{{Game: game.RPSID, ResourceID: rpsID, State: rps.StateStarted, RouteID: "game-rps"}}, PendingResults: []rps.HomePendingItem{}},
	}}
	service := newHomeService(t, database, authorizer, links, rpsSource)

	summary, err := service.Read(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 || authorizer.userID != 1 || links.calls != 1 || rpsSource.calls != 1 ||
		authorizer.tx == nil || authorizer.tx != links.tx || links.tx != rpsSource.tx {
		t.Fatalf("single snapshot auth=%d/%d link=%d rps=%d tx=%p/%p/%p", authorizer.calls, authorizer.userID,
			links.calls, rpsSource.calls, authorizer.tx, links.tx, rpsSource.tx)
	}
	if len(summary.Continue) != 3 || summary.Continue[0] != (ContinueItem{Game: game.FishingID, ResourceID: fishingContinue, State: "settlement_pending", RouteID: "game-fishing"}) ||
		summary.Continue[1] != (ContinueItem{Game: game.LinkLinkID, ResourceID: linkID, State: "active", RouteID: "game-linklink"}) ||
		summary.Continue[2] != (ContinueItem{Game: game.RPSID, ResourceID: rpsID, State: rps.StateStarted, RouteID: "game-rps"}) {
		t.Fatalf("continue=%+v", summary.Continue)
	}
	if len(summary.PendingResults) != 2 || summary.PendingResults[0].ResourceID != fishingEarly || summary.PendingResults[1].ResourceID != fishingLate {
		t.Fatalf("pending=%+v", summary.PendingResults)
	}
	var remaining int
	if err := database.QueryRow(`SELECT COUNT(*) FROM game_fishing_batches WHERE user_id=1 AND state='committed' AND revealed_at IS NULL`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("read changed fishing ACK state count=%d err=%v", remaining, err)
	}

	foreign, err := service.Read(context.Background(), 2)
	if err != nil || len(foreign.Continue) != 0 || len(foreign.PendingResults) != 1 || foreign.PendingResults[0].ResourceID != foreignFishing {
		t.Fatalf("foreign summary=(%+v,%v)", foreign, err)
	}
}

func TestReadMergesPendingFIFOAndRejectsImpossibleRPSUnion(t *testing.T) {
	database := openHomeDatabase(t)
	firstFish := homeOID("fb_", 'A')
	secondFish := homeOID("fb_", 'B')
	seedHomeFishing(t, database, secondFish, 1, "committed", 0, nil, 300, nil)
	seedHomeFishing(t, database, firstFish, 1, "committed", 0, nil, 100, nil)
	rpsID := homeOID("rps_", 'A')
	authorizer := &homeAuthorizer{}
	links := &homeLinkSource{summaries: map[int64]linklink.HomeSummary{}}
	rpsSource := &homeRPSSource{summaries: map[int64]rps.HomeSummary{
		1: {Continue: []rps.HomeContinueItem{}, PendingResults: []rps.HomePendingItem{{Game: game.RPSID, ResourceID: rpsID, CreatedAt: 200, RouteID: "game-rps"}}},
	}}
	service := newHomeService(t, database, authorizer, links, rpsSource)
	summary, err := service.Read(context.Background(), 1)
	if err != nil || len(summary.Continue) != 0 || len(summary.PendingResults) != 3 {
		t.Fatalf("summary=(%+v,%v)", summary, err)
	}
	if summary.PendingResults[0].ResourceID != firstFish || summary.PendingResults[1].ResourceID != rpsID || summary.PendingResults[2].ResourceID != secondFish {
		t.Fatalf("FIFO pending=%+v", summary.PendingResults)
	}

	rpsSource.summaries[1] = rps.HomeSummary{
		Continue:       []rps.HomeContinueItem{{Game: game.RPSID, ResourceID: rpsID, State: rps.StateStarted, RouteID: "game-rps"}},
		PendingResults: []rps.HomePendingItem{{Game: game.RPSID, ResourceID: rpsID, CreatedAt: 200, RouteID: "game-rps"}},
	}
	if _, err := service.Read(context.Background(), 1); !errors.Is(err, ErrInvariant) {
		t.Fatalf("impossible RPS union error=%v", err)
	}
}

func TestHTTPStrictClosedWireAuthorizationAndMaintenanceRegistrar(t *testing.T) {
	database := openHomeDatabase(t)
	linkID := homeOID("ll_", 'A')
	rpsID := homeOID("rps_", 'A')
	authorizer := &homeAuthorizer{}
	links := &homeLinkSource{summaries: map[int64]linklink.HomeSummary{
		1: {Continue: []linklink.HomeContinue{{ResourceID: linkID, State: "active"}}},
	}}
	rpsSource := &homeRPSSource{summaries: map[int64]rps.HomeSummary{
		1: {Continue: []rps.HomeContinueItem{}, PendingResults: []rps.HomePendingItem{{Game: game.RPSID, ResourceID: rpsID, CreatedAt: 200, RouteID: "game-rps"}}},
	}}
	service := newHomeService(t, database, authorizer, links, rpsSource)
	routes := &homeRoutes{}
	if err := RegisterRoutes(routes, service); err != nil {
		t.Fatal(err)
	}
	if routes.method != http.MethodGet || routes.path != RouteSummary || routes.handler == nil {
		t.Fatalf("registered=%s %s handler=%v", routes.method, routes.path, routes.handler != nil)
	}

	valid := routes.request(http.MethodGet, RouteSummary, "", 1)
	if valid.Code != http.StatusOK || valid.Header().Get("Cache-Control") != "no-store" || valid.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("valid=%d headers=%v body=%s", valid.Code, valid.Header(), valid.Body.String())
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(valid.Body.Bytes(), &object); err != nil || len(object) != 2 || object["continue"] == nil || object["pending_results"] == nil {
		t.Fatalf("closed root object=%v err=%v body=%s", object, err, valid.Body.String())
	}
	if strings.Contains(valid.Body.String(), "href") || strings.Contains(valid.Body.String(), "history") || strings.Contains(valid.Body.String(), "ack") {
		t.Fatalf("response inferred forbidden fields: %s", valid.Body.String())
	}
	var decoded Summary
	if err := json.Unmarshal(valid.Body.Bytes(), &decoded); err != nil || len(decoded.Continue) != 1 || len(decoded.PendingResults) != 1 {
		t.Fatalf("wire decode=(%+v,%v)", decoded, err)
	}

	for _, request := range []struct{ target, body string }{{RouteSummary + "?extra=1", ""}, {RouteSummary + "?", ""}, {RouteSummary, `{}`}} {
		before := authorizer.calls
		response := routes.request(http.MethodGet, request.target, request.body, 1)
		if response.Code != http.StatusBadRequest || authorizer.calls != before {
			t.Fatalf("invalid request %q/%q status=%d auth=%d/%d body=%s", request.target, request.body, response.Code, authorizer.calls, before, response.Body.String())
		}
	}

	authorizer.err = resources.ErrUnauthorized
	unauthorized := routes.request(http.MethodGet, RouteSummary, "", 1)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("final authorization status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	authorizer.err = nil
	maintenance := &homeRoutes{maintenance: true}
	if err := RegisterRoutes(maintenance, service); err != nil {
		t.Fatal(err)
	}
	before := authorizer.calls
	blocked := maintenance.request(http.MethodGet, RouteSummary, "", 1)
	if blocked.Code != http.StatusServiceUnavailable || authorizer.calls != before {
		t.Fatalf("maintenance status=%d auth=%d/%d body=%s", blocked.Code, authorizer.calls, before, blocked.Body.String())
	}

	limited := httptest.NewRecorder()
	writeError(limited, ErrResourceLimit)
	var envelope httperr.Envelope
	if err := json.Unmarshal(limited.Body.Bytes(), &envelope); err != nil ||
		limited.Code != http.StatusUnprocessableEntity || envelope.Error.Code != httperr.CodeResourceLimitExceeded {
		t.Fatalf("resource limit status=%d error=%+v decode=%v", limited.Code, envelope.Error, err)
	}
}

type homeRoutes struct {
	method      string
	path        string
	handler     resources.AuthorizedUserHandler
	maintenance bool
}

func (routes *homeRoutes) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	routes.method, routes.path, routes.handler = method, path, handler
	return nil
}

func (routes *homeRoutes) request(method, target, body string, userID int64) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://user.example"+target, strings.NewReader(body))
	response := httptest.NewRecorder()
	if routes.maintenance {
		httperr.WriteError(response, httperr.New(httperr.CodeMaintenance, "maintenance in progress"))
		return response
	}
	routes.handler(response, request, resources.UserPrincipal{UserID: userID})
	return response
}

func newHomeService(t *testing.T, database *sql.DB, authorizer *homeAuthorizer, links *homeLinkSource, rpsSource *homeRPSSource) *Service {
	t.Helper()
	service, err := New(Options{Database: database, UserAuthorizer: authorizer, LinkLink: links, RPS: rpsSource})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func openHomeDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "home.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`CREATE TABLE game_fishing_batches(
id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,state TEXT NOT NULL,retry_exhausted INTEGER NOT NULL,
next_attempt_at INTEGER,created_at INTEGER NOT NULL,revealed_at INTEGER)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func seedHomeFishing(t *testing.T, database *sql.DB, id string, userID int64, state string, retry int, next *int64, created int64, revealed *int64) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO game_fishing_batches(id,user_id,state,retry_exhausted,next_attempt_at,created_at,revealed_at) VALUES(?,?,?,?,?,?,?)`,
		id, userID, state, retry, next, created, revealed); err != nil {
		t.Fatal(err)
	}
}

func homeOID(prefix string, marker byte) string {
	return prefix + strings.Repeat("A", 20) + string(marker) + "A"
}
