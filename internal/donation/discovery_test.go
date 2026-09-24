package donation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type donationDiscoveryRail struct {
	mu      sync.Mutex
	calls   atomic.Int64
	started chan struct{}
	release chan struct{}
	result  resources.DiscoveryClaimResult
}

func (rail *donationDiscoveryRail) Discover(ctx context.Context, input resources.DiscoveryClaimInput) (resources.DiscoveryClaimResult, error) {
	rail.calls.Add(1)
	if input.Authorize == nil {
		return resources.DiscoveryClaimResult{}, errors.New("missing management guard")
	}
	if rail.started != nil {
		rail.started <- struct{}{}
	}
	if rail.release != nil {
		select {
		case <-rail.release:
		case <-ctx.Done():
			return resources.DiscoveryClaimResult{}, ctx.Err()
		}
	}
	rail.mu.Lock()
	defer rail.mu.Unlock()
	return rail.result, nil
}

func (rail *donationDiscoveryRail) setResult(result resources.DiscoveryClaimResult) {
	rail.mu.Lock()
	defer rail.mu.Unlock()
	rail.result = result
}

type discoveryFixture struct {
	*donationTestEnv
	repository                                                  *resources.Repository
	pool                                                        *resources.DiscoveryWorkerPool
	rail                                                        *donationDiscoveryRail
	owner, admin, steward, endpoint, key, donation, donationKey int64
}

func approveDiscoveryDonation(t *testing.T, env *donationTestEnv, donation Donation, seed byte) {
	t.Helper()
	settings := make([]KeySetting, len(donation.Keys))
	for i, key := range donation.Keys {
		settings[i] = KeySetting{DonationKeyID: parseTestID(t, key.ID), Enabled: true, ExpiresAt: key.ExpiresAt}
	}
	id := parseTestID(t, donation.ID)
	_, err := env.service.ReviewAdmin(context.Background(), donationMutation(t, seed, http.MethodPost, routeAdminReview, []int64{id}, map[string]any{"decision": "approve"}), id,
		ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "discovery fixture", KeySettings: settings})
	if err != nil {
		t.Fatal(err)
	}
}

