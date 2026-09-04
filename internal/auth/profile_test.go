package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

const profileIdempotencyKey = "profile_key_1234567890AB"

func TestPatchMeStrictIdempotentAndReauthorizesReplay(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "profile", "")
	handler := f.runtime.UserHandler()
	patch := func(body, key string) *httptest.ResponseRecorder {
		headers := map[string]string{"Content-Type": "application/json"}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		return request(t, handler, host.StationUser, http.MethodPatch, "https://user.example/api/me", body, []*http.Cookie{cookie}, headers)
	}
	for name, input := range map[string]string{"unknown": `{"lang":"en","extra":true}`, "duplicate": `{"lang":"en","lang":"zh"}`, "null": `{"lang":null}`, "trailing": `{"lang":"en"} {}`} {
		t.Run(name, func(t *testing.T) {
			rec := patch(input, profileIdempotencyKey)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	if rec := patch(`{"lang":"en"}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key=%d %s", rec.Code, rec.Body.String())
	}
	if rec := patch(`{"lang":"en"}`, "short"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid key=%d %s", rec.Code, rec.Body.String())
	}
	first := patch(`{"lang":"en","game_profile_public":true}`, profileIdempotencyKey)
	if first.Code != http.StatusOK {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	var envelope UserEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.User.Lang != "en" || !envelope.User.GameProfilePublic {
		t.Fatalf("envelope=%+v", envelope.User)
	}
	replay := patch(`{"lang":"en","game_profile_public":true}`, profileIdempotencyKey)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s want=%s", replay.Code, replay.Body.String(), first.Body.String())
	}
	mismatch := patch(`{"lang":"zh","game_profile_public":true}`, profileIdempotencyKey)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("mismatch=%d %s", mismatch.Code, mismatch.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	denied := patch(`{"lang":"en","game_profile_public":true}`, profileIdempotencyKey)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("replay after ban=%d %s", denied.Code, denied.Body.String())
	}
}

func TestPatchMeReplayRejectsReplacedSession(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	old := loginUser(t, f, "first-profile", "")
	headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": profileIdempotencyKey}
	first := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPatch, "https://user.example/api/me", `{"lang":"en"}`, []*http.Cookie{old}, headers)
	if first.Code != http.StatusOK {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	replacement := loginUser(t, f, "replacement-profile", "")
	if replacement.Value == old.Value {
		t.Fatal("session was not replaced")
	}
	replay := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPatch, "https://user.example/api/me", `{"lang":"en"}`, []*http.Cookie{old}, headers)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("old replay=%d %s", replay.Code, replay.Body.String())
	}
}
