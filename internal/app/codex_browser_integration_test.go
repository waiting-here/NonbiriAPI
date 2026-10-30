package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const (
	codexBrowserAccess    = "synthetic-codex-browser-access"
	codexBrowserRefresh   = "synthetic-codex-browser-refresh"
	codexBrowserAccount   = "synthetic-codex-private-account"
	codexBrowserRequestID = "synthetic-codex-browser-upstream"
	codexBrowserPublicIP  = "93.184.216.34"
)

type codexBrowserCounts struct {
	Models          int `json:"models"`
	Usage           int `json:"usage"`
	ForbiddenModels int `json:"forbidden_models"`
	ForbiddenUsage  int `json:"forbidden_usage"`
	Denied          int `json:"denied"`
}

type codexBrowserUpstream struct {
	mu              sync.Mutex
	server          *httptest.Server
	ordinaryAddress string
	forbidden       bool
	used            int
	counts          codexBrowserCounts
}

// Only this browser fixture replaces DNS, dialing and certificate trust. The
// application still uses its fixed Codex URLs and its complete outbound stack.
func newCodexBrowserUpstream(t *testing.T, ordinaryAddress string) *codexBrowserUpstream {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Synthetic Codex browser fixture"},
		DNSNames:     []string{"chatgpt.com"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)

	fixture := &codexBrowserUpstream{ordinaryAddress: ordinaryAddress, used: 25}
	fixture.server = httptest.NewUnstartedServer(http.HandlerFunc(fixture.serve))
	fixture.server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	fixture.server.StartTLS()
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		fixture.server.Close()
		http.DefaultTransport = previous
		transport.CloseIdleConnections()
		if counts := fixture.snapshot(); counts.Denied != 0 {
			t.Errorf("Codex browser fixture rejected %d unplanned requests", counts.Denied)
		}
	})
	return fixture
}

func (f *codexBrowserUpstream) configure(options *egress.StackOptions) {
	options.Resolver, options.Dialer = f, f
}
func (f *codexBrowserUpstream) deny() {
	f.mu.Lock()
	f.counts.Denied++
	f.mu.Unlock()
}
func (f *codexBrowserUpstream) snapshot() codexBrowserCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts
}
func (f *codexBrowserUpstream) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	switch host {
	case "chatgpt.com":
		return []netip.Addr{netip.MustParseAddr(codexBrowserPublicIP)}, nil
	case "localhost":
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	case "native-chat.example.test", "cache.example.test":
		// Existing management cases actively discover these synthetic failure
		// targets. Fail resolution without real DNS, dialing or a Codex denial.
		return nil, errors.New("expected synthetic management discovery resolution failure")
	default:
		f.deny()
		return nil, errors.New("unplanned browser fixture hostname")
	}
}
func (f *codexBrowserUpstream) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch address {
	case codexBrowserPublicIP + ":443":
		address = f.server.Listener.Addr().String()
	case f.ordinaryAddress:
		// Existing management fixtures retain their isolated local upstream.
	default:
		f.deny()
		return nil, errors.New("unplanned browser fixture dial")
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
func (f *codexBrowserUpstream) serve(w http.ResponseWriter, r *http.Request) {
	models := r.URL.Path == "/backend-api/codex/models" && r.URL.RawQuery == "client_version=0.0.0"
	usage := r.URL.Path == "/backend-api/wham/usage" && r.URL.RawQuery == ""
	// The protected egress stack canonicalizes the HTTPS origin with its port.
	if r.Method != http.MethodGet || r.Host != "chatgpt.com:443" || (!models && !usage) ||
		r.Header.Get("Authorization") != "Bearer "+codexBrowserAccess ||
		r.Header.Get("ChatGPT-Account-ID") != codexBrowserAccount ||
		r.Header.Get("originator") != "nonbiriapi" || r.Header.Get("Accept") != "application/json" {
		f.deny()
		http.Error(w, "unplanned synthetic Codex request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	forbidden, used := f.forbidden, f.used
	if models {
		f.counts.Models++
		if forbidden {
			f.counts.ForbiddenModels++
		}
	} else {
		f.counts.Usage++
		if forbidden {
			f.counts.ForbiddenUsage++
		}
	}
	f.mu.Unlock()
	w.Header().Set("X-Request-ID", codexBrowserRequestID)
	if forbidden {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>synthetic-codex-block-page</title><body>synthetic upstream refusal</body></html>"))
		return
	}
	if models {
		writeImageFixtureJSON(w, map[string]any{"models": []map[string]string{
			{"slug": "synthetic-codex-model-a", "display_name": "Synthetic model A", "visibility": "list"},
			{"slug": "synthetic-codex-model-b", "display_name": "Synthetic model B", "visibility": "list"},
		}})
		return
	}
	writeImageFixtureJSON(w, map[string]any{
		"plan_type": "synthetic-browser-plan", "workspace_name": "Synthetic quota workspace",
		"rate_limit": map[string]any{"primary_window": map[string]any{
			"used_percent": used, "limit_window_seconds": 1200, "reset_at": time.Now().Add(20 * time.Minute).Unix(),
		}},
	})
}

// The caller has already checked the separate fixture control token. Connecting
// seeds synthetic material only; teardown disconnects through the normal API.
func (f *codexBrowserUpstream) control(w http.ResponseWriter, r *http.Request, app *imageBrowserFixture) bool {
	if r.URL.Path == "/codex-fixture/stats" && r.Method == http.MethodGet {
		writeImageFixtureJSON(w, f.snapshot())
		return true
	}
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/codex-fixture/connect":
		expires := time.Now().Add(time.Hour).Unix()
		raw, err := json.Marshal(codexauth.Credentials{
			AccessToken: codexBrowserAccess, RefreshToken: codexBrowserRefresh,
			AccountID: codexBrowserAccount, Subject: "synthetic-codex-subject", ExpiresAt: expires,
		})
		if err != nil {
			http.Error(w, "synthetic credential failed", 500)
			return true
		}
		encrypted, err := app.vault.SealCodex(raw)
		clear(raw)
		if err != nil {
			http.Error(w, "synthetic credential failed", 500)
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := app.store.DB().ExecContext(ctx, `INSERT INTO agent_codex_session
(id,revision,encrypted_credentials,account_id,account_label,expires_at,updated_at)
VALUES(1,1,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET revision=revision+1,encrypted_credentials=excluded.encrypted_credentials,
 account_id=excluded.account_id,account_label=excluded.account_label,expires_at=excluded.expires_at,updated_at=excluded.updated_at
WHERE agent_codex_session.encrypted_credentials=''`,
			encrypted, codexBrowserAccount, "Synthetic browser connection", expires, time.Now().UnixMilli())
		if err != nil {
			http.Error(w, "synthetic connection failed", 500)
			return true
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			http.Error(w, "connection must be disconnected", 409)
			return true
		}
		f.mu.Lock()
		f.forbidden, f.used = false, 25
		f.mu.Unlock()
	case "/codex-fixture/forbidden":
		f.mu.Lock()
		f.forbidden = true
		f.mu.Unlock()
	case "/codex-fixture/success":
		f.mu.Lock()
		f.forbidden, f.used = false, 40
		f.mu.Unlock()
	default:
		return false
	}
	writeImageFixtureJSON(w, f.snapshot())
	return true
}