func newDiscoveryFixture(t *testing.T, blocked bool) *discoveryFixture {
	t.Helper()
	env := newDonationTestEnv(t)
	level := int64(6)
	f := &discoveryFixture{donationTestEnv: env, owner: env.seedUser(t, "discovery-donor", nil, false), admin: env.seedUser(t, "", nil, true), steward: env.seedUser(t, "discovery-steward", &level, false)}
	f.endpoint, f.key = env.seedEndpointKey(t, f.owner, 'q')
	expires := donationTestNow + 3600
	created, err := env.service.Create(context.Background(), f.owner,
		donationMutation(t, 'A', http.MethodPost, routeDonations, nil, map[string]any{"fixture": "discovery"}),
		CreateInput{Description: "discovery donation", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: f.key, ExpiresAt: &expires}}})
	if err != nil {
		t.Fatal(err)
	}
	donation := created.Value
	f.donation, f.donationKey = parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)
	approveDiscoveryDonation(t, env, donation, 'B')
	if _, err := env.store.DB().Exec(`INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision) VALUES(?,'unknown',1)`, f.key); err != nil {
		t.Fatal(err)
	}
	f.rail = &donationDiscoveryRail{result: resources.DiscoveryClaimResult{Succeeded: true, Models: []resources.DiscoveredModel{{UpstreamModelID: "fresh-model", Provider: "fixture"}}}}
	if blocked {
		f.rail.started = make(chan struct{}, 1)
		f.rail.release = make(chan struct{})
	}
	f.pool, err = resources.NewDiscoveryWorkerPool(1, 2, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.repository, err = resources.New(resources.Config{Store: env.store, Connectors: connector.NewDefaultRegistry(), BaseURLs: bulkDeletionBaseURLValidator{}, Secrets: bulkDeletionSecretWriter{}, KeyDeletion: env.service,
		KeyCreation: bulkDeletionLifecycleHook{}, Projection: bulkDeletionLifecycleHook{}, DiscoveryRail: f.rail, DiscoveryWorker: f.pool, ManagedDiscovery: env.service, CursorKeys: env.vault, FinalAuth: env.auth, Now: func() time.Time { return time.Unix(env.clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.pool.Close)
	return f
}

func (f *discoveryFixture) start(t *testing.T, role string, actor int64, seed byte) (resources.MutationResult[resources.DiscoveryAccepted], error) {
	t.Helper()
	route := "/admin/api/donations/{id}/keys/{keyId}/models/refresh"
	if role == "level6" {
		route = "/api/steward/donations/{id}/keys/{keyId}/models/refresh"
	}
	return f.repository.RefreshManagedDiscovery(context.Background(), role, actor, f.donation, f.donationKey, donationMutation(t, seed, http.MethodPost, route, []int64{f.donation, f.donationKey}, nil))
}

func (f *discoveryFixture) wait(t *testing.T, operation string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err := f.store.DB().QueryRow(`SELECT state FROM accepted_operations WHERE id=?`, operation).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("discovery did not complete")
}

func TestManagedDiscoveryPermissionsReplayAndEmptyCatalog(t *testing.T) {
	for _, role := range []string{"admin", "level6"} {
		t.Run(role, func(t *testing.T) {
			f := newDiscoveryFixture(t, false)
			actor := f.admin
			if role == "level6" {
				actor = f.steward
			}
			if _, err := f.start(t, role, f.owner, 'C'); !errors.Is(err, resources.ErrForbidden) {
				t.Fatalf("ordinary actor = %v", err)
			}
			tx, err := f.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.repository.EnsureManualEntryInTransaction(context.Background(), tx, f.owner, f.endpoint, f.key, "manual-model"); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			accepted, err := f.start(t, role, actor, 'D')
			if err != nil || accepted.Status != 202 {
				t.Fatalf("accept: %+v %v", accepted, err)
			}
			f.wait(t, accepted.Value.OperationID)
			replay, err := f.start(t, role, actor, 'D')
			if err != nil || !replay.Replayed || replay.Value.OperationID != accepted.Value.OperationID || f.rail.calls.Load() != 1 {
				t.Fatalf("replay %+v %v calls %d", replay, err, f.rail.calls.Load())
			}
			var savedActor int64
			var savedRole string
			if err := f.store.DB().QueryRow(`SELECT actor_user_id,actor_role FROM accepted_operations WHERE id=?`, accepted.Value.OperationID).Scan(&savedActor, &savedRole); err != nil {
				t.Fatal(err)
			}
			if savedActor != actor || savedRole != role {
				t.Fatalf("wrong audit actor %d %s", savedActor, savedRole)
			}
			catalog, err := f.repository.GetCatalog(context.Background(), f.owner, f.endpoint, f.key, 100, "")
			if err != nil || len(catalog.AutomaticEntries) != 1 || len(catalog.ManualEntries) != 1 {
				t.Fatalf("catalog %+v %v", catalog, err)
			}
			f.rail.setResult(resources.DiscoveryClaimResult{FailureClass: resources.DiscoveryFailureAuth})
			failed, err := f.start(t, role, actor, 'E')
			if err != nil {
				t.Fatal(err)
			}
			f.wait(t, failed.Value.OperationID)
			catalog, err = f.repository.GetCatalog(context.Background(), f.owner, f.endpoint, f.key, 100, "")
			if err != nil || catalog.Evidence.State != "failed" || len(catalog.AutomaticEntries) != 1 || len(catalog.ManualEntries) != 1 {
				t.Fatalf("failed catalog %+v %v", catalog, err)
			}
			f.rail.setResult(resources.DiscoveryClaimResult{Succeeded: true, Models: []resources.DiscoveredModel{}})
			empty, err := f.start(t, role, actor, 'F')
			if err != nil {
				t.Fatal(err)
			}
			f.wait(t, empty.Value.OperationID)
			catalog, err = f.repository.GetCatalog(context.Background(), f.owner, f.endpoint, f.key, 100, "")
			if err != nil || catalog.Evidence.State != "succeeded" || catalog.Evidence.Count == nil || *catalog.Evidence.Count != "0" || len(catalog.AutomaticEntries) != 0 || len(catalog.ManualEntries) != 1 {
				t.Fatalf("empty catalog %+v %v", catalog, err)
			}
			if role == "admin" {
				f.auth.denyAdmin.Store(true)
			} else {
				f.auth.denySteward.Store(true)
			}
			if _, err := f.start(t, role, actor, 'D'); !errors.Is(err, resources.ErrForbidden) {
				t.Fatalf("revoked replay = %v", err)
			}
		})
	}
}

func TestManagedDiscoveryRejectsIneligibleKeysAndWrongParent(t *testing.T) {
	cases := map[string]string{
		"endpoint disabled":     `UPDATE endpoints SET enabled=0`,
		"physical key disabled": `UPDATE endpoint_keys SET enabled=0`,
		"donation key disabled": `UPDATE donation_keys SET enabled=0`,
		"failure disabled":      `UPDATE donation_keys SET failure_disabled=1`,
		"expired":               "",
		"membership removed":    `DELETE FROM donation_key_memberships`,
		"quota exhausted":       `UPDATE donation_keys SET call_limit_mag=nbi_u128(0)`,
		"donor banned":          `UPDATE users SET is_banned=1 WHERE is_admin=0 AND level IS NULL`,
		"pending donation":      `UPDATE donations SET status='pending'`,
		"terminated key":        `UPDATE donation_keys SET ended_reason='terminated',ended_at=updated_at,report_match_until=updated_at+7776000,enabled=0`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDiscoveryFixture(t, false)
			if query == "" {
				f.clock.Add(3601)
			} else if _, err := f.store.DB().Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := f.start(t, "admin", f.admin, 'G'); !errors.Is(err, resources.ErrResourceLocked) {
				t.Fatalf("ineligible start %v", err)
			}
			page, err := f.service.selectDiscoveries(context.Background(), reviewerAdmin, 0, 0, "")
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("ineligible selection %+v %v", page, err)
			}
			if f.rail.calls.Load() != 0 {
				t.Fatal("ineligible key dispatched")
			}
		})
	}
	f := newDiscoveryFixture(t, false)
	if _, err := f.repository.ManagedDiscoveryEvidence(context.Background(), "admin", f.admin, f.donation+1, f.donationKey); !errors.Is(err, resources.ErrNotFound) {
		t.Fatalf("wrong parent %v", err)
	}
}

