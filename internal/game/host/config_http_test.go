package host

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
	builtinconfig "github.com/waiting-here/NonbiriAPI/internal/game/builtin/config"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
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
	if err != nil || !bytes.Contains(first.Modules["fishing"], []byte(`"worm":"2500.001"`)) {
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
	patch, err := builtinconfig.DecodeGamesConfigPatch(body)
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

func TestInvalidQuickStakesRollBackWholeConfigurationPatch(t *testing.T) {
	f := newGameFixture(t, nil)
	ctx := context.Background()
	before, err := f.service.ReadGamesConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, delta := range []string{`{"min_stake":"2000"}`, `{"quick_stakes":["1000","1000"]}`, `{"quick_stakes":null}`} {
		body := []byte(`{"expected_revision":"` + before.Revision + `","fishing":{"bait_prices":{"worm":"2500"}},"blackjack":` + delta + `}`)
		if _, err := f.service.PatchGamesConfig(ctx, body, validTestKey(200+i)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("invalid patch accepted", err)
		}
		after, err := f.service.ReadGamesConfig(ctx)
		if err != nil || !equalJSONValue(t, before, after) {
			t.Fatal("invalid patch changed configuration or revision", err)
		}
		var n int
		if err := f.database.QueryRow(`SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`).Scan(&n); err != nil || n != 0 {
			t.Fatal("invalid patch retained mutation record", n, err)
		}
	}
	body := []byte(`{"expected_revision":"` + before.Revision + `","blackjack":{"min_stake":"2000","quick_stakes":["5000","2000"]}}`)
	after, err := f.service.PatchGamesConfig(ctx, body, validTestKey(210))
	if err != nil || after.Revision == before.Revision || !bytes.Contains(after.Modules["blackjack"], []byte(`"quick_stakes":["2000","5000"]`)) {
		t.Fatal("atomic limits and buttons patch failed", err)
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

func TestConfigIdempotencyDatabaseFailuresRemainRetryable(t *testing.T) {
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

}
