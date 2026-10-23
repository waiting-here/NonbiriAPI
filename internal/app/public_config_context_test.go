package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestPublicBootstrapCancellationReleasesPoolWait(t *testing.T) {
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "bootstrap.db")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, endpoint := range []struct {
		path  string
		serve func(*db.Store, http.ResponseWriter, *http.Request)
	}{
		{"/api/config", servePublicConfig},
		{"/admin/api/branding", servePublicBranding},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			var held []*sql.Conn
			defer func() {
				for _, conn := range held {
					_ = conn.Close()
				}
			}()
			for range store.ReadDB().Stats().MaxOpenConnections {
				conn, err := store.ReadDB().Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, conn)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, endpoint.path, nil)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { endpoint.serve(store, response, request); close(done) }()
			select {
			case <-done:
				if response.Code != http.StatusInternalServerError {
					t.Fatalf("canceled bootstrap status=%d", response.Code)
				}
			case <-time.After(2 * time.Second):
				for _, conn := range held {
					_ = conn.Close()
				}
				held = nil
				<-done
				t.Fatal("canceled bootstrap kept waiting for a read connection")
			}
		})
	}
}