func TestManagedDiscoveryConcurrentAcceptanceAndRevokedCompletion(t *testing.T) {
	f := newDiscoveryFixture(t, true)
	var once sync.Once
	release := func() { once.Do(func() { close(f.rail.release) }) }
	defer release()
	var group sync.WaitGroup
	results := make(chan resources.MutationResult[resources.DiscoveryAccepted], 8)
	for range 8 {
		group.Go(func() {
			value, err := f.start(t, "level6", f.steward, 'H')
			if err != nil {
				t.Errorf("concurrent: %v", err)
			}
			results <- value
		})
	}
	group.Wait()
	close(results)
	var operation string
	for value := range results {
		if operation != "" && operation != value.Value.OperationID {
			t.Fatal("duplicate accepted operation")
		}
		operation = value.Value.OperationID
	}
	select {
	case <-f.rail.started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery not started")
	}
	f.auth.denySteward.Store(true)
	release()
	f.wait(t, operation)
	if f.rail.calls.Load() != 1 {
		t.Fatalf("calls = %d", f.rail.calls.Load())
	}
	catalog, err := f.repository.GetCatalog(context.Background(), f.owner, f.endpoint, f.key, 100, "")
	if err != nil || catalog.Evidence.State != "failed" || catalog.Evidence.SafeClass != "interrupted" || len(catalog.AutomaticEntries) != 0 {
		t.Fatalf("late result published %+v %v", catalog, err)
	}
}

