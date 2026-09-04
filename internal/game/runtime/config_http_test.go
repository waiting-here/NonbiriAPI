package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func configPatchBody(t *testing.T, fixture *gameFixture, amount string) []byte {
	t.Helper()
	current, err := fixture.service.ReadGamesConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"expected_revision": current.Revision,
		"fishing":           map[string]any{"bait_prices": map[string]any{"worm": amount}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPatchGamesConfigExactReplayConflictAndLiveAuthorization(t *testing.T) {
	fixture := newGameFixture(t, nil)
	body := configPatchBody(t, fixture, "2500.001")
	key := validTestKey(100)
	first, err := fixture.service.PatchGamesConfig(context.Background(), body, key)
	if err != nil || first.Fishing.BaitPrices.Worm != "2500.001" {
		t.Fatalf("first patch = (%#v,%v)", first, err)
	}
	storedBody, marshalErr := json.Marshal(first)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var status int
	var persisted []byte
	if err = fixture.database.QueryRow(`SELECT http_status,response_body FROM idempotency_records WHERE scope='control_mutation'`).Scan(&status, &persisted); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || !bytes.Equal(persisted, storedBody) {
		t.Fatalf("persisted replay = status %d body %s, want %s", status, persisted, storedBody)
	}

	// expected_revision is now stale. A completed replay must return the
	// original snapshot without consulting current configuration.
	replay, err := fixture.service.PatchGamesConfig(context.Background(), body, key)
	if err != nil || !equalJSONValue(t, replay, first) {
		t.Fatalf("replay = (%#v,%v), want %#v", replay, err, first)
	}
	different := configPatchBody(t, fixture, "2500.002")
	if _, err = fixture.service.PatchGamesConfig(context.Background(), different, key); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key different body error = %v", err)
	}

	fixture.adminAuth.setError(authz.ErrForbidden)
	if _, err = fixture.service.PatchGamesConfig(context.Background(), body, key); !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay without live admin authority error = %v", err)
	}
}

func TestPatchGamesConfigSameAdminKeyAcrossRouteConflicts(t *testing.T) {
	fixture := newGameFixture(t, nil)
	body := configPatchBody(t, fixture, "2500.003")
	patch, err := game.DecodeGamesConfigPatch(body)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := idempotency.CanonicalJSON(patch)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(fixture.adminID, 10))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: http.MethodPatch, Route: "/admin/api/other-control", Body: canonical,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := validTestKey(101)
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key,
		RequestHash: digest, DecisionNow: fixture.clock.Load(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = idempotency.Complete(context.Background(), tx, decision, http.StatusOK, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.service.PatchGamesConfig(context.Background(), body, key); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-route key reuse error = %v", err)
	}
}

func TestPatchGamesConfigRevisionCAS(t *testing.T) {
	fixture := newGameFixture(t, nil)
	body := configPatchBody(t, fixture, "2500.01")
	if _, err := fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(110)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(111)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

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

func TestPatchConfigHTTPRequiresOneValidIdempotencyKey(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		status  int
	}{
		{name: "missing", status: http.StatusBadRequest},
		{name: "short", headers: []string{"short"}, status: http.StatusBadRequest},
		{name: "invalid alphabet", headers: []string{"game_idempotency_key_!_xxxxxxxx"}, status: http.StatusBadRequest},
		{name: "duplicate", headers: []string{validTestKey(120), validTestKey(121)}, status: http.StatusBadRequest},
		{name: "valid", headers: []string{validTestKey(122)}, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGameFixture(t, nil)
			body := configPatchBody(t, fixture, "2500.02")
			request := httptest.NewRequest(http.MethodPatch, "http://example.test"+RouteAdminGamesConfig, bytes.NewReader(body))
			for _, value := range test.headers {
				request.Header.Add("Idempotency-Key", value)
			}
			response := httptest.NewRecorder()
			(&httpAPI{service: fixture.service}).patchConfig(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
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
	t.Run("admin complete", func(t *testing.T) {
		fixture := newGameFixture(t, nil)
		body := configPatchBody(t, fixture, "2500.03")
		before, err := fixture.service.ReadGamesConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fixture.database.Exec(`CREATE TRIGGER fail_idempotency_complete BEFORE UPDATE ON idempotency_records WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'database is locked'); END`); err != nil {
			t.Fatal(err)
		}
		_, err = fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(132))
		if !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("admin Complete BUSY classification = %v", err)
		}
		after, readErr := fixture.service.ReadGamesConfig(context.Background())
		if readErr != nil || !equalJSONValue(t, before, after) {
			t.Fatalf("admin Complete rollback = (%#v,%v), want %#v", after, readErr, before)
		}
	})
	if !errors.Is(mapIdempotency(idempotency.ErrState), ErrInvariant) || !errors.Is(mapIdempotencyComplete(idempotency.ErrState), ErrInvariant) {
		t.Fatal("idempotency state corruption was not classified as invariant")
	}
}

func equalJSONValue(t *testing.T, left, right any) bool {
	t.Helper()
	a, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(a, b)
}
