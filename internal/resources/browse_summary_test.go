package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Seed valid current and stale catalog pairs with many independent logical
// models. The foreign binding is deliberately hostile storage: owner filtering
// must cover both the model and the physical endpoint even in that case.
func seedBrowseModels(t *testing.T, env *resourceTestEnvironment, owner, other, keyID int64, count int) {
	t.Helper()
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE model_discovery_evidence SET state='succeeded',revision=3,completed_at=?,fetched_count=2 WHERE endpoint_key_id=?`, resourceTestNow, keyID); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"manual", "automatic", "stale", "unsupported"} {
		manual, automatic, revision := 0, 0, 0
		if i == 0 {
			manual = 1
		}
		if i == 1 {
			automatic, revision = 1, 3
		}
		if i == 2 {
			automatic, revision = 1, 2
		}
		if _, err := tx.Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,automatic_supports,automatic_revision,updated_at) VALUES(?,?,?,?,?,?)`, keyID, name, manual, automatic, revision, resourceTestNow); err != nil {
			t.Fatal(err)
		}
		if manual+automatic > 0 {
			source, identity, sourceRevision := "manual", name, 1
			if automatic > 0 {
				source, identity, sourceRevision = "automatic", fmt.Sprintf("%d:0", revision), revision
			}
			if _, err := tx.Exec(`INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,source_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, keyID, source, identity, name, sourceRevision, resourceTestNow, resourceTestNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	models, err := tx.Prepare(`INSERT INTO models(user_id,provider,model,full_name,binding_revision,created_at,updated_at) VALUES(?,'logical',?,?,1,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer models.Close()
	bindings, err := tx.Prepare(`INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,0,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.Close()
	for i := 0; i <= count; i++ {
		user := owner
		if i == count {
			user = other
		}
		name := fmt.Sprintf("model-%05d", i)
		result, err := models.Exec(user, name, "logical/"+name, resourceTestNow, resourceTestNow)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.Exec(id, keyID, []string{"manual", "automatic", "stale", "unsupported"}[i%4], resourceTestNow, resourceTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestResourceBrowseCountsCompleteOwnerSetWithBoundedPreviewsAndIndexedPages(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "browse-owner"), env.seedUser(t, "browse-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	kid := resourceTestID(t, key.ID)
	seedBrowseModels(t, env, owner, other, kid, 10_017)
	if _, err := env.store.DB().Exec(`UPDATE endpoint_key_limits SET max_concurrency=17,max_rpm=123 WHERE endpoint_key_id=?`, kid); err != nil {
		t.Fatal(err)
	}
	before := env.rowCount(t, `SELECT total_changes()`)
	started := time.Now()
	endpoints, err := env.repository.ListEndpointsPage(context.Background(), owner, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got := endpoints.Data[0].Browse; got == nil || got.ModelCount != "10017" || got.AvailableKeyCount != "1" || got.State != "available" {
		t.Fatalf("endpoint browse=%+v", got)
	}
	keys, err := env.repository.ListEndpointKeysPage(context.Background(), owner, eid, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got := keys.Data[0].Browse; got == nil || got.ModelCount != "10017" || got.BindingCount != "10017" || got.AvailableBindingCount != "5009" || len(got.Preview) != 3 || got.Discovery.State != "succeeded" {
		t.Fatalf("key browse=%+v", got)
	}
	for _, size := range []int{10, 20, 50, 100} {
		window := pagination.Request{Page: pagination.MaxPage, Size: size}
		page, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid, kid, "", window)
		last := (10_017 + size - 1) / size
		if err != nil || page.Pagination == nil || page.Pagination.TotalItems != "10017" || page.Pagination.Page != strconv.Itoa(last) || len(page.Data) != 10_017-(last-1)*size {
			t.Fatalf("size %d: %+v err=%v", size, page, err)
		}
		for _, binding := range page.Data {
			if binding.EndpointID != ep.ID || binding.EndpointKeyID != key.ID || binding.ModelFullName == "logical/model-10017" || binding.MaxConcurrency != 17 || binding.MaxRPM != 123 {
				t.Fatalf("binding projection=%+v", binding)
			}
		}
		models, err := env.repository.ListModelsPage(context.Background(), owner, window)
		if err != nil || models.Pagination.TotalItems != "10017" {
			t.Fatalf("models=%+v err=%v", models.Pagination, err)
		}
		for _, model := range models.Data {
			if model.Browse == nil || len(model.Browse.Preview) != 1 || model.Browse.Preview[0].ModelID != model.ID {
				t.Fatalf("model=%+v", model)
			}
		}
	}
	page, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid, kid, "manual", pagination.Default())
	if err != nil || page.Pagination.TotalItems != "2505" {
		t.Fatalf("filtered=%+v err=%v", page.Pagination, err)
	}
	for _, item := range page.Data {
		if item.UpstreamModelID != "manual" || item.State != "available" {
			t.Fatalf("filtered row=%+v", item)
		}
	}
	if env.rowCount(t, `SELECT total_changes()`) != before {
		t.Fatal("browse mutated storage")
	}
	t.Logf("complete 10017-model summaries and four page sizes in %s", time.Since(started))
	rows, err := env.store.DB().Query(`EXPLAIN QUERY PLAN `+ownedBindingSelect+ownedBindingScope+` AND e.id=? AND k.id=? ORDER BY b.upstream_model_id,b.id LIMIT 20 OFFSET 9980`, owner, owner, eid, kid)
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	plan := strings.Join(plans, "\n")
	t.Log(plan)
	if !strings.Contains(plan, "idx_model_bindings_key_browse") || strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("unindexed inverse page: %s", plan)
	}
	for _, legacy := range []any{ep, key} {
		body, err := json.Marshal(legacy)
		if err != nil || strings.Contains(string(body), `"browse"`) {
			t.Fatalf("legacy shape changed: %s %v", body, err)
		}
	}
	if _, err := env.repository.ListKeyBindingsPage(context.Background(), other, eid, kid, "", pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign page=%v", err)
	}
	if _, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid+1, kid, "", pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong parent=%v", err)
	}
	env.authorizer.deny.Store(true)
	if _, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid, kid, "", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked page=%v", err)
	}
	env.authorizer.deny.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := env.repository.ListKeyBindingsPage(ctx, owner, eid, kid, "", pagination.Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled page=%v", err)
	}
}

func TestResourceBrowseEligibilityMatchesRoutingAndReportsPhysicalReasons(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "browse-states-owner"), env.seedUser(t, "browse-states-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	empty, err := env.repository.ListEndpointsPage(context.Background(), owner, pagination.Default())
	if err != nil || empty.Data[0].Browse.State != "no_keys" {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	kid := resourceTestID(t, key.ID)
	seedBrowseModels(t, env, owner, other, kid, 4)
	for _, test := range []struct {
		name, sql, physical string
		available           int
	}{
		{"current", `SELECT 1`, "available", 2},
		{"failed discovery", `UPDATE model_discovery_evidence SET state='failed',safe_class='auth'`, "available", 1},
		{"key disabled", `UPDATE endpoint_keys SET enabled=0`, "key_disabled", 0},
		{"endpoint disabled", `UPDATE endpoints SET enabled=0`, "endpoint_disabled", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := env.store.DB().Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			page, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid, kid, "", pagination.Default())
			if err != nil {
				t.Fatal(err)
			}
			tx, err := env.store.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			available := 0
			for _, item := range page.Data {
				_, eligible, err := bindingSelectionConnectorTx(context.Background(), tx, owner, kid, item.UpstreamModelID)
				if err != nil || eligible != (item.State == "available") {
					t.Fatalf("routing disagreement %+v %v", item, err)
				}
				if eligible {
					available++
				}
				if test.physical != "available" && item.State != test.physical {
					t.Fatalf("physical state=%+v", item)
				}
			}
			tx.Rollback()
			if available != test.available {
				t.Fatalf("available=%d", available)
			}
			keys, err := env.repository.ListEndpointKeysPage(context.Background(), owner, eid, pagination.Default())
			if err != nil || keys.Data[0].Browse.AvailableBindingCount != strconv.Itoa(available) {
				t.Fatalf("summary=%+v err=%v", keys, err)
			}
		})
	}
	caseID := "rpc_" + strings.Repeat("A", 22)
	if _, err := env.store.DB().Exec(`UPDATE endpoints SET enabled=1; UPDATE endpoint_keys SET enabled=1;`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at)
VALUES(?,zeroblob(32),'openai-compatible','https://example.com/v1','pending_review','complete',1,1,?,0,0,0,?)`, caseID, resourceTestNow+3600, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`INSERT INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, kid, caseID, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	page, err := env.repository.ListKeyBindingsPage(context.Background(), owner, eid, kid, "", pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Data {
		if item.State != "key_suspended" {
			t.Fatalf("suspension=%+v", item)
		}
	}
	endpoints, err := env.repository.ListEndpointsPage(context.Background(), owner, pagination.Default())
	if err != nil || endpoints.Data[0].Browse.State != "no_usable_key" || endpoints.Data[0].Browse.AvailableKeyCount != "0" {
		t.Fatalf("endpoint=%+v err=%v", endpoints, err)
	}
	seedDeletionTestMembership(t, env, owner, kid, "openai-compatible")
	keys, err := env.repository.ListEndpointKeysPage(context.Background(), owner, eid, pagination.Default())
	if err != nil || keys.Data[0].Browse.DonationEligibility != "security_processing" {
		t.Fatalf("suspension takes precedence over membership: %+v %v", keys, err)
	}
}

func TestDonationSelectionBrowseUsesSubmissionOccupancyAndLiteralSearch(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner, other := env.seedUser(t, "donation-browse-owner"), env.seedUser(t, "donation-browse-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	kid := resourceTestID(t, key.ID)
	if _, err := env.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0,note='Alpha%_资源' WHERE id=?`, kid); err != nil {
		t.Fatal(err)
	}
	assertEligibility := func(want string) {
		t.Helper()
		page, err := env.repository.SearchEndpointKeysPage(context.Background(), owner, eid, "ALPHA%_", pagination.Default())
		if err != nil || len(page.Data) != 1 || page.Pagination.TotalItems != "1" || page.Data[0].Browse.DonationEligibility != want {
			t.Fatalf("donation eligibility want %s: %+v %v", want, page, err)
		}
	}
	assertEligibility("eligible")
	_, donationKeyID := seedDeletionTestMembership(t, env, owner, kid, "openai-compatible")
	assertEligibility("already_donated")
	if _, err := env.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, resourceTestNow, donationKeyID); err != nil {
		t.Fatal(err)
	}
	assertEligibility("already_donated")
	if env.rowCount(t, `SELECT COUNT(*) FROM donation_key_memberships WHERE endpoint_key_id=?`, kid) != 1 {
		t.Fatal("browse detached an expired membership")
	}
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := terminateDeletionTestMemberships(context.Background(), tx, []int64{kid}, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertEligibility("eligible")
	page, err := env.repository.SearchEndpointKeysPage(context.Background(), owner, eid, "Alpha__", pagination.Default())
	if err != nil || page.Pagination.TotalItems != "0" {
		t.Fatalf("wildcard interpreted: %+v %v", page, err)
	}
	if _, err := env.repository.SearchEndpointKeysPage(context.Background(), other, eid, "Alpha", pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign searched keys: %v", err)
	}
	api := &httpAPI{repository: env.repository}
	for _, query := range []string{"page=1&q=a&q=b", "page=1&q=%C2%85", "page=1&q=%ED%A0%80", "page=1&q=" + strings.Repeat("a", 129)} {
		w := resourceHTTPCall(t, api.listEndpointKeys, UserPrincipal{UserID: owner}, "GET", "/api/endpoints/"+ep.ID+"/keys?"+query, "", "", map[string]string{"id": ep.ID})
		if w.Code != 400 {
			t.Fatalf("malformed key search: %s %d", query, w.Code)
		}
	}
}

func TestKeyBindingPageHTTPStrictScopeAndWindow(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "binding-page-http-owner")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	api := &httpAPI{repository: env.repository}
	pathValues := map[string]string{"id": ep.ID, "keyId": key.ID}
	for _, query := range []string{"cursor=", "limit=20", "page=0", "page=1&page=2", "page_size=30", "upstream_model_id=", "upstream_model_id=a&upstream_model_id=b", "upstream_model_id=" + url.QueryEscape(strings.Repeat("界", 513)), "upstream_model_id=a%00b", "extra=1"} {
		w := resourceHTTPCall(t, api.listKeyBindings, UserPrincipal{UserID: owner}, "GET", "/api/endpoints/"+ep.ID+"/keys/"+key.ID+"/bindings?"+query, "", "", pathValues)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"", "?page=999&page_size=10", "?upstream_model_id=" + url.QueryEscape("完整/model ID")} {
		w := resourceHTTPCall(t, api.listKeyBindings, UserPrincipal{UserID: owner}, "GET", "/api/endpoints/"+ep.ID+"/keys/"+key.ID+"/bindings"+query, "", "", pathValues)
		var page Page[KeyBindingView]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Pagination == nil || page.Pagination.TotalItems != "0" || page.Pagination.Page != "1" {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	w := resourceHTTPCall(t, api.listKeyBindings, UserPrincipal{UserID: owner}, "GET", "/api/endpoints/"+ep.ID+"/keys/"+key.ID+"/bindings", "{}", "", pathValues)
	if w.Code != 400 {
		t.Fatalf("GET body=%d", w.Code)
	}
}
