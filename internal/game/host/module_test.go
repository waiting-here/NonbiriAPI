package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type freeCodec struct{ id string }
type freeWire struct {
	Enabled bool `json:"enabled"`
	Limit   int  `json:"limit"`
}
type freeValue struct {
	id string
	freeWire
}

func (codec freeCodec) Keys() []string {
	return []string{"game_" + codec.id + "_enabled", "game_" + codec.id + "_limit"}
}
func (codec freeCodec) ValidatePatch(raw json.RawMessage) error {
	var patch struct {
		Enabled *bool `json:"enabled,omitempty"`
		Limit   *int  `json:"limit,omitempty"`
	}
	return game.DecodeConfigPatch(raw, &patch)
}
func (codec freeCodec) Compile(raw map[string]string) (game.ConfigValue, error) {
	enabled, err := game.RawBool(raw, codec.Keys()[0], false)
	if err != nil {
		return nil, err
	}
	limit, err := game.RawInt(raw, codec.Keys()[1], 1, 1, 10)
	if err != nil {
		return nil, err
	}
	return freeValue{codec.id, freeWire{enabled, limit}}, nil
}
func (codec freeCodec) CompileWire(raw json.RawMessage, _ bool) (game.ConfigValue, error) {
	var value freeWire
	if err := game.DecodeConfigPatch(raw, &value); err != nil {
		return nil, err
	}
	if value.Limit < 1 || value.Limit > 10 {
		return nil, game.ErrInvalidConfig
	}
	return freeValue{codec.id, value}, nil
}
func (codec freeCodec) Merge(current game.ConfigValue, patch json.RawMessage, master bool) (game.ConfigValue, error) {
	if err := codec.ValidatePatch(patch); err != nil {
		return nil, err
	}
	var value freeWire
	if err := game.MergeConfigFields(current.Wire(), patch, &value); err != nil {
		return nil, err
	}
	return codec.CompileWire(game.ConfigJSON(value), master)
}
func (value freeValue) Enabled() bool    { return value.freeWire.Enabled }
func (value freeValue) NeedsReady() bool { return value.Enabled() }
func (value freeValue) Raw() map[string]string {
	return map[string]string{"game_" + value.id + "_enabled": game.BoolRaw(value.Enabled()), "game_" + value.id + "_limit": strconv.Itoa(value.Limit)}
}
func (value freeValue) Wire() json.RawMessage { return game.ConfigJSON(value.freeWire) }
func (value freeValue) UserWire(available func(string, string) bool) json.RawMessage {
	return game.ConfigJSON(map[string]any{"enabled": value.Enabled(), "available": available("", ""), "limit": value.Limit})
}

func freeDescriptor(id string, order int) game.ModuleDescriptor {
	return game.ModuleDescriptor{ID: id, Version: 1, StableOrder: order, ResourcePrefixes: []string{id + "_"}, HomeRouteID: "game-" + id, Codec: freeCodec{id}, Routes: []game.RouteDeclaration{{Station: "user", Method: "GET", Pattern: "/api/games/" + id + "/state"}}}
}

type testFinalizer struct {
	done      bool
	committed *int
	aborted   *int
}

func (f *testFinalizer) Commit() bool {
	if f.done {
		return false
	}
	f.done = true
	*f.committed++
	return true
}
func (f *testFinalizer) Abort() bool {
	if f.done {
		return false
	}
	f.done = true
	*f.aborted++
	return true
}

type freeRow struct {
	ID        string
	UserID    int64
	ExpiresAt int64
}
type freeRuntime struct {
	shared                                Services
	ready                                 bool
	validated, recovered, started, closed int
	committed, aborted                    int
	lastTx                                *sql.Tx
}

