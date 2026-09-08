package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestEndpointPagesCountAuthorizedTenThousandRowsAndClampWithoutCursorWalk(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "page-owner")
	other := env.seedUser(t, "page-other")
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(?,'openai-compatible',?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10_017; i++ {
		user := owner
		if i > 10_007 {
			user = other
		}
		if _, err = stmt.Exec(user, fmt.Sprintf("https://endpoint-%d.example/v1", i), resourceTestNow, resourceTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err = stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	searched, err := env.repository.SearchEndpointsPage(context.Background(), owner, "ENDPOINT-1000", pagination.Default())
	if err != nil || searched.Pagination.TotalItems != "9" || len(searched.Data) != 9 || searched.Data[8].ID != "1000" {
		t.Fatalf("complete owner search: %+v %v", searched, err)
	}
	var before int64
	if err = env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{10, 20, 50, 100} {
		page, err := env.repository.ListEndpointsPage(context.Background(), owner, pagination.Request{Page: pagination.MaxPage, Size: size})
		if err != nil {
			t.Fatal(err)
		}
		last := (10_007 + size - 1) / size
		count := 10_007 - (last-1)*size
		if page.NextCursor != nil || page.Pagination == nil || page.Pagination.TotalItems != "10007" || page.Pagination.Page != strconv.Itoa(last) || len(page.Data) != count {
			t.Fatalf("size %d: metadata=%+v rows=%d", size, page.Pagination, len(page.Data))
		}
		if page.Data[len(page.Data)-1].ID != "1" {
			t.Fatalf("last row=%+v", page.Data[len(page.Data)-1])
		}
	}
	rows, err := env.store.DB().Query(`EXPLAIN QUERY PLAN `+endpointPageSelect+` ORDER BY e.updated_at DESC,e.id DESC LIMIT 20 OFFSET 9980`, owner)
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	plan := strings.Join(plans, "\n")
	t.Log(plan)
	if !strings.Contains(plan, "idx_endpoints_user_cursor") || strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("unindexed page order: %s", plan)
	}
	var after int64
	if err = env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("page reads changed persistent state")
	}
	env.authorizer.deny.Store(true)
	if _, err = env.repository.ListEndpointsPage(context.Background(), owner, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked page read=%v", err)
	}
}

func TestResourcePageHTTPRejectsMixedOrMalformedWindowsAndKeepsLegacyShape(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "page-http-owner")
	api := &httpAPI{repository: env.repository}
	for _, query := range []string{"page=0", "page=01", "page=-1", "page=1e2", "page=2147483648", "page=1&page=2", "page_size=30", "page_size=", "page=1&cursor=", "page=1&limit=20", "page=1&extra=1", "page=1&q=a&q=b", "page=1&q=%00", "page=1&q=%C2%85", "page=1&q=%ED%A0%80", "page=1&q=" + strings.Repeat("a", 129), "limit=20&q=word"} {
		r := httptest.NewRequest("GET", "/api/endpoints?"+query, nil)
		w := httptest.NewRecorder()
		api.listEndpoints(w, r, UserPrincipal{UserID: owner})
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"page=99", "page_size=10", "limit=10"} {
		r := httptest.NewRequest("GET", "/api/endpoints?"+query, nil)
		w := httptest.NewRecorder()
		api.listEndpoints(w, r, UserPrincipal{UserID: owner})
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if query == "limit=10" {
			if len(body) != 2 || body["pagination"] != nil {
				t.Fatalf("legacy shape=%s", w.Body.String())
			}
			continue
		}
		var meta pagination.Metadata
		if err := json.Unmarshal(body["pagination"], &meta); err != nil {
			t.Fatal(err)
		}
		if len(body) != 3 || string(body["next_cursor"]) != "null" || meta.Page != "1" || meta.TotalItems != "0" || meta.TotalPages != "1" {
			t.Fatalf("empty page=%s", w.Body.String())
		}
	}
}

