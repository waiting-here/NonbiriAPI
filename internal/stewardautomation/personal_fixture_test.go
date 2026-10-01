package stewardautomation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type automationAuthority struct{ auth *authz.Authorizer }

func (a automationAuthority) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, ok := authz.PersonalCallerFromContext(ctx); ok {
		_, err := a.auth.AuthorizePersonalCaller(ctx, tx, id)
		return err
	}
	_, err := a.auth.AuthorizeStewardCaller(ctx, tx, id)
	return err
}
func (a automationAuthority) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := a.auth.AuthorizeStewardCaller(ctx, tx, id)
	return err
}
func (automationAuthority) AuthorizeAdminMutation(context.Context, *sql.Tx, int64) error {
	return authz.ErrUnauthorized
}
func (automationAuthority) AuthorizeAdminFinalTx(context.Context, *sql.Tx, int64) error {
	return authz.ErrUnauthorized
}

// Unrelated issue and report projections are not part of this fixture. The
// mutation hook can fail to prove the business savepoint rolls back completely.
type automationHooks struct{ fail atomic.Bool }

func (*automationHooks) ProtectNewEndpointKey(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}
func (*automationHooks) ReconcileModelDiscovery(context.Context, *sql.Tx, int64, int64) error {
	return nil
}
func (h *automationHooks) ReconcileRoutingProjection(context.Context, *sql.Tx, int64, int64) error {
	if h.fail.Load() {
		return resources.ErrConflict
	}
	return nil
}
func (*automationHooks) PrepareEndpointDeletion(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}
func (*automationHooks) PrepareEndpointKeyDeletion(context.Context, *sql.Tx, int64, []int64, int64) error {
	return nil
}
func (*automationHooks) PrepareModelDeletion(context.Context, *sql.Tx, int64, int64, int64) error {
	return nil
}

type personalFixture struct {
	store                       *db.Store
	service                     *Service
	repo                        *resources.Repository
	bridge                      *resourcebridge.Runtime
	worker                      *resources.DiscoveryWorkerPool
	clock                       atomic.Int64
	calls                       atomic.Int64
	mode                        atomic.Int32
	hooks                       automationHooks
	userID, endpointID, modelID int64
	ctx                         context.Context
	started                     chan struct{}
	release                     chan struct{}
}

func newPersonalFixture(t *testing.T) *personalFixture {
	t.Helper()
	f := &personalFixture{started: make(chan struct{}, 20), release: make(chan struct{})}
	f.clock.Store(time.Now().Unix())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		select {
		case f.started <- struct{}{}:
		default:
		}
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(400)
			return
		}
		switch f.mode.Load() {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case 2:
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"private diagnostic"}}`))
		case 3:
			select {
			case <-r.Context().Done():
				return
			case <-f.release:
			}
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model","object":"model"}]}`))
		case 4:
			f.exec(t, `UPDATE caller_keys SET generation=generation+1,key_hash=zeroblob(32),display_head='newh',display_tail='newt' WHERE user_id=?`, f.userID)
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model","object":"model"}]}`))
		case 5:
			f.exec(t, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, f.userID)
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model","object":"model"}]}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model","object":"model"}]}`))
		}
	}))
	t.Cleanup(upstream.Close)
	vault, err := secret.New(bytes.Repeat([]byte{0x44}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "automation.sqlite")
	dbfixture.Materialize(t, path)
	f.store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	now := func() time.Time { return time.Unix(f.clock.Load(), 0) }
	claims, err := claim.New(claim.Dependencies{DB: f.store.DB(), Secrets: vault, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{upstream.URL}, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{ListenAddr: "127.0.0.1:8080", SiteBaseURL: "http://127.0.0.1:8081", UserHost: "127.0.0.1:8081", AdminHost: "127.0.0.1:8082"}); err != nil {
		t.Fatal(err)
	}
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.Initialize(context.Background(), f.store.DB()); err != nil {
		t.Fatal(err)
	}
	f.bridge, err = resourcebridge.New(resourcebridge.Config{Store: f.store, Vault: vault, Claims: claims, Backend: local, Review: review, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.bridge.Close() })
	f.worker, err = resources.NewDiscoveryWorkerPool(4, 32, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.worker.Close)
	authority := automationAuthority{auth: authz.New(authz.Options{Now: now})}
	f.repo, err = resources.New(resources.Config{Store: f.store, Connectors: connector.NewDefaultRegistry(), BaseURLs: stack, Secrets: f.bridge, KeyDeletion: &f.hooks, KeyCreation: &f.hooks, Projection: &f.hooks, DiscoveryRail: f.bridge, DiscoveryWorker: f.worker, CursorKeys: vault, FinalAuth: authority, AdminFinalAuth: authority, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	donations, err := donation.New(donation.Config{Store: f.store, Review: review, OwnerAuth: authority, RoleAuth: authority, CursorKeys: vault, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	charity, err := charityrouting.New(charityrouting.Config{Store: f.store, RoleAuth: authority, DonationState: donations, CursorKeys: vault, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = New(Config{Database: f.store.DB(), Authorizer: authority.auth, Resources: f.repo, Donations: donations, Charity: charity, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.service.Close() })
	f.userID = f.user(t, "owner", 1)
	f.ctx = authz.WithPersonalCaller(context.Background(), authz.PersonalCaller{UserID: f.userID, Generation: 1})
	f.endpointID = f.exec(t, `INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible',?,'owner endpoint',1,1,?,?)`, f.userID, upstream.URL+"/v1", f.clock.Load(), f.clock.Load())
	f.modelID = f.exec(t, `INSERT INTO models(user_id,provider,model,full_name,revision,binding_revision,created_at,updated_at) VALUES(?,'fixture','model','fixture/model',1,1,?,?)`, f.userID, f.clock.Load(), f.clock.Load())
	return f
}
func (f *personalFixture) exec(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	r, err := f.store.DB().Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *personalFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.store.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (f *personalFixture) user(t *testing.T, label string, level int) int64 {
	t.Helper()
	zero := make([]byte, 16)
	id := f.exec(t, `INSERT INTO users(discord_id,username,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, label, label, level, zero, zero, zero, zero, zero, zero, zero, zero, f.clock.Load(), f.clock.Load())
	hash := sha256.Sum256([]byte(label))
	f.exec(t, `INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,'head','tail',?,?)`, id, hash[:], f.clock.Load(), f.clock.Load())
	return id
}
func (f *personalFixture) request(ctx context.Context, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if method == http.MethodPost || method == http.MethodPatch {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.service.ServeHTTP(w, r)
	return w
}
func (f *personalFixture) importBody(secret string) string {
	body, _ := json.Marshal(map[string]any{"ownership_confirmed": true, "keys": []map[string]any{{"secret": secret}}})
	return string(body)
}
func (f *personalFixture) importPath() string {
	return PersonalPrefix + "endpoints/" + strconv.FormatInt(f.endpointID, 10) + "/keys/batch-import"
}
func (f *personalFixture) bindingPath() string {
	return PersonalPrefix + "models/" + strconv.FormatInt(f.modelID, 10) + "/bindings/batch"
}
func decodeBatch(t *testing.T, w *httptest.ResponseRecorder, want int) batchResult {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
	var out batchResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, item := range out.Results {
		if item.Status == "success" && (item.Code != "" || item.Message != "") {
			t.Fatalf("success retained a failure projection: %+v", item)
		}
	}
	return out
}
func (f *personalFixture) key(t *testing.T, label string) string {
	out := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat(label, 22), f.importBody("fixture-upstream-"+label)), 200)
	return out.Results[0].EndpointKeyID
}
