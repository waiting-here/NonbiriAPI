package stewardautomation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestPersonalRestartContinuesOnlyUnfinishedItems(t *testing.T) {
	f := newPersonalFixture(t)
	input := importInput{OwnershipConfirmed: true, Keys: []importKey{{Secret: stringPointer("restart-first")}, {Secret: stringPointer("restart-second")}}}
	canonical, err := canonicalImport(input)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.service.registerBatch(f.ctx, f.userID, f.endpointID, "key_import", strings.Repeat("i", 22), canonical, 2)
	clear(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.runItem(f.ctx, batch, 0, "", nil, func(ctx context.Context, tx *sql.Tx) (resources.AutomationMutation, error) {
		return f.repo.ImportEndpointKeyInTransaction(ctx, tx, f.userID, f.endpointID, resources.CreateEndpointKeyInput{Secret: []byte("restart-first"), Enabled: true, OwnershipConfirmed: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = idempotency.NewMaintenance(f.store.DB()).Recover(context.Background(), f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.RecoverBeforeListener(context.Background(), f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	old := f.service
	f.service, err = New(Config{Database: f.store.DB(), Authorizer: old.auth, Resources: f.repo, Donations: old.donations, Charity: old.charity, Now: old.now})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := canonicalImport(input)
	out := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("i", 22), string(body)), 200)
	clear(body)
	if out.Results[0].Outcome != "created" || out.Results[1].Outcome != "created" || f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 2 {
		t.Fatal("restart duplicated confirmed item")
	}
	if f.count(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='personal_automation' AND state='accepted'`) != 0 {
		t.Fatal("cross-transaction accepted row persisted")
	}
}
func stringPointer(value string) *string { return &value }
func TestPersonalConcurrentImportPreservesLegacyDuplicates(t *testing.T) {
	f := newPersonalFixture(t)
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			tx, err := f.store.DB().BeginTx(f.ctx, nil)
			if err == nil {
				defer tx.Rollback()
				_, err = f.repo.CreateEndpointKeyInTransaction(f.ctx, tx, f.userID, f.endpointID, resources.CreateEndpointKeyInput{Secret: []byte("legacy-body"), Enabled: true, OwnershipConfirmed: true})
				if err == nil {
					err = tx.Commit()
				}
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 2 {
		t.Fatal("browser semantics became globally deduplicated")
	}
	minimum := fmt.Sprint(f.count(t, `SELECT MIN(id) FROM endpoint_keys`))
	out := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("l", 22), f.importBody("legacy-body")), 200)
	if out.Results[0].Outcome != "existing" || out.Results[0].EndpointKeyID != minimum || f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 2 {
		t.Fatal("legacy duplicates not preserved/minimum not returned")
	}
}
func TestPersonalSharedAdmissionCancelCloseAndStableDiscovery(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	f.mode.Store(3)
	body := fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, key)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.request(ctx, "POST", f.bindingPath(), strings.Repeat("z", 22), body) }()
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("real discovery did not start")
	}
	if w := f.request(f.ctx, "GET", PersonalPrefix+"endpoints", "", ""); w.Code != 429 {
		t.Fatalf("same user admission %d", w.Code)
	}
	steward := authz.WithStewardCaller(context.Background(), authz.StewardCaller{UserID: f.userID, Generation: 1})
	if w := f.request(steward, "GET", FailurePolicyPath, "", ""); w.Code != 429 {
		t.Fatalf("old charity API did not share admission: %d", w.Code)
	}
	for user := int64(100); user < 103; user++ {
		if !f.service.admit(user) {
			t.Fatal("global slot unavailable")
		}
		defer f.service.release(user)
	}
	other := f.user(t, "other", 1)
	otherCtx := authz.WithPersonalCaller(context.Background(), authz.PersonalCaller{UserID: other, Generation: 1})
	if w := f.request(otherCtx, "GET", PersonalPrefix+"endpoints", "", ""); w.Code != 429 {
		t.Fatalf("global admission %d", w.Code)
	}
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel work")
	}
	f.worker.Close()
	if f.count(t, `SELECT COUNT(*) FROM model_bindings`) != 0 {
		t.Fatal("canceled refresh bound a model")
	}
	old := f.service
	var err error
	f.service, err = New(Config{Database: f.store.DB(), Authorizer: old.auth, Resources: f.repo, Donations: old.donations, Charity: old.charity, Now: old.now})
	if err != nil {
		t.Fatal(err)
	}
	f.mode.Store(0)
	out := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("z", 22), body), 422)
	if out.Results[0].Status != "failed" || f.calls.Load() != 1 {
		t.Fatal("resume changed discovery identity or dispatched again")
	}
}

func TestPersonalImportDeduplicatesBodyAndPreservesExistingMetadata(t *testing.T) {
	f := newPersonalFixture(t)
	body := `{"ownership_confirmed":true,"keys":[{"secret":"same-body","note":"original","enabled":false,"max_rpm":9},{"secret":"same-body","note":"changed","enabled":true},{"secret":"second-body"}]}`
	out := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("a", 22), body), 200)
	if out.Results[0].Outcome != "created" || out.Results[1].Outcome != "existing" || out.Results[0].EndpointKeyID != out.Results[1].EndpointKeyID || out.Results[2].Outcome != "created" {
		t.Fatalf("outcomes: %+v", out.Results)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM endpoint_keys WHERE endpoint_id=?`, f.endpointID); got != 2 {
		t.Fatalf("keys=%d", got)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM endpoint_keys k JOIN endpoint_key_limits l ON l.endpoint_key_id=k.id WHERE k.id=? AND k.note='original' AND k.enabled=0 AND l.max_rpm=9`, out.Results[0].EndpointKeyID); got != 1 {
		t.Fatal("existing key was overwritten")
	}
	f.exec(t, `UPDATE site_config SET value='2' WHERE key='default_endpoint_key_limit'`)
	again := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("b", 22), `{"ownership_confirmed":true,"keys":[{"secret":"same-body"},{"secret":"full-capacity"}]}`), 200)
	if again.Results[0].Outcome != "existing" || again.Results[1].Status != "failed" {
		t.Fatalf("capacity: %+v", again)
	}
	replay := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("a", 22), body)
	if replay.Code != 200 || replay.Body.String() != f.request(f.ctx, "POST", f.importPath(), strings.Repeat("a", 22), body).Body.String() {
		t.Fatal("replay changed")
	}
	if f.calls.Load() != 0 {
		t.Fatal("import performed discovery")
	}
}
func TestPersonalWholeShapeAndDigestConflictPrecedeWrites(t *testing.T) {
	f := newPersonalFixture(t)
	for _, body := range []string{
		`{"ownership_confirmed":true,"keys":[{"secret":"valid"},{"secret":"other","enabled":"true"}]}`,
		`{"ownership_confirmed":true,"keys":[{"secret":"valid"},{"secret":"other","unknown":1}]}`,
		`{"ownership_confirmed":true,"keys":[{"secret":"valid"},{"secret":"x","secret":"y"}]}`,
		`{"ownership_confirmed":true,"keys":[{"secret":"valid"},{"secret":null}]}`,
		`{"ownership_confirmed":true,"keys":[{"secret":"valid"},null]}`,
	} {
		w := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("s", 22), body)
		if w.Code != 400 {
			t.Fatalf("shape %d: %s", w.Code, w.Body.String())
		}
		if f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 0 || f.count(t, `SELECT COUNT(*) FROM personal_automation_batches`) != 0 {
			t.Fatal("shape error wrote resources")
		}
	}
	key := strings.Repeat("d", 22)
	decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), key, f.importBody("one")), 200)
	w := f.request(f.ctx, "POST", f.importPath(), key, f.importBody("two"))
	if w.Code != 409 || f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 1 {
		t.Fatalf("digest conflict: %d %s", w.Code, w.Body.String())
	}
	w = f.request(f.ctx, "POST", f.bindingPath(), key, `{"endpoint_key_ids":["1"],"upstream_model_id":"exact/model"}`)
	if w.Code != 409 {
		t.Fatalf("cross route conflict %d %s", w.Code, w.Body.String())
	}
}
func TestPersonalManualBindingAtomicTailAndExistingDisabled(t *testing.T) {
	f := newPersonalFixture(t)
	first, second := f.key(t, "a"), f.key(t, "b")
	body := fmt.Sprintf(`{"endpoint_key_ids":["%s","%s"],"upstream_model_id":"exact/model"}`, first, second)
	out := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("m", 22), body), 200)
	if out.Results[0].Outcome != "created" || out.Results[1].Outcome != "created" {
		t.Fatalf("append %+v", out)
	}
	if f.count(t, `SELECT COUNT(*) FROM model_bindings WHERE model_id=? AND ord IN (0,1)`, f.modelID) != 2 || f.count(t, `SELECT binding_revision FROM models WHERE id=?`, f.modelID) != 3 {
		t.Fatal("append did not preserve tail/revision")
	}
	f.exec(t, `UPDATE endpoint_keys SET enabled=0 WHERE id=?`, first)
	existing := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("n", 22), fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, first)), 200)
	if existing.Results[0].Outcome != "existing" || existing.Results[0].BindingID != out.Results[0].BindingID || f.calls.Load() != 0 || f.count(t, `SELECT binding_revision FROM models WHERE id=?`, f.modelID) != 3 {
		t.Fatal("existing binding changed or refreshed")
	}
	f.hooks.fail.Store(true)
	failed := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("o", 22), fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"rollback-model"}`, second)), 422)
	if failed.Results[0].Status != "failed" || f.count(t, `SELECT COUNT(*) FROM model_catalog_entries WHERE endpoint_key_id=? AND source_type='manual' AND normalized_model_id='rollback-model'`, second) != 0 {
		t.Fatal("failed append leaked manual entry")
	}
	f.hooks.fail.Store(false)
	replay := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("o", 22), fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"rollback-model"}`, second)), 422)
	if replay.Results[0].Status != "failed" {
		t.Fatal("confirmed failure was retried")
	}
}
func TestPersonalRefreshUsesRealDiscoveryStrictly(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   int32
		status int
	}{{"success", 0, 200}, {"empty", 1, 422}, {"failed", 2, 422}} {
		t.Run(test.name, func(t *testing.T) {
			f := newPersonalFixture(t)
			f.mode.Store(test.mode)
			key := f.key(t, "k")
			body := fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, key)
			out := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("r", 22), body), test.status)
			if f.calls.Load() != 1 {
				t.Fatalf("discovery calls %d", f.calls.Load())
			}
			if test.status == 200 && out.Results[0].Outcome != "created" {
				t.Fatalf("result %+v", out)
			}
			if f.count(t, `SELECT COUNT(*) FROM model_catalog_entries WHERE source_type='manual'`) != 0 {
				t.Fatal("refresh fell back to manual")
			}
			decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("r", 22), body), test.status)
			if f.calls.Load() != 1 {
				t.Fatal("replay redispatched")
			}
			if f.count(t, `SELECT COUNT(*) FROM logical_requests WHERE accounting_state<>'none' OR account_reserved_milli<>0`) != 0 {
				t.Fatal("discovery charged credits")
			}
		})
	}
}
func TestPersonalReadsPurposeOwnershipAndLiveGeneration(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	for _, level := range []int{0, 1, 5, 6} {
		if level == 0 {
			f.exec(t, `UPDATE users SET level=NULL WHERE id=?`, f.userID)
		} else {
			f.exec(t, `UPDATE users SET level=? WHERE id=?`, level, f.userID)
		}
		w := f.request(f.ctx, "GET", PersonalPrefix+"endpoints?page_size=10", "", "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture-upstream") {
			t.Fatalf("level %d: %d %s", level, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{PersonalPrefix + "endpoints?cursor=anything", PersonalPrefix + "models?page_size=11", PersonalPrefix + "models?q=" + strings.Repeat("q", 201), fmt.Sprintf("%sendpoints/%d?x=1", PersonalPrefix, f.endpointID)} {
		if w := f.request(f.ctx, "GET", path, "", ""); w.Code != 400 {
			t.Fatalf("query %d %s", w.Code, path)
		}
	}
	if w := f.request(f.ctx, "GET", PersonalPrefix+"endpoints", strings.Repeat("h", 22), ""); w.Code != 400 {
		t.Fatal("GET accepted idempotency key")
	}
	foreign := f.user(t, "foreign", 1)
	foreignCtx := authz.WithPersonalCaller(context.Background(), authz.PersonalCaller{UserID: foreign, Generation: 1})
	if w := f.request(foreignCtx, "GET", fmt.Sprintf("%sendpoints/%d/keys/%s", PersonalPrefix, f.endpointID, key), "", ""); w.Code != 404 {
		t.Fatalf("foreign %d %s", w.Code, w.Body.String())
	}
	if w := f.request(context.Background(), "GET", PersonalPrefix+"endpoints", "", ""); w.Code != 401 {
		t.Fatal("cookie-less purpose not required")
	}
	f.exec(t, `UPDATE caller_keys SET generation=2,key_hash=zeroblob(32),display_head='newh',display_tail='newt' WHERE user_id=?`, f.userID)
	if w := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("k", 22), f.importBody("fixture-upstream-k")); w.Code != 401 {
		t.Fatalf("revoked replay %d", w.Code)
	}
}
func TestPersonalFixedExpiryExportRetentionAndDeleteMappedReceipts(t *testing.T) {
	f := newPersonalFixture(t)
	f.key(t, "a")
	f.key(t, "b")
	actor, _ := personalActor(f.userID, false)
	controlActor, _ := idempotency.ActorScopeHash("user", fmt.Sprint(f.userID))
	tx, err := f.store.DB().BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := idempotency.Begin(f.ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: controlActor, Key: strings.Repeat("u", 22), RequestHash: actor, DecisionNow: f.clock.Load()})
	if err != nil {
		t.Fatal(err)
	}
	if err = idempotency.Complete(f.ctx, tx, decision, 200, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	export, err := f.service.ExportPersonalAutomation(f.ctx, tx, lifecycle.ExportRequest{UserID: f.userID, DecisionNow: f.clock.Load(), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(export) != 2 || export[0].Results[0].Status != "success" {
		t.Fatalf("export %+v", export)
	}
	encoded, _ := json.Marshal(export)
	for _, forbidden := range []string{"secret", "hash", "fixture-upstream", "root_key", "actor_scope"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("export contains %s", forbidden)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expires := f.count(t, `SELECT MIN(expires_at) FROM personal_automation_batches`)
	f.clock.Add(100)
	decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("a", 22), f.importBody("fixture-upstream-a")), 200)
	if f.count(t, `SELECT MIN(expires_at) FROM personal_automation_batches`) != expires {
		t.Fatal("retry extended expiry")
	}
	work, err := f.service.Retain(context.Background(), expires, 1, time.Now().Add(2*time.Second))
	if err != nil || work.Processed != 1 || !work.More {
		t.Fatalf("bounded retention %+v %v", work, err)
	}
	tx, err = f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PrepareDelete(context.Background(), tx, lifecycle.DeleteRequest{UserID: f.userID, DecisionNow: expires}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT COUNT(*) FROM personal_automation_batches`) != 0 || f.count(t, `SELECT COUNT(*) FROM personal_automation_steps`) != 0 || f.count(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='personal_automation'`) != 0 || f.count(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`) != 1 {
		t.Fatal("delete mapping removed unrelated receipts or retained batch data")
	}
}