func TestNestedCatalogAndCandidatePagesCountTheirOwnFilteredOwnerSet(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "nested-page-owner")
	other := env.seedUser(t, "nested-page-other")
	ep := env.createEndpoint(t, owner, resourceTestKey('a'))
	eid := resourceTestID(t, ep.ID)
	key := env.createEndpointKey(t, owner, eid, resourceTestKey('b'))
	kid := resourceTestID(t, key.ID)
	model := env.createModel(t, owner, resourceTestKey('c'), "logical", "page")
	mid := resourceTestID(t, model.ID)
	inputs := make([]ManualCatalogInput, 23)
	for i := range inputs {
		inputs[i] = ManualCatalogInput{UpstreamModelID: fmt.Sprintf("manual-%02d", i)}
	}
	mutation := resourceTestMutation(t, resourceTestKey('d'), "POST", routeManualCatalog, []int64{eid, kid}, createManualCanonical{Entries: inputs})
	if _, err := env.repository.CreateManualEntries(context.Background(), owner, eid, kid, mutation, inputs); err != nil {
		t.Fatal(err)
	}
	w := pagination.Request{Page: 3, Size: 10}
	manual, err := env.repository.GetCatalogPage(context.Background(), owner, eid, kid, "manual", w)
	if err != nil || manual.Pagination == nil || manual.Pagination.TotalItems != "23" || len(manual.ManualEntries) != 3 || len(manual.AutomaticEntries) != 0 {
		t.Fatalf("manual=%+v err=%v", manual, err)
	}
	automatic, err := env.repository.GetCatalogPage(context.Background(), owner, eid, kid, "automatic", w)
	if err != nil || automatic.Pagination.TotalItems != "0" || automatic.Pagination.Page != "1" || len(automatic.ManualEntries) != 0 {
		t.Fatalf("automatic=%+v err=%v", automatic, err)
	}
	query := CandidateQuery{EndpointID: eid, KeyID: kid, Source: "manual", Query: "manual-1"}
	candidates, err := env.repository.BindingCandidatesPage(context.Background(), owner, mid, query, w)
	if err != nil || candidates.Pagination.TotalItems != "10" || candidates.Pagination.Page != "1" || len(candidates.Data) != 10 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	for _, c := range candidates.Data {
		if c.EndpointKeyID != key.ID || !strings.HasPrefix(c.UpstreamModelID, "manual-1") {
			t.Fatal("unscoped candidate")
		}
	}
	if _, err = env.repository.GetCatalogPage(context.Background(), other, eid, kid, "manual", w); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign catalog=%v", err)
	}
	if _, err = env.repository.ListEndpointKeysPage(context.Background(), other, eid, w); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign keys=%v", err)
	}
	if _, err = env.repository.BindingCandidatesPage(context.Background(), other, mid, query, w); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign model=%v", err)
	}
	otherEP := env.createEndpoint(t, other, resourceTestKey('e'))
	query.EndpointID = resourceTestID(t, otherEP.ID)
	query.KeyID = 0
	if _, err = env.repository.BindingCandidatesPage(context.Background(), owner, mid, query, w); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign candidate parent=%v", err)
	}
	keys, err := env.repository.ListEndpointKeysPage(context.Background(), owner, eid, w)
	if err != nil || keys.Pagination.TotalItems != "1" || keys.Data[0].ID != key.ID {
		t.Fatalf("own keys=%+v err=%v", keys, err)
	}
	models, err := env.repository.ListModelsPage(context.Background(), owner, w)
	if err != nil || models.Pagination.TotalItems != "1" || models.Data[0].ID != model.ID {
		t.Fatalf("own models=%+v err=%v", models, err)
	}
}

func TestMainstreamChannelPagesCountFilteredSetsAndRecheckAdmin(t *testing.T) {
	env := newResourceTestEnvironment(t)
	admin := resourceAdminUser(t, env, "channel-page-admin")
	for i := 1; i <= 25; i++ {
		state := "active"
		var retired any
		if i > 13 {
			state, retired = "retired", resourceTestNow
		}
		_, err := env.store.DB().Exec(`INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,'api_platform','openai-compatible','https://example.com/v1',0,?,1,?,?,?)`, fmt.Sprintf("mch_%021dA", i), fmt.Sprintf("Channel %d", i), state, resourceTestNow, resourceTestNow, retired)
		if err != nil {
			t.Fatal(err)
		}
	}
	api := &httpAPI{repository: env.repository}
	for _, test := range []struct {
		query string
		total string
		page  string
		rows  int
	}{{"page=9&page_size=10", "13", "2", 3}, {"page=2&page_size=10&state=retired", "12", "2", 2}, {"page=1&page_size=20&state=all", "25", "1", 20}} {
		response := resourceAdminHTTPCall(t, api.listMainstreamChannels, admin, "GET", routeMainstreamChannels+"?"+test.query, "", "", "")
		var page Page[MainstreamChannel]
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Pagination == nil || page.Pagination.TotalItems != test.total || page.Pagination.Page != test.page || len(page.Data) != test.rows || page.NextCursor != nil {
			t.Fatalf("%s: %d %s", test.query, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"page=1&state=", "page=1&state=unknown", "page=1&state=all&state=active", "page=1&limit=10"} {
		response := resourceAdminHTTPCall(t, api.listMainstreamChannels, admin, "GET", routeMainstreamChannels+"?"+query, "", "", "")
		if response.Code != 400 {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
	env.authorizer.deny.Store(true)
	response := resourceAdminHTTPCall(t, api.listMainstreamChannels, admin, "GET", routeMainstreamChannels+"?page=1&state=all", "", "", "")
	if response.Code != 403 {
		t.Fatalf("revoked admin: %d %s", response.Code, response.Body.String())
	}
}
