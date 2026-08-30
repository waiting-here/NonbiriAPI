package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type registrarBaseURLs struct{}

func (registrarBaseURLs) ValidateBaseURL(value string) (string, error) { return value, nil }

type registrarSecrets struct{}

func (registrarSecrets) WriteEndpointSecret(context.Context, *sql.Tx, resources.SecretWriteInput) (resources.StoredSecret, error) {
	return resources.StoredSecret{RefID: 1}, nil
}
func (registrarSecrets) MarkEndpointSecretOrphaned(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

type registrarDeletion struct{}

func (registrarDeletion) PrepareEndpointKeyDeletion(context.Context, *sql.Tx, int64, []int64, int64) error {
	return nil
}

type registrarDiscovery struct{}

func (registrarDiscovery) Discover(context.Context, resources.DiscoveryClaimInput) (resources.DiscoveryClaimResult, error) {
	return resources.DiscoveryClaimResult{Succeeded: true}, nil
}

func TestResourcesRegisterRoutesAcceptsServeMuxVariablesAndPassesPathValue(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	worker, err := resources.NewDiscoveryWorkerPool(1, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	repository, err := resources.New(resources.Config{Store: f.store, Connectors: connector.NewDefaultRegistry(), BaseURLs: registrarBaseURLs{}, Secrets: registrarSecrets{}, KeyDeletion: registrarDeletion{}, DiscoveryRail: registrarDiscovery{}, DiscoveryWorker: worker, CursorKeys: f.vault, FinalAuth: f.runtime, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.RegisterRoutes(f.runtime.UserRouteRegistrar(), repository); err != nil {
		t.Fatalf("real resources.RegisterRoutes failed: %v", err)
	}
	if err := resources.RegisterRoutes(f.runtime.UserRouteRegistrar(), repository); err == nil {
		t.Fatal("duplicate resources routes unexpectedly accepted")
	}
	cookie := loginUser(t, f, "resources", "")
	create := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/endpoints", `{"connector_type":"openai-compatible","base_url":"https://upstream.example/v1","note":"path-value","enabled":true}`, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "ABCDEFGHIJKLMNOPQRSTUV"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var endpoint resources.Endpoint
	if err := json.Unmarshal(create.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.ID == "" {
		t.Fatalf("empty endpoint id: %s", create.Body.String())
	}
	read := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/endpoints/"+endpoint.ID, "", []*http.Cookie{cookie}, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("GET path-variable status=%d body=%s", read.Code, read.Body.String())
	}
	var fetched resources.Endpoint
	if err := json.Unmarshal(read.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.ID != endpoint.ID || fetched.Note != "path-value" {
		t.Fatalf("path value did not reach resources handler: got=%+v want=%+v", fetched, endpoint)
	}
	if err := f.runtime.RegisterAnonymousUserRoute(http.MethodGet, "/api/after-freeze", func(http.ResponseWriter, *http.Request) {}); err != ErrFrozen {
		t.Fatalf("post-freeze registration error=%v", err)
	}
}

func TestRouteRegistrationRejectsMalformedServeMuxPatterns(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := func(http.ResponseWriter, *http.Request) {}
	for _, pattern := range []string{"api/missing-slash", "/api/bad/{", "/api/bad/{id}/{id}", "/api/bad?query"} {
		if err := f.runtime.RegisterAnonymousUserRoute(http.MethodGet, pattern, handler); err != ErrInvalidRoute {
			t.Fatalf("pattern %q error=%v", pattern, err)
		}
	}
}

func TestRuntimeCloseRejectsRequestsAndRegistration(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := f.runtime.UserHandler()
	if err := f.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := f.runtime.RegisterAnonymousUserRoute(http.MethodGet, "/api/closed", func(http.ResponseWriter, *http.Request) {}); err != ErrClosed {
		t.Fatalf("registration error=%v", err)
	}
	rec := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/session", "", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOptionalUserRouteDistinguishesAnonymousAndAuthenticatedCallers(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	invocations := 0
	authenticated := false
	if err := f.runtime.RegisterOptionalUserRoute(http.MethodPost, "/api/optional-user", func(w http.ResponseWriter, req *http.Request, principal *OptionalUserPrincipal) {
		invocations++
		actor, hasActor := ActorFromContext(req.Context())
		if principal == nil {
			if hasActor {
				t.Error("anonymous request received an actor")
			}
		} else {
			authenticated = hasActor && actor.UserID == principal.UserID && actor.UserID > 0
		}
		w.WriteHeader(http.StatusAccepted)
	}); err != nil {
		t.Fatal(err)
	}
	handler := f.runtime.UserHandler()

	anonymous := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", nil, nil)
	if anonymous.Code != http.StatusAccepted || invocations != 1 {
		t.Fatalf("anonymous=%d invocations=%d body=%s", anonymous.Code, invocations, anonymous.Body.String())
	}

	cookie := loginUser(t, f, "optional-user", "")
	user := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", []*http.Cookie{cookie}, nil)
	if user.Code != http.StatusAccepted || invocations != 2 || !authenticated {
		t.Fatalf("user=%d invocations=%d authenticated=%v body=%s", user.Code, invocations, authenticated, user.Body.String())
	}
	if renewed := responseCookie(t, user, UserSessionCookieName); renewed.Value != cookie.Value {
		t.Fatalf("renewed cookie=%+v", renewed)
	}

	invalid := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", nil, map[string]string{"Cookie": UserSessionCookieName + "=invalid"})
	if invalid.Code != http.StatusUnauthorized || invocations != 2 {
		t.Fatalf("invalid=%d invocations=%d body=%s", invalid.Code, invocations, invalid.Body.String())
	}
	duplicate := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", []*http.Cookie{cookie, cookie}, nil)
	if duplicate.Code != http.StatusUnauthorized || invocations != 2 {
		t.Fatalf("duplicate=%d invocations=%d body=%s", duplicate.Code, invocations, duplicate.Body.String())
	}
	malformed := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", nil, map[string]string{"Cookie": UserSessionCookieName})
	if malformed.Code != http.StatusUnauthorized || invocations != 2 {
		t.Fatalf("malformed=%d invocations=%d body=%s", malformed.Code, invocations, malformed.Body.String())
	}
	malformedWhitespace := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", nil, map[string]string{"Cookie": UserSessionCookieName + " =invalid"})
	if malformedWhitespace.Code != http.StatusUnauthorized || invocations != 2 {
		t.Fatalf("malformed whitespace=%d invocations=%d body=%s", malformedWhitespace.Code, invocations, malformedWhitespace.Body.String())
	}
	wrongStation := request(t, handler, host.StationAdmin, http.MethodPost, "https://user.example/api/optional-user", "", []*http.Cookie{cookie}, nil)
	if wrongStation.Code != http.StatusForbidden || invocations != 2 {
		t.Fatalf("wrong station=%d invocations=%d body=%s", wrongStation.Code, invocations, wrongStation.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	banned := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", []*http.Cookie{cookie}, nil)
	if banned.Code != http.StatusForbidden || invocations != 2 {
		t.Fatalf("banned=%d invocations=%d body=%s", banned.Code, invocations, banned.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.DB().Exec(`UPDATE sessions SET expires_at=last_seen_at WHERE token_hash=?`, sessionLookupHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	expired := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", []*http.Cookie{cookie}, nil)
	if expired.Code != http.StatusUnauthorized || invocations != 2 {
		t.Fatalf("expired=%d invocations=%d body=%s", expired.Code, invocations, expired.Body.String())
	}

	f.gate.enabled = true
	maintenance := request(t, handler, host.StationUser, http.MethodPost, "https://user.example/api/optional-user", "", nil, nil)
	if maintenance.Code != http.StatusServiceUnavailable || invocations != 2 {
		t.Fatalf("maintenance=%d invocations=%d body=%s", maintenance.Code, invocations, maintenance.Body.String())
	}
}
