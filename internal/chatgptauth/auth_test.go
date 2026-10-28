package chatgptauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	_ "modernc.org/sqlite"
)

type testResolver struct{}

func (testResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type authFixture struct {
	m            *Manager
	key          *ecdsa.PrivateKey
	issuer       string
	clock        atomic.Int64
	refreshes    atomic.Int64
	revocations  atomic.Int64
	mode         atomic.Int64
	denied       atomic.Bool
	codeExchange func(http.ResponseWriter, *http.Request)
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{}
	f.clock.Store(1800000000)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	enc := base64.RawURLEncoding.EncodeToString
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.issuer, "jwks_uri": f.issuer + "/jwks", "revocation_endpoint": f.issuer + "/revoke"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "EC", "crv": "P-256", "kid": "fixture", "alg": "ES256", "use": "sig", "x": enc(key.X.FillBytes(make([]byte, 32))), "y": enc(key.Y.FillBytes(make([]byte, 32)))}}})
		case "/api/accounts/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" && f.codeExchange != nil {
				f.codeExchange(w, r)
				return
			}
			f.refreshes.Add(1)
			if (r.Form.Get("refresh_token") != "refresh-original" && r.Form.Get("refresh_token") != "refresh-rotated") || r.Form.Get("client_id") != "issued-client" || r.Form.Get("resource") != Resource {
				t.Error("unexpected refresh parameters")
			}
			switch f.mode.Load() {
			case 1:
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":"temporary secret-upstream-detail"}`)
			case 2:
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"refresh-original"}`)
			default:
				_, _ = io.WriteString(w, `{"access_token":"access-rotated","refresh_token":"refresh-rotated","token_type":"Bearer","expires_in":3600}`)
			}
		case "/revoke":
			_ = r.ParseForm()
			if r.Form.Get("token") != "refresh-rotated" && r.Form.Get("token") != "refresh-original" {
				t.Error("unexpected revocation token")
			}
			f.revocations.Add(1)
			if f.mode.Load() == 3 {
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":"private-revocation-detail"}`)
				return
			}
			w.WriteHeader(200)
		default:
			http.NotFound(w, r)
		}
	}))
	f.issuer = server.URL
	t.Cleanup(server.Close)
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, Resolver: testResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://auth-self.example.invalid", UserHost: "auth-self.example.invalid", AdminHost: "auth-admin.example.invalid", ListenAddr: "127.0.0.1:41998"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	database, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "auth.sqlite"))+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(2)
	t.Cleanup(func() { database.Close() })
	if _, err = database.Exec(db.AgentRequestsSQL + db.AgentDriversSQL); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	f.m, err = New(Config{DB: database, ReadDB: database, Stack: stack, Secrets: vault, Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }, AdminAuthorize: func(context.Context, *sql.Tx) error {
		if f.denied.Load() {
			return errors.New("forbidden")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.m.client.issuer = server.URL
	return f
}

func (f *authFixture) jwt(t *testing.T, modify func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{"iss": f.issuer, "sub": "subject-fixture", "aud": "issued-client", "nonce": strings.Repeat("n", 43), "iat": f.clock.Load(), "exp": f.clock.Load() + 3600, "email": "operator@example.invalid"}
	if modify != nil {
		modify(claims)
	}
	enc := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": "fixture"})
	p, _ := json.Marshal(claims)
	input := enc(h) + "." + enc(p)
	hash := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, f.key, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + enc(signature)
}

func (f *authFixture) credentials(t *testing.T) Credentials {
	return Credentials{ClientID: "issued-client", AccessToken: "access-original", RefreshToken: "refresh-original", IDToken: f.jwt(t, nil), Scope: Scopes, Nonce: strings.Repeat("n", 43), ExpiresAt: f.clock.Load() + 30}
}

func (f *authFixture) importCredentials(t *testing.T) Status {
	t.Helper()
	target, err := f.m.Target(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Protect(target, f.credentials(t))
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.m.Import(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestIdentityClaimsAndTransferBinding(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	for name, modify := range map[string]func(map[string]any){
		"nonce":          func(c map[string]any) { c["nonce"] = "wrong" },
		"audience":       func(c map[string]any) { c["aud"] = "another-client" },
		"issuer":         func(c map[string]any) { c["iss"] = "https://other.example.invalid" },
		"expired":        func(c map[string]any) { c["exp"] = f.clock.Load() },
		"multi-audience": func(c map[string]any) { c["aud"] = []string{"issued-client", "another"} },
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := f.m.client.Validate(ctx, f.jwt(t, modify), "issued-client", strings.Repeat("n", 43)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid claims: %v", err)
			}
		})
	}
	target, err := f.m.Target(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creds := f.credentials(t)
	bad := creds
	bad.Scope = "openid offline_access resource.invoke"
	if _, err = Protect(target, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing consent accepted", err)
	}
	envelope, err := Protect(target, creds)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(envelope)
	for _, s := range []string{creds.AccessToken, creds.RefreshToken, creds.IDToken} {
		if strings.Contains(string(encoded), s) {
			t.Fatal("credential leaked into transfer")
		}
	}
	tampered := envelope
	tampered.HostID += "-other"
	if _, err = f.m.Import(ctx, tampered); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong target accepted", err)
	}
	tampered = envelope
	cipher, _ := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	cipher[0] ^= 1
	tampered.Ciphertext = base64.RawURLEncoding.EncodeToString(cipher)
	if _, err = f.m.Import(ctx, tampered); err == nil {
		t.Fatal("tampered transfer accepted")
	}
	status, err := f.m.Import(ctx, envelope)
	if err != nil || !status.Connected {
		t.Fatal(status, err)
	}
	if _, err = f.m.Import(ctx, envelope); err == nil {
		t.Fatal("replayed transfer accepted")
	}
	encoded, _ = json.Marshal(status)
	if strings.Contains(string(encoded), "access-original") || strings.Contains(string(encoded), "refresh-original") {
		t.Fatal("status leaked secrets")
	}
	f.denied.Store(true)
	if _, err = f.m.Status(ctx); err == nil {
		t.Fatal("unauthorized status")
	}
	if _, err = f.m.Target(ctx); err == nil {
		t.Fatal("unauthorized target")
	}
	if _, err = f.m.Import(ctx, envelope); err == nil {
		t.Fatal("unauthorized import")
	}
}

func TestRefreshSerializesRotationAndRetainsRegistrationOnLogout(t *testing.T) {
	f := newAuthFixture(t)
	connected := f.importCredentials(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, revision, err := f.m.AccessTokenSnapshot(context.Background())
			if err != nil || string(token) != "access-rotated" || revision != connected.Revision {
				t.Errorf("access generation: %q %d %v", token, revision, err)
			}
			clear(token)
		}()
	}
	wg.Wait()
	if f.refreshes.Load() != 1 {
		t.Fatalf("refresh reused: %d", f.refreshes.Load())
	}
	status, err := f.m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != connected.Revision {
		t.Fatal("refresh changed connection revision")
	}
	result, err := f.m.Logout(context.Background(), status.Revision)
	if err != nil || !result.RemoteRevocationConfirmed || f.revocations.Load() != 1 {
		t.Fatal(result, err)
	}
	target, err := f.m.Target(context.Background())
	if err != nil || target.ClientID != "issued-client" || target.Subject != "subject-fixture" {
		t.Fatal(target, err)
	}
	if _, err = f.m.AccessToken(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatal(err)
	}
}

func TestRefreshErrorsAndFailedPersistenceDoNotReuseRotatedToken(t *testing.T) {
	f := newAuthFixture(t)
	f.importCredentials(t)
	ctx := context.Background()
	f.mode.Store(1)
	if _, err := f.m.AccessToken(ctx); err == nil || strings.Contains(err.Error(), "secret-upstream-detail") {
		t.Fatal("unsafe transient failure", err)
	}
	status, _ := f.m.Status(ctx)
	if !status.Connected {
		t.Fatal("transient error disconnected session")
	}
	f.mode.Store(0)
	_, err := f.m.config.DB.Exec(`CREATE TRIGGER reject_rotation BEFORE UPDATE OF encrypted_credentials ON agent_chatgpt_session BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.AccessToken(ctx); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if _, err = f.m.config.DB.Exec("DROP TRIGGER reject_rotation"); err != nil {
		t.Fatal(err)
	}
	token, err := f.m.AccessToken(ctx)
	if err != nil || string(token) != "access-rotated" || f.refreshes.Load() != 2 {
		t.Fatalf("rotated token retried: %q %v %d", token, err, f.refreshes.Load())
	}
	clear(token)
	f.clock.Add(3600)
	f.mode.Store(2)
	if _, err = f.m.AccessToken(ctx); !errors.Is(err, ErrDisconnected) {
		t.Fatal(err)
	}
	status, _ = f.m.Status(ctx)
	if status.Connected || !status.NeedsLogin {
		t.Fatal(status)
	}
}

func TestLocalLoginUsesPKCEAndNeverExportsPlaintext(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	target, err := f.m.Target(ctx)
	if err != nil {
		t.Fatal(err)
	}
	localHost, err := NewHostID()
	if err != nil {
		t.Fatal(err)
	}
	var authorization url.Values
	f.codeExchange = func(w http.ResponseWriter, r *http.Request) {
		v := r.Form.Get("code_verifier")
		hash := sha256.Sum256([]byte(v))
		if base64.RawURLEncoding.EncodeToString(hash[:]) != authorization.Get("code_challenge") || r.Form.Get("redirect_uri") != authorization.Get("redirect_uri") || r.Form.Get("code") != "fixture-code" || r.Form.Get("client_id") != "issued-client" {
			t.Error("invalid code exchange")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-original", "refresh_token": "refresh-original", "id_token": f.jwt(t, func(c map[string]any) { c["nonce"] = authorization.Get("nonce") }), "token_type": "Bearer", "scope": Scopes, "expires_in": 3600})
	}
	envelope, err := f.m.client.Login(ctx, target, localHost, func(link string) error {
		u, e := url.Parse(link)
		if e != nil {
			return e
		}
		authorization = u.Query()
		if u.Path != "/api/accounts/authorize" || authorization.Get("client_id") != "dynamic_agent_client" || authorization.Get("ext_agent_host_id") != localHost || authorization.Get("agent_name_hint") != "NonbiriAPI" || authorization.Get("scope") != Scopes || authorization.Get("code_challenge_method") != "S256" {
			return fmt.Errorf("unexpected authorization request")
		}
		callback, e := url.Parse(authorization.Get("redirect_uri"))
		if e != nil {
			return e
		}
		q := url.Values{"state": {"wrong"}, "code": {"fixture-code"}, "client_id": {"issued-client"}}
		callback.RawQuery = q.Encode()
		resp, e := http.Get(callback.String())
		if e != nil {
			return e
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			return fmt.Errorf("invalid state consumed")
		}
		q.Set("state", authorization.Get("state"))
		callback.RawQuery = q.Encode()
		resp, e = http.Get(callback.String())
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("callback rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.m.Import(ctx, envelope)
	if err != nil || !status.Connected {
		t.Fatal(status, err)
	}
}

func TestAccountReplacementIsExplicitAuthenticatedAndAtomic(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	before := f.importCredentials(t)
	newCredentials := f.credentials(t)
	newCredentials.ClientID = "second-client"
	newCredentials.AccessToken = "access-second"
	newCredentials.ExpiresAt = f.clock.Load() + 3600
	newCredentials.IDToken = f.jwt(t, func(c map[string]any) {
		c["aud"] = "second-client"
		c["sub"] = "second-subject"
		c["email"] = "second@example.invalid"
	})
	target, err := f.m.Target(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := Protect(target, newCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.Import(ctx, ordinary); !errors.Is(err, ErrInvalid) {
		t.Fatal("ordinary reconnect changed account", err)
	}
	tampered := ordinary
	tampered.NewAccount = true
	if _, err = f.m.Import(ctx, tampered); !errors.Is(err, ErrInvalid) {
		t.Fatal("replacement intent was not authenticated", err)
	}
	target, err = f.m.TargetFor(ctx, true)
	if err != nil || !target.NewAccount || target.ClientID != "" || target.Subject != "" {
		t.Fatal(target, err)
	}
	current, err := f.m.Status(ctx)
	if err != nil || current != before {
		t.Fatal("target changed current connection", current, err)
	}
	bad := newCredentials
	bad.IDToken = f.jwt(t, func(c map[string]any) { c["aud"] = "second-client"; c["nonce"] = "wrong" })
	badEnvelope, err := Protect(target, bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.Import(ctx, badEnvelope); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid replacement accepted", err)
	}
	current, err = f.m.Status(ctx)
	if err != nil || current != before {
		t.Fatal("failed replacement changed connection", current, err)
	}
	envelope, err := Protect(target, newCredentials)
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.m.Import(ctx, envelope)
	if err != nil || !status.Connected || status.Revision != before.Revision+1 || status.AccountLabel != "second@example.invalid" {
		t.Fatal(status, err)
	}
	retained, err := f.m.Target(ctx)
	if err != nil || retained.ClientID != "second-client" || retained.Subject != "second-subject" || retained.HostID != target.HostID {
		t.Fatal(retained, err)
	}
	if _, err = f.m.Import(ctx, envelope); err == nil {
		t.Fatal("account replacement replay accepted")
	}
	token, revision, err := f.m.AccessTokenSnapshot(ctx)
	if err != nil || string(token) != "access-second" || revision != status.Revision {
		t.Fatalf("token and generation differ: %q %d %v", token, revision, err)
	}
	clear(token)
}

func TestConcurrentAccountReplacementKeepsCredentialGenerationTogether(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	before := f.importCredentials(t)
	token, _, err := f.m.AccessTokenSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clear(token)
	target, err := f.m.TargetFor(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	creds := f.credentials(t)
	creds.ClientID = "second-client"
	creds.AccessToken = "access-second"
	creds.ExpiresAt = f.clock.Load() + 3600
	creds.IDToken = f.jwt(t, func(c map[string]any) { c["aud"] = "second-client"; c["sub"] = "second-subject" })
	envelope, err := Protect(target, creds)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				key, revision, e := f.m.AccessTokenSnapshot(ctx)
				if e != nil || !(string(key) == "access-rotated" && revision == before.Revision || string(key) == "access-second" && revision == before.Revision+1) {
					t.Errorf("mixed credential generation: %q %d %v", key, revision, e)
				}
				clear(key)
			}
		}()
	}
	close(start)
	_, err = f.m.Import(ctx, envelope)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}
