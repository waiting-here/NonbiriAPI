package app

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func TestCodexApplicationRoutesAuthorityAndShutdown(t *testing.T) {
	f := newActivityWireFixture(t)
	if _, err := f.store.DB().Exec(db.CodexSessionSQL); err != nil {
		t.Fatal(err)
	}
	app := f.app.Load()
	if app.codex == nil {
		t.Fatal("Codex manager was not retained by the application")
	}
	if _, err := app.codex.Status(t.Context()); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("manager accepted a missing administrator actor: %v", err)
	}
	status := f.call("GET", "/admin/api/ai/codex", nil, f.adminCookie, true)
	if status["connected"] != false || status["revision"] != "0" {
		t.Fatalf("unexpected disconnected state: %v", status)
	}
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/admin/api/ai/codex", nil},
		{"POST", "/admin/api/ai/codex/login", map[string]string{"expected_revision": "0"}},
		{"POST", "/admin/api/ai/codex/cancel", map[string]string{"login_id": "missing"}},
		{"POST", "/admin/api/ai/codex/logout", map[string]string{"expected_revision": "0"}},
	} {
		for _, seat := range []int{0, 2, 3} {
			response := f.request(route.method, route.path, route.body, f.users[seat].Cookie, true)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Fatalf("non-administrator %s status=%d", route.path, response.Code)
			}
		}
	}
	stale := f.request("POST", "/admin/api/ai/codex/login", map[string]string{"expected_revision": "1"}, f.adminCookie, true)
	if stale.Code != http.StatusConflict {
		t.Fatalf("login did not reach the configured manager: %d %s", stale.Code, stale.Body.String())
	}
	cancel := f.request("POST", "/admin/api/ai/codex/cancel", map[string]string{"login_id": "missing"}, f.adminCookie, true)
	if cancel.Code != http.StatusConflict {
		t.Fatalf("cancel did not reach the configured manager: %d %s", cancel.Code, cancel.Body.String())
	}
	status = f.call("POST", "/admin/api/ai/codex/logout", map[string]string{"expected_revision": "0"}, f.adminCookie, true)
	if status["connected"] != false || status["revision"] != "1" {
		t.Fatalf("disconnect did not reach the configured manager: %v", status)
	}
	envelope, err := f.vault.SealCodex([]byte("synthetic-wiring-only"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().Exec("UPDATE agent_codex_session SET revision=2,encrypted_credentials=? WHERE id=1", envelope); err != nil {
		t.Fatal(err)
	}
	active, release, err := app.codex.RequestContext(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("application shutdown left the Codex connection active")
	}
	if _, _, err = app.codex.RequestContext(t.Context(), 2); !errors.Is(err, codexauth.ErrClosed) {
		t.Fatalf("Codex manager still accepts work after application shutdown: %v", err)
	}
	if err = f.store.DB().PingContext(t.Context()); err != nil {
		t.Fatalf("application closed caller-owned storage: %v", err)
	}
	if _, err = f.vault.OpenCodex(envelope); err != nil {
		t.Fatalf("application closed caller-owned vault: %v", err)
	}
}

type codexWiringResolver struct{}

func (codexWiringResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if host == "auth.openai.com" {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

type codexWiringDialer struct {
	started chan struct{}
	once    sync.Once
}

func (d *codexWiringDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	d.once.Do(func() { close(d.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCodexPartialApplicationCloseCancelsPendingLogin(t *testing.T) {
	vault := processTestVault(t)
	store := processTestStore(t, filepath.Join(t.TempDir(), "codex-close.sqlite"), vault)
	if _, err := store.DB().Exec(db.CodexSessionSQL); err != nil {
		t.Fatal(err)
	}
	dialer := &codexWiringDialer{started: make(chan struct{})}
	stack, err := egress.NewStack(egress.StackOptions{Resolver: codexWiringResolver{}, Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	if err = stack.AddSelfOrigins(t.Context(), &config.Config{SiteBaseURL: "https://self.invalid", UserHost: "self.invalid", AdminHost: "admin.invalid", ListenAddr: "127.0.0.1:41997"}); err != nil {
		t.Fatal(err)
	}
	manager, err := codexauth.New(codexauth.Config{
		DB: store.DB(), ReadDB: store.ReadDB(), Stack: stack, Secrets: vault,
		AdminAuthorize: func(context.Context, *sql.Tx) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &application{codex: manager, egress: stack}
	t.Cleanup(func() { _ = app.Close() })
	result := make(chan error, 1)
	go func() { _, err := manager.Begin(t.Context(), 0); result <- err }()
	waitForProcessTestSignal(t, dialer.started, "synthetic Codex login dispatch")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err = app.CloseContext(ctx); err != nil {
		t.Fatalf("partial application failed to stop pending login: %v", err)
	}
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("cancelled login succeeded")
		}
	case <-ctx.Done():
		t.Fatal("login outlived application shutdown")
	}
	if _, err = manager.Begin(t.Context(), 0); !errors.Is(err, codexauth.ErrClosed) {
		t.Fatalf("partial application retained an open manager: %v", err)
	}
}
