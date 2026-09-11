package runtime

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestRequireExactQueryRejectsAmbiguousAndMalformedRawQuery(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		force      bool
		allowed    []string
		wantAccept bool
	}{
		{name: "none", wantAccept: true},
		{name: "allowed", raw: "board=single", allowed: []string{"board"}, wantAccept: true},
		{name: "empty question mark", force: true},
		{name: "bad escape", raw: "board=%zz", allowed: []string{"board"}},
		{name: "duplicate", raw: "board=single&board=total", allowed: []string{"board"}},
		{name: "unexpected", raw: "other=single", allowed: []string{"board"}},
		{name: "unexpected on empty route", raw: "board=single"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://example.test/api/games", nil)
			request.URL.RawQuery = test.raw
			request.URL.ForceQuery = test.force
			response := httptest.NewRecorder()
			accepted := requireExactQuery(response, request, test.allowed...)
			if accepted != test.wantAccept {
				t.Fatalf("accepted = %v, want %v", accepted, test.wantAccept)
			}
			if !accepted && response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestFishingHTTPRequiresOneValidIdempotencyKey(t *testing.T) {
	fixture := newGameFixture(t, nil)
	userID := fixture.seedUser("http-key", fixtureFunding)
	api := &httpAPI{service: fixture.service}
	invalid := []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "invalid", headers: []string{"short"}},
		{name: "duplicate", headers: []string{validTestKey(125), validTestKey(126)}},
	}
	for _, test := range invalid {
		t.Run("start "+test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://example.test"+RouteFishingBatches, bytes.NewBufferString(`{"bait":"worm","count":1}`))
			for _, value := range test.headers {
				request.Header.Add("Idempotency-Key", value)
			}
			response := httptest.NewRecorder()
			api.start(response, request, resources.UserPrincipal{UserID: userID})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
	validStart := httptest.NewRequest(http.MethodPost, "http://example.test"+RouteFishingBatches, bytes.NewBufferString(`{"bait":"worm","count":1}`))
	validStart.Header.Set("Idempotency-Key", validTestKey(127))
	validResponse := httptest.NewRecorder()
	api.start(validResponse, validStart, resources.UserPrincipal{UserID: userID})
	if validResponse.Code != http.StatusOK {
		t.Fatalf("valid start status = %d body=%s", validResponse.Code, validResponse.Body.String())
	}

	pendingUser := fixture.seedUser("http-recover", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: pendingUser, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(128)})
	if err != nil || pending == nil {
		t.Fatalf("seed pending = (%#v,%v)", pending, err)
	}
	if _, err = fixture.database.Exec(`UPDATE game_fishing_batches SET attempt_count=10,next_attempt_at=NULL,last_error_class='settlement_failed',retry_exhausted=1 WHERE id=?`, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	for _, test := range invalid {
		t.Run("recover "+test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://example.test/api/games/fishing/batches/"+pending.BatchID+"/recover", nil)
			request.SetPathValue("id", pending.BatchID)
			for _, value := range test.headers {
				request.Header.Add("Idempotency-Key", value)
			}
			response := httptest.NewRecorder()
			api.recover(response, request, resources.UserPrincipal{UserID: pendingUser})
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
	validRecover := httptest.NewRequest(http.MethodPost, "http://example.test/api/games/fishing/batches/"+pending.BatchID+"/recover", nil)
	validRecover.SetPathValue("id", pending.BatchID)
	validRecover.Header.Set("Idempotency-Key", validTestKey(129))
	recoverResponse := httptest.NewRecorder()
	api.recover(recoverResponse, validRecover, resources.UserPrincipal{UserID: pendingUser})
	if recoverResponse.Code != http.StatusAccepted {
		t.Fatalf("valid recover status = %d body=%s", recoverResponse.Code, recoverResponse.Body.String())
	}
}

func TestIdempotencyDatabaseFailuresRemainRetryable(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		fixture := newGameFixture(t, nil)
		userID := fixture.seedUser("begin-busy", fixtureFunding)
		if _, err := fixture.database.Exec(`CREATE TRIGGER fail_idempotency_begin BEFORE INSERT ON idempotency_records BEGIN SELECT RAISE(ABORT,'database is locked'); END`); err != nil {
			t.Fatal(err)
		}
		_, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(130)})
		if !errors.Is(err, ErrServiceUnavailable) || errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Begin BUSY classification = %v", err)
		}
	})
	t.Run("fishing complete", func(t *testing.T) {
		fixture := newGameFixture(t, nil)
		userID := fixture.seedUser("complete-busy", fixtureFunding)
		if _, err := fixture.database.Exec(`CREATE TRIGGER fail_idempotency_complete BEFORE UPDATE ON idempotency_records WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'database is locked'); END`); err != nil {
			t.Fatal(err)
		}
		before := fixture.scalar(`SELECT COUNT(*) FROM credit_operations`)
		_, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(131)})
		if !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("Fishing Complete BUSY classification = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations`) != before {
			t.Fatal("Fishing Complete failure did not roll back")
		}
	})
	if !errors.Is(mapIdempotency(idempotency.ErrState), ErrInvariant) || !errors.Is(mapIdempotencyComplete(idempotency.ErrState), ErrInvariant) {
		t.Fatal("idempotency state corruption was not classified as invariant")
	}
}