func (runtime *freeRuntime) rows(ctx context.Context, tx *sql.Tx, user int64) ([]freeRow, error) {
	runtime.lastTx = tx
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,expires_at FROM free_records WHERE user_id=? ORDER BY id LIMIT 11`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []freeRow{}
	for rows.Next() {
		var item freeRow
		if err := rows.Scan(&item.ID, &item.UserID, &item.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (runtime *freeRuntime) module() *Module {
	module := inertModule()
	module.ValidatePersistedState = func(context.Context) error { runtime.validated++; return nil }
	module.RecoverBeforeListen = func(context.Context, int64, int, time.Time) (WorkResult, error) {
		runtime.recovered++
		return WorkResult{}, nil
	}
	module.StartWorker = func(context.Context) error { runtime.started++; return nil }
	module.Close = func() error { runtime.closed++; return nil }
	module.ReadyTx = func(_ context.Context, tx *sql.Tx) bool { runtime.lastTx = tx; return runtime.ready }
	module.UserSnapshotTx = func(_ context.Context, tx *sql.Tx, _ int64, _ int64, value game.ConfigValue) (game.UserSnapshot, error) {
		runtime.lastTx = tx
		return game.UserSnapshot{Config: value.UserWire(module.Available)}, nil
	}
	module.RegisterRoutes = func(routes Registrars) error {
		return routes.User.RegisterUserRoute("GET", "/api/games/free/state", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			tx, err := runtime.shared.Database.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				writeError(w, err)
				return
			}
			defer tx.Rollback()
			if err := runtime.shared.UserAuthorizer.AuthorizeUserMutation(r.Context(), tx, p.UserID); err != nil {
				writeError(w, err)
				return
			}
			rows, err := runtime.rows(r.Context(), tx, p.UserID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, 200, rows)
		})
	}
	module.HomeSummaryTx = func(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
		rows, err := runtime.rows(ctx, tx, user)
		if err != nil {
			return game.HomeSummary{}, err
		}
		out := game.HomeSummary{}
		for _, row := range rows {
			out.PendingResults = append(out.PendingResults, game.PendingResult{Game: "free", ResourceID: row.ID, CreatedAt: row.ExpiresAt, RouteID: "game-free"})
		}
		return out, nil
	}
	module.ActiveCountsTx = func(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
		runtime.lastTx = tx
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM free_records`).Scan(&count); err != nil {
			return game.ActiveCounts{}, err
		}
		out := game.ActiveCounts{}
		if count > 0 {
			out.Games = []game.GameCount{{Game: "free", Count: strconv.Itoa(count)}}
		}
		return out, nil
	}
	module.ExportTx = func(ctx context.Context, tx *sql.Tx, user, _ int64, limit int) (any, Finalizer, error) {
		rows, err := runtime.rows(ctx, tx, user)
		if len(rows) > limit {
			return nil, nil, ErrResourceLimit
		}
		return rows, &testFinalizer{committed: &runtime.committed, aborted: &runtime.aborted}, err
	}
	module.PrepareDeleteTx = func(ctx context.Context, tx *sql.Tx, user, _ int64) (Finalizer, error) {
		runtime.lastTx = tx
		_, err := tx.ExecContext(ctx, `DELETE FROM free_records WHERE user_id=?`, user)
		return &testFinalizer{committed: &runtime.committed, aborted: &runtime.aborted}, err
	}
	module.Retain = func(ctx context.Context, now int64, limit int, deadline time.Time) (WorkResult, error) {
		ctx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		result, err := runtime.shared.Database.ExecContext(ctx, `DELETE FROM free_records WHERE id IN (SELECT id FROM free_records WHERE expires_at<=? ORDER BY id LIMIT ?)`, now, limit)
		if err != nil {
			return WorkResult{}, err
		}
		count, err := result.RowsAffected()
		return WorkResult{Processed: int(count)}, err
	}
	return module
}

type capturedRoutes struct {
	user  map[string]resources.AuthorizedUserHandler
	admin map[string]http.Handler
}

func (r *capturedRoutes) RegisterUserRoute(method, path string, h resources.AuthorizedUserHandler) error {
	if r.user == nil {
		r.user = map[string]resources.AuthorizedUserHandler{}
	}
	key := method + " " + path
	if r.user[key] != nil {
		return errors.New("duplicate route")
	}
	r.user[key] = h
	return nil
}
func (r *capturedRoutes) RegisterAdminRoute(method, path string, h http.Handler) error {
	if r.admin == nil {
		r.admin = map[string]http.Handler{}
	}
	r.admin[method+" "+path] = h
	return nil
}
func (r *capturedRoutes) RegisterContinuationUserRoute(string, string, resources.AuthenticatedContinuationHandler) error {
	return errors.New("undeclared continuation")
}
func (r *capturedRoutes) Register(maintenance.ContinuationKind, maintenance.ContinuationRegistration) error {
	return errors.New("undeclared continuation")
}
func (r *capturedRoutes) all() Registrars {
	return Registrars{User: r, Admin: r, Continuation: r, Maintenance: r}
}

