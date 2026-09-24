package imageactivity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testNow int64 = 1800000000

type actorContext struct{}
type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type authority struct{ auth *authz.Authorizer }

func (a authority) check(ctx context.Context, tx *sql.Tx, user int64, role authz.Role) error {
	actor, ok := ctx.Value(actorContext{}).(authz.Actor)
	if !ok || actor.UserID != user {
		return authz.ErrUnauthorized
	}
	_, err := a.auth.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	return err
}
func (a authority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	return a.check(ctx, tx, user, authz.RoleUser)
}
func (a authority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, user int64) error {
	return a.check(ctx, tx, user, authz.RoleAdministrator)
}

type admission struct{}

func (admission) AuthorizeUserActivity(ctx context.Context, tx *sql.Tx, user int64) error {
	var on bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM maintenance_state WHERE id=1").Scan(&on); err != nil {
		return err
	}
	if on {
		return maintenance.ErrMaintenanceOn
	}
	return nil
}

type sourceProxy struct {
	*observability.Repository
	fail atomic.Bool
}

func (s *sourceProxy) RecordImageSourceTx(ctx context.Context, tx *sql.Tx, id string, user, now int64) error {
	if s.fail.Load() {
		return ErrInvariant
	}
	return s.Repository.RecordImageSourceTx(ctx, tx, id, user, now)
}
func (s *sourceProxy) DeleteImageTaskDataTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM request_error_bodies WHERE task_id=?", id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM image_task_sources WHERE task_id=?", id)
	return err
}

type fakeUpstream struct {
	server               *httptest.Server
	mode                 atomic.Int32
	posts, polls, models atomic.Int64
	mu                   sync.Mutex
	submitted            []map[string]any
	started              chan struct{}
	release              chan struct{}
	png                  []byte
	releaseOnce          sync.Once
}

