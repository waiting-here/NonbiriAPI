package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type programMux struct{ *http.ServeMux }

func (m programMux) RegisterUserRoute(method, path string, h resources.AuthorizedUserHandler) error {
	m.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		h(w, r, resources.UserPrincipal{UserID: 1})
	})
	return nil
}
func (m programMux) RegisterAdminRoute(method, path string, h http.Handler) error {
	m.Handle(method+" "+path, h)
	return nil
}

func TestReleaseDisablesUploadedProgramsAtRegisteredRoutesAndWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured-worker")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NONBIRI_COMPANION_WORKER", path)
	s := New(host.Services{})
	if s.worker.Path != "" {
		t.Fatal("environment enabled the companion worker")
	}
	s.worker = runner.Client{Path: path}
	ctx := context.Background()
	if _, _, err := s.worker.Compile(ctx, "unused"); !errors.Is(err, runner.ErrUploadsDisabled) {
		t.Fatal("compile accepted a manually configured worker", err)
	}
	if _, err := s.worker.Decide(ctx, runner.Job{}, nil); !errors.Is(err, runner.ErrUploadsDisabled) {
		t.Fatal("execution accepted a manually configured worker", err)
	}
	for _, game := range []string{"bidding", "likes", "gwent"} {
		t.Run(game, func(t *testing.T) {
			if runner.UploadsEnabled || runner.GameUploadsEnabled(game) || runner.UploadsEnabledFor(game) {
				t.Fatal("release contains an enabled upload switch")
			}
			if _, _, err := s.compile(ctx, game, "unused"); !errors.Is(err, runner.ErrUploadsDisabled) {
				t.Fatal("compilation was admitted", err)
			}
			for _, b := range []Binding{
				{Game: game, Name: "private", OwnerID: 1, Version: 1, Difficulty: 1},
				{Game: game, Name: builtin, Official: true, Version: 1, Difficulty: 1},
			} {
				if _, err := s.Decide(ctx, b, nil, nil); !errors.Is(err, runner.ErrUploadsDisabled) {
					t.Fatal("issued an uploaded program decision", b, err)
				}
			}
			mux := programMux{http.NewServeMux()}
			if err := s.RegisterRoutes(game, host.Registrars{User: mux, Admin: mux}); err != nil {
				t.Fatal(err)
			}
			base := "/api/games/" + game + "/companion"
			for _, route := range []struct{ method, path string }{
				{"GET", base + "/kit"}, {"GET", base + "/programs?name=private&version=1"},
				{"POST", base + "/programs"}, {"POST", base + "/programs/activate"}, {"POST", base + "/programs/delete"},
				{"POST", "/admin" + base + "/versions"}, {"POST", "/admin" + base + "/activate"},
			} {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				mux.ServeHTTP(w, req)
				if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "built-in companion") {
					t.Errorf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
				}
			}
		})
	}
}

type readFixtureUser struct{}

func (readFixtureUser) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }

func TestCompanionSettingRemainsIndependentOfReleaseUploadSwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	dbfixture.Materialize(t, path)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	zero := db.EncodeU128(db.U128{})
	_, err = database.Exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('operator',1,?,?,?,?,?,?,?,?,100,100)`, zero, zero, zero, zero, zero, zero, zero, zero)
	if err != nil {
		t.Fatal(err)
	}
	s := New(host.Services{ReadDatabase: database, Database: database, Now: time.Now,
		UserAuthorizer: readFixtureUser{}, AdminAuthorizer: host.AdminAuthorizerFunc(func(context.Context, *sql.Tx) error { return nil })})
	mux := programMux{http.NewServeMux()}
	if err := s.RegisterRoutes("gwent", host.Registrars{Admin: mux, User: mux}); err != nil {
		t.Fatal(err)
	}
	const base = "/api/games/gwent/companion"
	read := func(method, path, body, key string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for i, enabled := range []bool{false, true} {
		body := fmt.Sprintf(`{"enabled":%t}`, enabled)
		admin := read("POST", "/admin"+base+"/settings", body, fmt.Sprintf("%022d", i))
		adminGet := read("GET", "/admin"+base, "", "")
		player := read("GET", base+"/programs", "", "")
		for _, payload := range []map[string]any{admin, adminGet, player} {
			if payload["enabled"] != enabled || payload["upload_available"] != false ||
				len(payload["programs"].([]any)) != 0 || payload["official"].(map[string]any)["version"] != float64(0) {
				t.Fatal(payload)
			}
		}
		for _, payload := range []map[string]any{admin, adminGet} {
			if payload["upload_global_enabled"] != false || payload["upload_game_enabled"] != false {
				t.Fatal(payload)
			}
		}
		if _, ok := player["upload_global_enabled"]; ok {
			t.Fatal("operator settings in player response")
		}
	}
}