func TestFreeModuleUsesAllRegisteredCapabilitiesWithoutLedgerKinds(t *testing.T) {
	fixture := newGameFixture(t, nil)
	database := fixture.database
	if _, err := database.Exec(`CREATE TABLE free_records(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,expires_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"game_free_enabled": "0", "game_free_limit": "1"} {
		if _, err := database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)`, key, value, fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	registry := game.NewRegistry()
	if err := registry.Register(freeDescriptor("free", 0)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	runtime := &freeRuntime{}
	service, err := New(Options{Database: database, Registry: registry, Factories: map[string]Factory{"free": func(shared Services) (*Module, error) { runtime.shared = shared; return runtime.module(), nil }}, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth, Now: func() time.Time { return time.Unix(fixtureNow, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := builtinfinance.ForModule("free"); !errors.Is(err, ledger.ErrInvalidPlan) {
		t.Fatalf("unregistered financial capability: %v", err)
	}
	if err := service.StartWorker(context.Background()); err == nil {
		t.Fatal("worker started before validation/recovery")
	}
	if err := service.ValidatePersistedState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.StartWorker(context.Background()); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("unrecovered worker: %v", err)
	}
	if _, err := service.RecoverModule(context.Background(), "free", fixtureNow, 2, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := service.StartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.validated != 1 || runtime.recovered != 1 || runtime.started != 1 || runtime.shared.Limiter != service.Limiter() {
		t.Fatalf("lifecycle/shared limiter: %+v", runtime)
	}
	before, err := service.ReadGamesConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	patch := []byte(fmt.Sprintf(`{"expected_revision":%q,"free":{"enabled":true,"limit":2}}`, before.Revision))
	if _, err := service.PatchGamesConfig(context.Background(), patch, validTestKey(300)); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("enabled without ready: %v", err)
	}
	unchanged, err := service.ReadGamesConfig(context.Background())
	if err != nil || !equalJSONValue(t, before, unchanged) {
		t.Fatal("not-ready patch changed configuration")
	}
	runtime.ready = true
	updated, err := service.PatchGamesConfig(context.Background(), patch, validTestKey(300))
	if err != nil || updated.Revision == before.Revision {
		t.Fatalf("patch: %+v %v", updated, err)
	}
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(context.Background(), tx, fixture.adminID, fixtureNow); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	id := "free_" + strings.Repeat("A", 22)
	if _, err := database.Exec(`INSERT INTO free_records VALUES(?,?,?)`, id, fixture.adminID, fixtureNow); err != nil {
		t.Fatal(err)
	}
	routes := &capturedRoutes{}
	if err := service.RegisterRoutes(routes.all()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "https://example.test/api/games/free/state", nil)
	response := httptest.NewRecorder()
	routes.user["GET /api/games/free/state"](response, request, resources.UserPrincipal{UserID: fixture.adminID})
	if response.Code != 200 || !strings.Contains(response.Body.String(), id) {
		t.Fatalf("module route: %d %s", response.Code, response.Body.String())
	}
	snapshot, err := service.GamesSnapshot(context.Background(), fixture.adminID, time.Unix(fixtureNow, 0))
	if err != nil || len(snapshot) != 4 || snapshot["free"] == nil {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	home, err := service.HomeSummary(context.Background(), fixture.adminID)
	if err != nil || len(home.PendingResults) != 1 || home.PendingResults[0].ResourceID != id {
		t.Fatalf("home: %+v %v", home, err)
	}
	counts, err := service.ActiveCounts(context.Background())
	if err != nil || len(counts.Games) != 1 || counts.Games[0].Count != "1" {
		t.Fatalf("counts: %+v %v", counts, err)
	}
	tx, err = database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := service.BindTx(context.Background(), tx, "free")
	if err != nil {
		t.Fatal(err)
	}
	exported, finalizer, err := bound.Export(fixture.adminID, fixtureNow, 2)
	if err != nil || len(exported.([]freeRow)) != 1 || runtime.lastTx != tx {
		t.Fatalf("bound export: %+v %v", exported, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Abort() || finalizer.Commit() {
		t.Fatal("export finalizer was not once-only")
	}
	tx, err = database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, err = service.BindTx(context.Background(), tx, "free")
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err = bound.PrepareDelete(fixture.adminID, fixtureNow)
	if err != nil || runtime.lastTx != tx {
		t.Fatal("delete opened another transaction")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	finalizer.Abort()
	if result, err := service.HomeSummary(context.Background(), fixture.adminID); err != nil || len(result.PendingResults) != 1 {
		t.Fatal("delete rollback lost records")
	}
	tx, err = database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, err = service.BindTx(context.Background(), tx, "free")
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err = bound.PrepareDelete(fixture.adminID, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	finalizer.Commit()
	if runtime.committed != 1 || runtime.aborted != 2 {
		t.Fatalf("finalizers %d/%d", runtime.committed, runtime.aborted)
	}
	if _, err := database.Exec(`INSERT INTO free_records VALUES(?,?,?)`, id, fixture.adminID, fixtureNow); err != nil {
		t.Fatal(err)
	}
	result, err := service.RetainModule(context.Background(), "free", fixtureNow, 1, time.Now().Add(time.Second))
	if err != nil || result.Processed != 1 {
		t.Fatalf("retain: %+v %v", result, err)
	}
	var operations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM credit_operations`).Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("free module wrote ledger: %d %v", operations, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil || runtime.closed != 1 {
		t.Fatal("close is not idempotent")
	}
	if _, _, err := service.Limiter().Reserve(fixture.adminID); err == nil {
		t.Fatal("host left shared limiter open")
	}
}

func TestHostConstructionRecoveryFailureAndReverseClose(t *testing.T) {
	fixture := newGameFixture(t, nil)
	registry := game.NewRegistry()
	for i, id := range []string{"one", "two", "three"} {
		if err := registry.Register(freeDescriptor(id, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"construct", "validate", "recover", "start", "none"} {
		t.Run(failure, func(t *testing.T) {
			events := []string{}
			var limiter *game.StartLimiter
			factories := map[string]Factory{}
			for _, id := range []string{"one", "two", "three"} {
				factories[id] = func(shared Services) (*Module, error) {
					if limiter != nil && limiter != shared.Limiter {
						t.Fatal("duplicated shared limiter")
					}
					limiter = shared.Limiter
					events = append(events, "construct "+id)
					m := inertModule()
					m.Close = func() error { events = append(events, "close "+id); return nil }
					if failure == "construct" && id == "two" {
						return m, errors.New("constructor failure")
					}
					m.ValidatePersistedState = func(context.Context) error {
						events = append(events, "validate "+id)
						if failure == "validate" && id == "two" {
							return errors.New("bad persisted state")
						}
						return nil
					}
					m.RecoverBeforeListen = func(context.Context, int64, int, time.Time) (WorkResult, error) {
						events = append(events, "recover "+id)
						if failure == "recover" && id == "two" {
							return WorkResult{}, errors.New("recovery failure")
						}
						return WorkResult{}, nil
					}
					m.StartWorker = func(context.Context) error {
						events = append(events, "start "+id)
						if failure == "start" && id == "two" {
							return errors.New("worker failure")
						}
						return nil
					}
					return m, nil
				}
			}
			service, err := New(Options{Database: fixture.database, Registry: registry, Factories: factories, UserAuthorizer: testUserAuthorizer{}, AdminAuthorizer: fixture.adminAuth})
			if err == nil {
				err = service.RecoverBeforeListen(context.Background())
				if err == nil {
					err = service.StartWorker(context.Background())
				}
				_ = service.Close()
			}
			if (err != nil) != (failure != "none") {
				t.Fatalf("error %v events %v", err, events)
			}
			want := []string{"close three", "close two", "close one"}
			if failure == "construct" {
				want = []string{"close two", "close one"}
			}
			if !reflect.DeepEqual(events[len(events)-len(want):], want) {
				t.Fatalf("reverse close %v", events)
			}
			if failure == "validate" || failure == "recover" {
				for _, event := range events {
					if strings.HasPrefix(event, "start ") {
						t.Fatalf("started before recovery %v", events)
					}
				}
			}
		})
	}
}