func TestDiscoverySelectionScansAllPagesWithBoundScope(t *testing.T) {
	env := newDonationTestEnv(t)
	owner := env.seedUser(t, "selection-donor", nil, false)
	env.seedUser(t, "", nil, true)
	level := int64(6)
	steward := env.seedUser(t, "selection-steward", &level, false)
	if _, err := env.store.DB().Exec(`UPDATE site_config SET value='110' WHERE key='default_endpoint_key_limit'`); err != nil {
		t.Fatal(err)
	}
	_, keys := seedBulkDeletionEndpoint(t, env, owner, 105)
	for i := range 4 {
		created := env.createDonationWithSeed(t, owner, byte('M'+i), keys[i*25:(i+1)*25]...)
		approveDiscoveryDonation(t, env, created, byte('Q'+i))
	}
	second := env.createDonationWithSeed(t, owner, 'K', keys[100:]...)
	approveDiscoveryDonation(t, env, second, 'L')
	if _, err := env.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE donation_id<>?`, second.ID); err != nil {
		t.Fatal(err)
	}
	page, err := env.service.selectDiscoveries(context.Background(), reviewerSteward, steward, 0, "")
	if err != nil || len(page.Items) != 0 || page.NextCursor == nil {
		t.Fatalf("sparse page %+v %v", page, err)
	}
	next, err := env.service.selectDiscoveries(context.Background(), reviewerSteward, steward, 0, *page.NextCursor)
	if err != nil || len(next.Items) != 5 || next.NextCursor != nil {
		t.Fatalf("last page %+v %v", next, err)
	}
	for _, item := range next.Items {
		if item.DonationID != second.ID {
			t.Fatalf("unexpected donation %+v", item)
		}
	}
	for _, call := range []func() error{
		func() error {
			_, err := env.service.selectDiscoveries(context.Background(), reviewerAdmin, 0, 0, *page.NextCursor)
			return err
		},
		func() error {
			_, err := env.service.selectDiscoveries(context.Background(), reviewerSteward, steward, parseTestID(t, second.ID), *page.NextCursor)
			return err
		},
		func() error {
			_, err := env.service.selectDiscoveries(context.Background(), reviewerSteward, steward, 0, *page.NextCursor+"x")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("cursor rebinding %v", err)
		}
	}
	env.clock.Add(3601)
	if _, err := env.service.selectDiscoveries(context.Background(), reviewerSteward, steward, 0, *page.NextCursor); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expired cursor %v", err)
	}
}

func TestDiscoverySelectionHTTPStrictBodyAndAuthorization(t *testing.T) {
	f := newDiscoveryFixture(t, false)
	api := &httpAPI{service: f.service}
	for _, body := range []string{`{}`, `{"donation_id":null}`, `{"donation_id":0,"cursor":null}`, `{"donation_id":"01","cursor":null}`, `{"donation_id":null,"cursor":""}`, `{"donation_id":null,"cursor":null,"q":"x"}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, routeAdminDiscoverySelection, strings.NewReader(body))
		api.discoverySelectionAdmin(w, r)
		if w.Code != 400 {
			t.Fatalf("body %s: %d %s", body, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, routeAdminDiscoverySelection, strings.NewReader(`{"donation_id":null,"cursor":null}`))
	api.discoverySelectionAdmin(w, r)
	var result DiscoverySelection
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 {
		t.Fatalf("selection %d %s", w.Code, w.Body)
	}
	f.auth.denyAdmin.Store(true)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, routeAdminDiscoverySelection, strings.NewReader(`{"donation_id":null,"cursor":null}`))
	api.discoverySelectionAdmin(w, r)
	if w.Code != 403 {
		t.Fatalf("revoked selection %d %s", w.Code, w.Body)
	}
}

var _ resources.ManagedDiscoveryAuthorizer = (*Service)(nil)

func TestManagedDiscoveryQueuedAndLateEligibilityChanges(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{true: "queued", false: "in flight"}[queued], func(t *testing.T) {
			f := newDiscoveryFixture(t, true)
			var once sync.Once
			release := func() { once.Do(func() { close(f.rail.release) }) }
			defer release()
			if queued {
				// Occupy the one shared worker without publishing a catalog result.
				reservation, ok := f.pool.ReserveDiscovery()
				if !ok {
					t.Fatal("worker reservation rejected")
				}
				reservation.Start(func(context.Context) { f.rail.started <- struct{}{}; <-f.rail.release })
				<-f.rail.started
			}
			accepted, err := f.start(t, "admin", f.admin, 'Z')
			if err != nil {
				t.Fatal(err)
			}
			if !queued {
				<-f.rail.started
			}
			if _, err := f.store.DB().Exec(`DELETE FROM donation_key_memberships`); err != nil {
				t.Fatal(err)
			}
			release()
			f.wait(t, accepted.Value.OperationID)
			if queued && f.rail.calls.Load() != 0 {
				t.Fatal("queued withdrawn key dispatched")
			}
			catalog, err := f.repository.GetCatalog(context.Background(), f.owner, f.endpoint, f.key, 100, "")
			if err != nil || catalog.Evidence.State != "failed" || catalog.Evidence.SafeClass != "interrupted" || len(catalog.AutomaticEntries) != 0 {
				t.Fatalf("withdrawn result published %+v %v", catalog, err)
			}
		})
	}
}
