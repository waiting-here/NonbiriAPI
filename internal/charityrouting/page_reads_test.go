package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestManagementModelPagesCountFilteredSetAndAuthorizeBeforeEmpty(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(5)
	steward := env.seedUser(t, false, &level)
	for i := 0; i < 23; i++ {
		input := testModelCreate()
		input.Model = fmt.Sprintf("page-%02d", i)
		input.Enabled = i%2 == 0
		if _, err := env.service.CreateAdmin(context.Background(), routingMutation(t, byte('a'+i), http.MethodPost, routeAdminModels, nil, map[string]any{"model": i}), input); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		for _, size := range []int{10, 20, 50, 100} {
			out, err := env.service.modelsPage(context.Background(), role, steward, "page-", nil, pagination.Request{Page: pagination.MaxPage, Size: size})
			if err != nil {
				t.Fatal(err)
			}
			last := (23 + size - 1) / size
			if out.NextCursor != nil || out.Pagination.TotalItems != "23" || out.Pagination.Page != strconv.Itoa(last) || len(out.Data) != 23-(last-1)*size || out.Data[len(out.Data)-1].Model != "page-22" {
				t.Fatalf("role=%s size=%d result=%+v rows=%d", role, size, out.Pagination, len(out.Data))
			}
		}
	}
	enabled := true
	filtered, err := env.service.modelsPage(context.Background(), roleSteward, steward, "page-", &enabled, pagination.Default())
	if err != nil || filtered.Pagination.TotalItems != "12" {
		t.Fatalf("filtered=%+v err=%v", filtered.Pagination, err)
	}
	for _, item := range filtered.Data {
		if !item.Enabled {
			t.Fatal("disabled model passed filter")
		}
	}
	filtered, err = env.service.modelsPage(context.Background(), roleSteward, steward, "page-%", nil, pagination.Default())
	if err != nil || filtered.Pagination.TotalItems != "0" {
		t.Fatalf("LIKE wildcard was not literal: %+v %v", filtered.Pagination, err)
	}
	env.auth.denySteward.Store(true)
	if _, err := env.service.modelsPage(context.Background(), roleSteward, steward, "empty", nil, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized COUNT returned %v", err)
	}
}

func TestManagementBindingPagesShareExpiryFiltersWithoutWrites(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(5)
	steward := env.seedUser(t, false, &level)
	donor := env.seedUser(t, false, nil)
	model := env.createModel(t, 'A')
	modelID, _ := parsePositiveID(model.ID)
	var firstDonation, firstKey, firstPhysical int64
	for i := 0; i < 24; i++ {
		did, kid, physical := env.seedCandidate(t, donor, byte('a'+i), fmt.Sprintf("upstream-%02d", i))
		if i == 0 {
			firstDonation, firstKey, firstPhysical = did, kid, physical
		}
		if i == 23 {
			if _, err := env.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, routingTestNow, kid); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := env.store.DB().Exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'manual-only',0,1,1,1,?)`, firstPhysical, routingTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream-00',0,?,?)`, modelID, firstKey, firstPhysical, routingTestNow, routingTestNow); err != nil {
		t.Fatal(err)
	}
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		for _, size := range []int{10, 20, 50, 100} {
			request := pagination.Request{Page: pagination.MaxPage, Size: size}
			candidates, err := env.service.candidatesPage(context.Background(), role, steward, modelID, CandidateQuery{}, request)
			if err != nil {
				t.Fatal(err)
			}
			donations, _, err := env.service.bindingSourcesPage(context.Background(), role, steward, modelID, 0, request)
			if err != nil {
				t.Fatal(err)
			}
			last := (23 + size - 1) / size
			for _, meta := range []*pagination.Metadata{candidates.Pagination, donations.Pagination} {
				if meta.TotalItems != "23" || meta.Page != strconv.Itoa(last) {
					t.Fatalf("incorrect filtered count/window: %+v", meta)
				}
			}
			if len(candidates.Data) != 23-(last-1)*size || len(donations.Data) != len(candidates.Data) {
				t.Fatal("incorrect page length")
			}
		}
	}
	page, err := env.service.candidatesPage(context.Background(), roleSteward, steward, modelID, CandidateQuery{DonationID: firstDonation, DonationKeyID: firstKey, Source: "manual", Query: "manual-only"}, pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].UpstreamModelID != "manual-only" {
		t.Fatalf("scoped candidates=%+v err=%v", page, err)
	}
	auto, err := env.service.candidatesPage(context.Background(), roleSteward, steward, modelID, CandidateQuery{DonationID: firstDonation, Source: "automatic"}, pagination.Default())
	if err != nil || auto.Pagination.TotalItems != "0" {
		t.Fatalf("bound/unsupported candidate included: %+v %v", auto, err)
	}
	_, keys, err := env.service.bindingSourcesPage(context.Background(), roleSteward, steward, modelID, firstDonation, pagination.Request{Page: 99, Size: 10})
	if err != nil || len(keys.Data) != 1 || keys.Data[0].Note != "safe label" || keys.Pagination.Page != "1" {
		t.Fatalf("keys=%+v %v", keys, err)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private key note", `"owner"`, `"discord_id"`, `"endpoint_key_id"`, `"encrypted_secret"`, `"reviewer"`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("shared key leaked %s", forbidden)
		}
	}
	if env.state.dueCalls.Load() != 0 {
		t.Fatal("page read materialized expiry")
	}
	var expiredStatus string
	if err := env.store.DB().QueryRow(`SELECT d.status FROM donations d JOIN donation_keys dk ON dk.donation_id=d.id WHERE dk.expires_at=?`, routingTestNow).Scan(&expiredStatus); err != nil || expiredStatus != "approved" {
		t.Fatalf("read changed donation: %s %v", expiredStatus, err)
	}
	if _, err := env.service.candidatesPage(context.Background(), roleSteward, steward, 99999, CandidateQuery{}, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent=%v", err)
	}
	env.auth.denySteward.Store(true)
	if _, err := env.service.candidatesPage(context.Background(), roleSteward, steward, 99999, CandidateQuery{}, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("parent leaked before authority=%v", err)
	}
	if _, _, err := env.service.bindingSourcesPage(context.Background(), roleSteward, steward, modelID, firstDonation, pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("key count authorized stale role=%v", err)
	}
}

