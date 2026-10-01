package stewardautomation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestPersonalHundredItemsAndSemanticFailures(t *testing.T) {
	f := newPersonalFixture(t)
	items := make([]string, 100)
	for i := range items {
		items[i] = `{"secret":"bulk-same-body","enabled":true,"note":"same","force_store_false":false,"max_concurrency":0,"max_rpm":0}`
	}
	items[50] = `{"secret":"invalid\nbody"}`
	body := `{"ownership_confirmed":true,"keys":[` + strings.Join(items, ",") + `]}`
	out := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("h", 22), body), 200)
	if len(out.Results) != 100 || out.Results[50].Status != "failed" || out.Results[0].Outcome != "created" || out.Results[99].Outcome != "existing" || f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 1 {
		t.Fatal("bulk shape or per-item failure semantics")
	}
}
func TestPersonalTimeoutReportsPartialAndResumesStableIdentity(t *testing.T) {
	f := newPersonalFixture(t)
	first, second := f.key(t, "a"), f.key(t, "b")
	decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("u", 22), fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model"}`, first)), 200)
	f.service.discoveryTimeout = 5 * time.Second
	f.mode.Store(3)
	body := fmt.Sprintf(`{"endpoint_key_ids":["%s","%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, first, second)
	out := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("t", 22), body), 200)
	if out.Results[0].Outcome != "existing" || out.Results[1].Status != "incomplete" || f.calls.Load() != 1 {
		t.Fatalf("partial %+v calls=%d", out, f.calls.Load())
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.count(t, `SELECT COUNT(*) FROM model_discovery_evidence WHERE endpoint_key_id=? AND state='checking'`, second) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("canceled worker did not finish evidence cleanup")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mode.Store(0)
	resumed := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("t", 22), body), 200)
	if resumed.Results[0].Outcome != "existing" || resumed.Results[1].Status != "failed" || f.calls.Load() != 1 {
		t.Fatal("timeout resume redispatched or changed confirmed item")
	}
}
func TestPersonalDeletionClearsOnlyMappedDiscoveryReceipts(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	id, _ := numericID(key)
	decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("r", 22), fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, key)), 200)
	if _, err := f.repo.RefreshDiscoveryAndWait(f.ctx, f.userID, f.endpointID, id); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='model_discovery'`) != 2 {
		t.Fatal("discovery fixture did not create independent receipts")
	}
	tx, err := f.store.DB().BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = f.service.PrepareDelete(f.ctx, tx, lifecycle.DeleteRequest{UserID: f.userID, DecisionNow: f.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='model_discovery'`) != 1 || f.count(t, `SELECT COUNT(*) FROM resource_operation_status WHERE stage='catalog_refresh'`) != 1 {
		t.Fatal("mapped cleanup removed unrelated discovery receipts")
	}
}

func TestPersonalLiveRevocationStopsLaterItemsAndResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   int32
		status int
	}{{"generation", 4, 401}, {"ban", 5, 403}} {
		t.Run(test.name, func(t *testing.T) {
			f := newPersonalFixture(t)
			first, second := f.key(t, "a"), f.key(t, "b")
			f.mode.Store(test.mode)
			body := fmt.Sprintf(`{"endpoint_key_ids":["%s","%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, first, second)
			w := f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("v", 22), body)
			if w.Code != test.status || f.calls.Load() != 1 || f.count(t, `SELECT COUNT(*) FROM model_bindings`) != 0 {
				t.Fatalf("revoked execution %d calls=%d %s", w.Code, f.calls.Load(), w.Body.String())
			}
		})
	}
}
func TestPersonalDisabledNewRefreshDoesNotDispatch(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	f.exec(t, `UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key)
	body := fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, key)
	out := decodeBatch(t, f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("d", 22), body), 422)
	if out.Results[0].Status != "failed" || f.calls.Load() != 0 || f.count(t, `SELECT COUNT(*) FROM model_discovery_evidence WHERE state<>'unknown'`) != 0 {
		t.Fatal("disabled new binding started discovery")
	}
}
func TestPersonalDeletedCredentialCannotBindAfterNetworkReturn(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	f.mode.Store(3)
	body := fmt.Sprintf(`{"endpoint_key_ids":["%s"],"upstream_model_id":"exact/model","catalog_mode":"refresh"}`, key)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.request(f.ctx, "POST", f.bindingPath(), strings.Repeat("e", 22), body) }()
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery did not start")
	}
	f.exec(t, `DELETE FROM endpoint_keys WHERE id=?`, key)
	close(f.release)
	select {
	case w := <-done:
		out := decodeBatch(t, w, 422)
		if out.Results[0].Status != "failed" {
			t.Fatal("deleted source reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("late discovery did not settle")
	}
	if f.count(t, `SELECT COUNT(*) FROM model_bindings`) != 0 {
		t.Fatal("late callback revived binding")
	}
}
func TestPersonalReadBudgetAndBulkBodyBudget(t *testing.T) {
	f := newPersonalFixture(t)
	for _, contentType := range []string{"", "text/plain"} {
		r := httptest.NewRequest("POST", f.importPath(), strings.NewReader(f.importBody("media-type-test"))).WithContext(f.ctx)
		r.Header.Set("Idempotency-Key", strings.Repeat("m", 22))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		f.service.ServeHTTP(w, r)
		if w.Code != 400 || f.count(t, `SELECT COUNT(*) FROM personal_automation_batches`) != 0 {
			t.Fatalf("invalid media type accepted: %q status=%d", contentType, w.Code)
		}
	}
	if w := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("p", 22), strings.Repeat(" ", idempotency.MaxControlBodyBytes+1)); w.Code != 413 {
		t.Fatalf("body budget %d", w.Code)
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	note := strings.Repeat("😀", 1024)
	for i := 0; i < 100; i++ {
		_, err = tx.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible',?,?,1,1,?,?)`, f.userID, fmt.Sprintf("https://endpoint-%d.example.test/v1", i), note, f.clock.Load(), f.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if w := f.request(f.ctx, "GET", PersonalPrefix+"endpoints?page_size=100", "", ""); w.Code != 422 || !strings.Contains(w.Body.String(), "resource_limit_exceeded") || strings.Contains(w.Body.String(), `"data"`) {
		t.Fatalf("response budget %d %s", w.Code, w.Body.String())
	}
	if w := f.request(f.ctx, "GET", PersonalPrefix+"endpoints?page_size=10", "", ""); w.Code != 200 {
		t.Fatalf("smaller page %d", w.Code)
	}
}
func TestPersonalCapacityRejectsOnlyNewRegistrationAndFixedWindowReuse(t *testing.T) {
	f := newPersonalFixture(t)
	original := f.key(t, "k")
	tx, err := f.store.DB().BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	actor, _ := personalActor(f.userID, false)
	for i := 0; i < 999; i++ {
		id, err := db.GenerateOpaqueID("pab_")
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("capacity-%d", i)))
		_, err = tx.Exec(`INSERT INTO personal_automation_batches(id,user_id,actor_scope_hash,root_key_hash,request_hash,kind,target_id,item_count,created_at,expires_at) VALUES(?,?,?,?,?,'key_import',?,1,?,?)`, id, f.userID, actor[:], hash[:], hash[:], f.endpointID, f.clock.Load(), f.clock.Load()+idempotency.ReplayWindowSeconds)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if w := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("x", 22), f.importBody("new-key")); w.Code != 429 {
		t.Fatalf("new registration capacity %d %s", w.Code, w.Body.String())
	}
	replay := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("k", 22), f.importBody("fixture-upstream-k")), 200)
	if replay.Results[0].EndpointKeyID != original {
		t.Fatal("capacity prevented existing replay")
	}
	f.clock.Add(idempotency.ReplayWindowSeconds)
	changed := decodeBatch(t, f.request(f.ctx, "POST", f.importPath(), strings.Repeat("k", 22), f.importBody("new-window")), 200)
	if changed.Results[0].Outcome != "created" || f.count(t, `SELECT COUNT(*) FROM endpoint_keys`) != 2 {
		t.Fatal("fixed-window reuse failed")
	}
}
func TestPersonalOwnerEndpointBodyIsolationAndDeletePreventsResume(t *testing.T) {
	f := newPersonalFixture(t)
	key := f.key(t, "k")
	otherEndpoint := f.exec(t, `INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible','https://other.example.test/v1','',1,1,?,?)`, f.userID, f.clock.Load(), f.clock.Load())
	other := decodeBatch(t, f.request(f.ctx, "POST", fmt.Sprintf("%sendpoints/%d/keys/batch-import", PersonalPrefix, otherEndpoint), strings.Repeat("j", 22), f.importBody("fixture-upstream-k")), 200)
	if other.Results[0].Outcome != "created" || other.Results[0].EndpointKeyID == key {
		t.Fatal("body dedup crossed endpoint ownership")
	}
	foreign := f.user(t, "foreign", 1)
	foreignEndpoint := f.exec(t, `INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible','https://foreign.example.test/v1','',1,1,?,?)`, foreign, f.clock.Load(), f.clock.Load())
	if w := f.request(f.ctx, "GET", fmt.Sprintf("%sendpoints/%d/keys/%s", PersonalPrefix, foreignEndpoint, key), "", ""); w.Code != 404 {
		t.Fatal("parent mismatch accepted")
	}
	tx, err := f.store.DB().BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = f.service.PrepareDelete(f.ctx, tx, lifecycle.DeleteRequest{UserID: f.userID, DecisionNow: f.clock.Load()}); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`DELETE FROM users WHERE id=?`, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if w := f.request(f.ctx, "POST", f.importPath(), strings.Repeat("k", 22), f.importBody("fixture-upstream-k")); w.Code != 401 || f.count(t, `SELECT COUNT(*) FROM personal_automation_batches`) != 0 {
		t.Fatal("deleted account resumed batch")
	}
}
