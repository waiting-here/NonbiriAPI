package rewards

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

type userRoutes map[string]limitedactivities.AuthorizedUserHandler

func (r userRoutes) RegisterUserRoute(method, path string, h limitedactivities.AuthorizedUserHandler) error {
	r[method+" "+path] = h
	return nil
}

type adminRoutes map[string]limitedactivities.AuthorizedAdminHandler

func (r adminRoutes) RegisterAdminRoute(method, path string, h limitedactivities.AuthorizedAdminHandler) error {
	r[method+" "+path] = h
	return nil
}

func TestRedemptionHTTPRequiresUniqueKeyAndReturnsSafeReplay(t *testing.T) {
	f := setup(t)
	users, admins := userRoutes{}, adminRoutes{}
	if err := RegisterRoutes(users, admins, f.s); err != nil {
		t.Fatal(err)
	}
	h := users["POST /api/rewards/redeem"]
	request := func(body string, keys ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/rewards/redeem", strings.NewReader(body)).WithContext(t.Context())
		for _, key := range keys {
			r.Header.Add("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h(w, r, limitedactivities.UserPrincipal{UserID: f.user})
		return w
	}
	key := f.nextKey()
	for _, keys := range [][]string{nil, {"short"}, {key, key}} {
		if w := request(`{"code":"MISSING"}`, keys...); w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"code":null}`, `{"code":"ONE","code":"TWO"}`, `{"code":"MISSING","extra":1}`} {
		if w := request(body, key); w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if f.scalar(t, "SELECT count(*) FROM idempotency_records WHERE scope='reward_redemption'") != 0 {
		t.Fatal("invalid transport request counted as a submission")
	}
	first := request(`{"code":"MISSING"}`, key)
	replay := request(`{"code":"MISSING"}`, key)
	if first.Code != 200 || replay.Code != 200 || first.Body.String() != replay.Body.String() || strings.TrimSpace(first.Body.String()) != `{"rejection":"unavailable"}` || first.Header().Get("Cache-Control") != "no-store" || f.violations(t, f.user) != 1 {
		t.Fatal(first.Code, first.Body.String(), replay.Body.String())
	}
	if conflict := request(`{"code":"DIFFERENT"}`, key); conflict.Code != http.StatusConflict {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
	f.set(t, antiabuse.KeyRedemptionBanThreshold, 2)
	ban := request(`{"code":"MISSING"}`, f.nextKey())
	if ban.Code != 200 || !strings.Contains(ban.Body.String(), `"rejection":"banned"`) || !strings.Contains(ban.Body.String(), `"banned_until":1800086400`) || strings.Contains(ban.Body.String(), "MISSING") || strings.Contains(ban.Body.String(), "threshold") {
		t.Fatal(ban.Code, ban.Body.String())
	}
}
