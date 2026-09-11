package host

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestRegistryRejectsConflictsAndDefensivelyCopiesDeclarations(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*game.ModuleDescriptor)
	}{
		{"id", func(d *game.ModuleDescriptor) { d.ID = "free" }},
		{"version", func(d *game.ModuleDescriptor) { d.Version = 0 }},
		{"order", func(d *game.ModuleDescriptor) { d.StableOrder = 0 }},
		{"prefix", func(d *game.ModuleDescriptor) { d.ResourcePrefixes = []string{"free_"} }},
		{"board", func(d *game.ModuleDescriptor) { d.BoardIDs = []string{"free-score"} }},
		{"key", func(d *game.ModuleDescriptor) { d.Codec = freeCodec{"free"} }},
		{"missing codec", func(d *game.ModuleDescriptor) { d.Codec = nil }},
		{"route", func(d *game.ModuleDescriptor) {
			d.Routes = []game.RouteDeclaration{{Station: "user", Method: "GET", Pattern: "/api/games/free/state"}}
		}},
		{"ambiguous route", func(d *game.ModuleDescriptor) {
			d.Routes = []game.RouteDeclaration{{Station: "user", Method: "GET", Pattern: "/{name}/b"}}
		}},
		{"malformed route", func(d *game.ModuleDescriptor) {
			d.Routes = []game.RouteDeclaration{{Station: "user", Method: "GET", Pattern: "/broken/{name"}}
		}},
		{"snapshot collision", func(d *game.ModuleDescriptor) { d.SnapshotFields = []string{"balance"} }},
		{"continuation collision", func(d *game.ModuleDescriptor) { d.ContinuationIDs = []string{"free-resume"} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			registry := game.NewRegistry()
			first := freeDescriptor("free", 0)
			first.BoardIDs = []string{"free-score"}
			first.ContinuationIDs = []string{"free-resume"}
			if mutate.name == "ambiguous route" {
				first.Routes = []game.RouteDeclaration{{Station: "user", Method: "GET", Pattern: "/a/{name}"}}
			}
			if err := registry.Register(first); err != nil {
				t.Fatal(err)
			}
			second := freeDescriptor("other", 1)
			mutate.change(&second)
			err := registry.Register(second)
			if err == nil {
				err = registry.Seal()
			}
			if err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
	registry := game.NewRegistry()
	descriptor := freeDescriptor("free", 0)
	if err := registry.Register(descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.ResourcePrefixes[0] = "foreign_"
	descriptor.Routes[0].Pattern = "/foreign"
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	copy := registry.Descriptors()[0]
	copy.ResourcePrefixes[0] = "again_"
	copy.Routes[0].Pattern = "/again"
	keys := copy.Codec.Keys()
	keys[0] = "foreign"
	actual := registry.Descriptors()[0]
	if actual.ResourcePrefixes[0] != "free_" || actual.Routes[0].Pattern != "/api/games/free/state" || actual.Codec.Keys()[0] != "game_free_enabled" {
		t.Fatal("mutable registry escaped")
	}
	if err := registry.Register(freeDescriptor("late", 1)); err == nil {
		t.Fatal("registration after seal accepted")
	}
}

func TestHostRejectsEveryMissingCapabilityAndInvalidStartupTime(t *testing.T) {
	fixture := newGameFixture(t, nil)
	registry := game.NewRegistry()
	if err := registry.Register(freeDescriptor("free", 0)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	moduleType := reflect.TypeOf(Module{})
	for i := 0; i < moduleType.NumField(); i++ {
		t.Run(moduleType.Field(i).Name, func(t *testing.T) {
			module := inertModule()
			reflect.ValueOf(module).Elem().Field(i).SetZero()
			service, err := New(Options{Database: fixture.database, Registry: registry, Factories: map[string]Factory{"free": func(Services) (*Module, error) { return module, nil }}, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth})
			if err == nil {
				service.Close()
				t.Fatal("missing capability accepted")
			}
		})
	}
	called := false
	_, err := New(Options{Database: fixture.database, Registry: registry, Factories: map[string]Factory{"free": func(Services) (*Module, error) { called = true; return inertModule(), nil }}, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth, Now: func() time.Time { return time.Unix(-1, 0) }})
	if err == nil || called {
		t.Fatal("invalid clock reached factory")
	}
	if _, err := fixture.service.RecoverModule(context.Background(), "fishing", fixtureNow, 0, time.Time{}); err == nil {
		t.Fatal("invalid recovery budget accepted")
	}
	if _, err := fixture.service.RetainModule(context.Background(), "fishing", fixtureNow, 0, time.Time{}); err == nil {
		t.Fatal("invalid retention budget accepted")
	}
}

func TestHostRouteBindingRequiresDeclaredAuthenticationAndCompleteSet(t *testing.T) {
	fixture := newGameFixture(t, nil)
	for _, mode := range []string{"missing", "extra", "wrong auth", "duplicate", "valid"} {
		t.Run(mode, func(t *testing.T) {
			registry := game.NewRegistry()
			if err := registry.Register(freeDescriptor("free", 0)); err != nil {
				t.Fatal(err)
			}
			if err := registry.Seal(); err != nil {
				t.Fatal(err)
			}
			module := inertModule()
			module.RegisterRoutes = func(routes Registrars) error {
				if mode == "missing" {
					return nil
				}
				path := "/api/games/free/state"
				if mode == "extra" {
					path = "/api/games/free/other"
				}
				if mode == "wrong auth" {
					return routes.Continuation.RegisterContinuationUserRoute("GET", path, func(http.ResponseWriter, *http.Request, resources.ContinuationUserPrincipal) {})
				}
				h := func(http.ResponseWriter, *http.Request, resources.UserPrincipal) {}
				if err := routes.User.RegisterUserRoute("GET", path, h); err != nil {
					return err
				}
				if mode == "duplicate" {
					return routes.User.RegisterUserRoute("GET", path, h)
				}
				return nil
			}
			service, err := New(Options{Database: fixture.database, Registry: registry, Factories: map[string]Factory{"free": func(Services) (*Module, error) { return module, nil }}, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			err = service.RegisterRoutes((&capturedRoutes{}).all())
			if (err == nil) != (mode == "valid") {
				t.Fatalf("route binding: %v", err)
			}
		})
	}
}

func TestConfigMultiModuleFailureRollsBackAndRevisionAdvancesOnce(t *testing.T) {
	fixture := newGameFixture(t, nil)
	before, err := fixture.service.ReadGamesConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"expected_revision":"` + before.Revision + `","fishing":{"bait_prices":{"worm":"2501"}},"linklink":{"enabled":true,"specs":{"6x8":{"enabled":true,"price":"1"}}},"rps":{"enabled":true,"modes":{"quick":{"enabled":true,"base":"1"}}}}`)
	if _, err := fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(400)); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("RPS not ready: %v", err)
	}
	after, err := fixture.service.ReadGamesConfig(context.Background())
	if err != nil || !equalJSONValue(t, before, after) {
		t.Fatal("failed multi-module patch was partially written")
	}
	fixture.service.modules["rps"].ReadyTx = func(context.Context, *sql.Tx) bool { return true }
	after, err = fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(400))
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := fixture.database.QueryRow(`SELECT revision FROM config_revisions WHERE domain='games'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if after.Revision == before.Revision || revision != 2 {
		t.Fatalf("revision must advance once: %s -> %s / %d", before.Revision, after.Revision, revision)
	}
	replay, err := fixture.service.PatchGamesConfig(context.Background(), body, validTestKey(400))
	if err != nil || !equalJSONValue(t, after, replay) {
		t.Fatal("multi-module replay changed result")
	}
}