func (f *fakeUpstream) unblock() { f.releaseOnce.Do(func() { close(f.release) }) }
func newUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	f := &fakeUpstream{started: make(chan struct{}, 100), release: make(chan struct{}), png: buf.Bytes()}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			f.models.Add(1)
			if f.mode.Load() == 7 {
				fmt.Fprint(w, `{"error":"synthetic malformed catalog"}`)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":"studio-test","meta":{"quality":["draft","high"]}}]}`)
		case "/generate":
			f.posts.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.submitted = append(f.submitted, body)
			f.mu.Unlock()
			select {
			case f.started <- struct{}{}:
			default:
			}
			switch f.mode.Load() {
			case 1:
				fmt.Fprint(w, `{"id":"job/../opaque","status":"pending"}`)
				return
			case 2:
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			case 3:
				w.WriteHeader(429)
				fmt.Fprint(w, `{"error":{"message":"synthetic rejection"}}`)
				return
			case 8:
				fmt.Fprint(w, `{"queued":true,"id":"receipt/../opaque"}`)
				return
			case 9:
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64": base64.StdEncoding.EncodeToString(f.png)}}})
				return
			case 10:
				_ = json.NewEncoder(w).Encode(map[string]any{"queued": true, "id": "contradictory", "data": []map[string]string{{"b64": base64.StdEncoding.EncodeToString(f.png)}}})
				return
			case 6:
				fmt.Fprint(w, `{"status":"done","data":[{"b64":"`+base64.StdEncoding.EncodeToString(f.png)+`"}]`)
				return
			case 4:
				select {
				case <-f.release:
				case <-r.Context().Done():
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "done", "data": []map[string]string{{"b64": base64.StdEncoding.EncodeToString(f.png)}}})
		default:
			if r.Method == http.MethodGet {
				f.polls.Add(1)
				if f.mode.Load() == 11 {
					fmt.Fprint(w, `{"status":"unrecognized"}`)
					return
				}
				if f.mode.Load() == 5 {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":"temporary"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "done", "data": []map[string]string{{"b64": base64.StdEncoding.EncodeToString(f.png)}}})
				return
			}
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(func() { f.unblock(); f.server.Close() })
	return f
}

type fixture struct {
	database                             *sql.DB
	service                              *Service
	limited                              *limitedactivities.Service
	vault                                *secret.Vault
	source                               *sourceProxy
	upstream                             *fakeUpstream
	now                                  atomic.Int64
	actors                               map[int64]authz.Actor
	admin, user, other, steward, trainee int64
	model                                string
	settings                             UpstreamInput
	sequence                             int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "image.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fixture{database: store.DB(), vault: vault, upstream: newUpstream(t), actors: map[int64]authz.Actor{}}
	f.now.Store(testNow)
	if _, err = f.database.Exec("UPDATE maintenance_state SET enabled=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("UPDATE site_config SET value='0' WHERE key='maintenance_mode'"); err != nil {
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{f.upstream.server.URL}, Resolver: publicResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://site.example.invalid", UserHost: "site.example.invalid", AdminHost: "admin.example.invalid", ListenAddr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	repo, err := observability.NewRepository(f.database)
	if err != nil {
		t.Fatal(err)
	}
	f.source = &sourceProxy{Repository: repo}
	now := func() time.Time { return time.Unix(f.now.Load(), 0) }
	auth := authority{authz.New(authz.Options{Now: now})}
	f.service, err = New(Config{Database: f.database, Users: auth, Admins: auth, Gate: admission{}, Vault: vault, Egress: stack, Sources: f.source, Diagnostics: repo, Now: now, Admission: func(ctx context.Context, tx *sql.Tx, user int64, key string, at int64) (limitedactivities.Detail, error) {
		return f.limited.CheckAdmissionTx(ctx, tx, user, key, at)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.service.Close() })
	f.limited, err = limitedactivities.New(limitedactivities.Config{Database: f.database, Users: auth, Admins: auth, Gate: admission{}, Keys: vault, Registry: limitedactivities.NewRegistry(f.service), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(name string, admin, level int) int64 {
		zero := make([]byte, 16)
		result, e := f.database.Exec("INSERT INTO users(discord_id,username,is_admin,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", "image-"+name, name, admin, level, zero, zero, zero, zero, zero, zero, zero, zero, testNow-100, testNow-100)
		if e != nil {
			t.Fatal(e)
		}
		id, e := result.LastInsertId()
		if e != nil {
			t.Fatal(e)
		}
		token := "image-session-" + name
		_, e = f.database.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)", token, id, testNow, testNow+86400, testNow+86400, testNow, "g1")
		if e != nil {
			t.Fatal(e)
		}
		kind := authz.ActorUserSession
		if admin == 1 {
			kind = authz.ActorAdminSession
		}
		f.actors[id] = authz.Actor{Kind: kind, UserID: id, SessionTokenHash: token, SessionGeneration: "g1"}
		f.tx(t, func(tx *sql.Tx) {
			for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
				if _, e := ledger.CreateUserAssetAccount(context.Background(), tx, id, asset, testNow); e != nil {
					t.Fatal(e)
				}
			}
		})
		return id
	}
	f.admin = seed("admin", 1, 1)
	f.user = seed("user", 0, 1)
	f.other = seed("other", 0, 1)
	f.steward = seed("steward", 0, 6)
	f.trainee = seed("trainee", 0, 5)
	return f
}
func (f *fixture) ctx(user int64) context.Context {
	return observability.WithSource(context.WithValue(context.Background(), actorContext{}, f.actors[user]), observability.Source{EffectiveIP: "198.51.100.7", IPQuality: "direct_peer", UserAgent: "SyntheticTest/1"})
}
func (f *fixture) key() string { f.sequence++; return fmt.Sprintf("image-operation-%08d", f.sequence) }
func (f *fixture) tx(t *testing.T, fn func(*sql.Tx)) {
	t.Helper()
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) configure(t *testing.T) {
	t.Helper()
	taskID, state, encoded, urlPointer, metadata := "/id", "/status", "/b64", "/url", "/meta"
	secret := "fixture-secret"
	minimum, maximum := float64(1), float64(16)
	length := 65536
	f.settings = UpstreamInput{ExpectedRevision: "1", BaseURL: f.upstream.server.URL, Secret: SecretInput{Mode: "replace", Value: &secret}, RPM: 100, Concurrency: 2, PerUserLimit: 10, GlobalLimit: 100, QueueTimeoutSeconds: 60, ExecutionTimeoutSeconds: 60, MemoryBudgetMiB: 512, ImageOrigins: []string{},
		Adapter: Adapter{Discovery: DiscoveryAdapter{Method: "GET", Path: "/models", ItemsPointer: "/data", IDPointer: "/id", MetadataPointer: &metadata}, Submit: SubmitAdapter{Method: "POST", Path: "/generate", Mapping: Mapping{ModelPointer: "/model", Parameters: map[ParameterKey]string{Prompt: "/prompt", N: "/n"}, Constants: []Constant{}}}, Poll: &PollAdapter{Method: "GET", Path: "/jobs/{task_id}"}, Response: ResponseAdapter{TaskIDPointer: &taskID, StatePointer: &state, WorkingStates: []string{"pending"}, SuccessStates: []string{"done"}, FailureStates: []string{"failed"}, ImagesPointer: "/data", Base64Pointer: &encoded, URLPointer: &urlPointer}}}
	if _, err := f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), f.settings); err != nil {
		t.Fatal(err)
	}
	accepted, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		r, e := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
		return e == nil && (r.State == "succeeded" || r.State == "failed")
	})
	result, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, accepted.Value.Operation.ID)
	if err != nil || result.State != "succeeded" {
		t.Fatalf("discovery %+v %v", result, err)
	}
	catalog, err := f.service.ListModels(f.ctx(f.admin), f.admin, true, 100, "")
	if err != nil || len(catalog.Data) != 1 {
		t.Fatalf("catalog %v %v", catalog, err)
	}
	f.model = catalog.Data[0].ID
	input := ModelInput{ExpectedRevision: "0", DisplayName: "Studio", Description: "Synthetic images", Enabled: true, Price: Price{"2", "1"}, Parameters: []ParameterRule{{Key: Prompt, Supported: true, Required: true, Type: "string", MaxLength: &length, LengthUnit: "utf8_bytes"}, {Key: N, Supported: true, Type: "integer", Minimum: &minimum, Maximum: &maximum}}, Combinations: []CombinationRule{}, Mapping: Mapping{Parameters: map[ParameterKey]string{}, Constants: []Constant{}}}
	if _, err = f.service.PutModel(f.ctx(f.admin), f.admin, f.model, f.key(), input); err != nil {
		t.Fatal(err)
	}
	start, end := testNow-1, testNow+86400
	common, err := f.limited.AdminConfig(f.ctx(f.admin), f.admin, limitedactivities.PictureBook)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(limitedactivities.ExchangeSettings{PaperPrice: "1", BrushPrice: "1", BrushCap: "1000"})
	if _, err = f.limited.UpdateConfig(f.ctx(f.admin), f.admin, limitedactivities.PictureBook, f.key(), limitedactivities.ConfigInput{ExpectedRevision: common.Revision, Visible: true, StartsAt: &start, EndsAt: &end, Paused: false, ModuleConfig: settings}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []int64{f.user, f.other, f.steward, f.trainee} {
		f.tx(t, func(tx *sql.Tx) {
			wallet, e := ledger.UserAccount(context.Background(), tx, user)
			if e != nil {
				t.Fatal(e)
			}
			external, e := ledger.CodedAssetAccount(context.Background(), tx, "external", ledger.General)
			if e != nil {
				t.Fatal(e)
			}
			op, e := db.GenerateOpaqueID("op_")
			if e != nil {
				t.Fatal(e)
			}
			plan, e := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: op, ActorUserID: f.admin, CreatedAt: testNow}, wallet.ID, external.ID, ledger.AmountFromMilli(1000000), 0, ledger.Amount{}, "fixture funding")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ledger.Apply(context.Background(), tx, plan); e != nil {
				t.Fatal(e)
			}
		})
		for _, asset := range []ledger.Asset{ledger.SketchPaper, ledger.SketchBrush} {
			if _, err = f.limited.Exchange(f.ctx(user), user, f.key(), limitedactivities.ExchangeInput{Asset: asset, Quantity: "100"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func (f *fixture) wait(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		if err := f.service.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out awaiting image state")
}
func (f *fixture) submit(t *testing.T, user int64, n int) Task {
	t.Helper()
	result, err := f.service.Submit(f.ctx(user), user, f.key(), SubmitInput{ModelID: f.model, ExpectedModelRevision: "1", Prompt: "Private synthetic prompt", N: &n})
	if err != nil {
		t.Fatal(err)
	}
	return result.Value.Task
}
func (f *fixture) checkLedger(t *testing.T) {
	f.tx(t, func(tx *sql.Tx) {
		if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
	})
}