func TestManagementPageHTTPRejectsMixedWindowsAndPreservesCursor(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(5)
	steward := env.seedUser(t, false, &level)
	model := env.createModel(t, 'H')
	api := &httpAPI{service: env.service}
	for _, route := range []string{"models", "candidates", "donations", "keys"} {
		request := func(query string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
			r.SetPathValue("id", model.ID)
			r.SetPathValue("donationId", "9999")
			w := httptest.NewRecorder()
			switch route {
			case "models":
				api.listModels(w, r, roleSteward, steward)
			case "candidates":
				api.candidates(w, r, roleSteward, steward)
			case "donations":
				api.bindingSources(w, r, roleSteward, steward, false)
			case "keys":
				api.bindingSources(w, r, roleSteward, steward, true)
			}
			return w
		}
		for _, query := range []string{"page=0", "page=01", "page=2147483648", "page_size=30", "page=1&page=2", "page=1&cursor=", "page=1&limit=20", "page=1&unexpected=1"} {
			if w := request(query); w.Code != 400 {
				t.Fatalf("%s %s: %d %s", route, query, w.Code, w.Body.String())
			}
		}
		for _, query := range []string{"page=99", "page_size=10", "limit=10"} {
			w := request(query)
			if w.Code != 200 {
				t.Fatalf("%s %s: %d %s", route, query, w.Code, w.Body.String())
			}
			var result map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (query == "limit=10") != (result["pagination"] == nil) {
				t.Fatalf("legacy/page shape: %s", w.Body.String())
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := env.service.modelsPage(ctx, roleAdmin, 0, "", nil, pagination.Default()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestCandidatePagesUseIndexedPairOrderAtScale(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	model := env.createModel(t, 'S')
	modelID, _ := parsePositiveID(model.ID)
	_, _, physical := env.seedCandidate(t, env.caller, 's', "pair-00000")
	tx, err := env.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,?,1,0,1,1,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 10017; i++ {
		if _, err := stmt.Exec(physical, fmt.Sprintf("pair-%05d", i), routingTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{10, 20, 50, 100} {
		out, err := env.service.candidatesPage(context.Background(), roleAdmin, 0, modelID, CandidateQuery{}, pagination.Request{Page: pagination.MaxPage, Size: size})
		if err != nil {
			t.Fatal(err)
		}
		last := (10017 + size - 1) / size
		if out.Pagination.TotalItems != "10017" || out.Pagination.Page != strconv.Itoa(last) || len(out.Data) != 10017-(last-1)*size || out.Data[len(out.Data)-1].UpstreamModelID != "pair-10016" {
			t.Fatalf("scale window: %+v rows=%d", out.Pagination, len(out.Data))
		}
	}
	selection, args := candidatePageSelection(modelID, routingTestNow, CandidateQuery{})
	rows, err := env.store.DB().Query(`EXPLAIN QUERY PLAN `+selection+` ORDER BY dk.id,pc.normalized_model_id LIMIT 100 OFFSET 9900`, args...)
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
	if !strings.Contains(plan, "model_pair_catalog") || strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("candidate page uses unindexed ordering: %s", plan)
	}
}
