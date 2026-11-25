package duel_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetiredChallengeEntrypoints(t *testing.T) {
	for _, game := range []string{"bidding", "gwent"} {
		t.Run(game, func(t *testing.T) {
			f := newFixture(t, game)
			mux := http.NewServeMux()
			r := &registrar{mux: mux, identity: f.identity(0)}
			if err := f.s.RegisterRoutes(r, r); err != nil {
				t.Fatal(err)
			}
			if err := f.s.RegisterAdminRoutes(adminRegistrar{mux}); err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []string{"GET /api/games/GAME/ai", "POST /api/games/GAME/ai/preference", "GET /admin/api/games/GAME/ai", "POST /admin/api/games/GAME/ai/settings", "POST /admin/api/games/GAME/ai/policies", "POST /admin/api/games/GAME/ai/bots", "POST /admin/api/games/GAME/ai/preview"} {
				method, path, _ := strings.Cut(endpoint, " ")
				req := httptest.NewRequest(method, strings.Replace(path, "GAME", game, 1), nil)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatal(endpoint, w.Code)
				}
			}
			if game == "gwent" {
				raw, err := f.s.PublicCatalog()
				if err != nil {
					t.Fatal(err)
				}
				var catalog struct{ Modes map[string]json.RawMessage }
				if err := json.Unmarshal(raw, &catalog); err != nil {
					t.Fatal(err)
				}
				if len(catalog.Modes) == 0 {
					t.Fatal("missing current catalogs")
				}
				if _, ok := catalog.Modes["ai"]; ok {
					t.Fatal("obsolete AI catalog published")
				}
			}
			if f.s.Available("ai", "") {
				t.Fatal("retired mode advertised as available")
			}
		})
	}
}
