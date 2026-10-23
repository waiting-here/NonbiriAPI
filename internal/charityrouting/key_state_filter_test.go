package charityrouting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBindingKeyStateFiltersPreserveBaseAndMatchMenus(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	model := e.createModel(t, 'A')
	mid, _ := parsePositiveID(model.ID)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	states := []string{"available", "disabled", "suspended", "exhausted", "rate_limited", "pending", "expired", "ended"}
	donations, keys := map[string]int64{}, map[string]int64{}
	until := e.clock.Load() + 60
	for i, state := range states {
		did, kid, physical := e.seedCandidate(t, owner, byte('a'+i), "auto-upstream")
		donations[state], keys[state] = did, kid
		exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'member-manual','member-manual',1,?,?)`, kid, e.clock.Load(), e.clock.Load())
		if state != "available" {
			exec(`INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at) VALUES(?,?,?)`, kid, until, e.clock.Load())
		}
		switch state {
		case "disabled":
			exec(`UPDATE endpoints SET enabled=0 WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)`, physical)
		case "suspended":
			caseID, err := db.GenerateOpaqueID("rpc_")
			if err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at) VALUES(?,?,'openai-compatible','https://state.routing.test/v1','pending_review','complete',1,1,?,0,0,0,?)`, caseID, make([]byte, 32), until, e.clock.Load())
			exec(`INSERT INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, physical, caseID, e.clock.Load())
		case "exhausted":
			exec(`UPDATE donation_keys SET output_token_limit_mag=nbi_u128(0),input_token_reserve=1,output_token_reserve=1 WHERE id=?`, kid)
		case "pending":
			exec(`UPDATE donations SET status='pending',reviewed_by_role='' WHERE id=?`, did)
			exec(`UPDATE donation_uses SET current_approved_revision_id=NULL,revision=revision+1 WHERE donation_key_id=?`, kid)
		case "expired":
			exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, e.clock.Load(), kid)
		case "ended":
			exec(`DELETE FROM donation_key_memberships WHERE donation_key_id=?`, kid)
			exec(`UPDATE donation_keys SET enabled=0,ended_reason='terminated',ended_at=?,report_match_until=? WHERE id=?`, e.clock.Load(), e.clock.Load()+7776000, kid)
		}
	}
	for _, state := range append([]string{"", "all"}, states...) {
		want := 2
		menuWant := 1
		if state == "" || state == "all" {
			want = 10
			menuWant = 5
		}
		if state == "pending" || state == "expired" || state == "ended" {
			want = 0
			menuWant = 0
		}
		for _, role := range []roleKind{roleAdmin, roleSteward} {
			actor := testRoleActor(role, steward)
			out, err := e.service.candidatesPage(ctx, role, actor, mid, CandidateQuery{KeyState: state}, pagination.Request{Page: 99, Size: 10})
			if err != nil || out.Pagination.TotalItems != fmt.Sprint(want) || len(out.Data) != want {
				t.Fatalf("%s %s candidates=%+v %v", role, state, out, err)
			}
			legacy, _, _, err := e.service.bindingCandidates(ctx, role, actor, mid, CandidateQuery{KeyState: state, Limit: 50})
			if err != nil || len(legacy) != want {
				t.Fatalf("%s legacy %s=%+v %v", role, state, legacy, err)
			}
			ds, _, err := e.service.bindingSourcesPage(ctx, role, actor, mid, 0, state, pagination.Default())
			if err != nil || len(ds.Data) != menuWant || ds.Pagination.TotalItems != fmt.Sprint(menuWant) {
				t.Fatalf("%s %s donations=%+v %v", role, state, ds, err)
			}
			if menuWant == 1 {
				if ds.Data[0].ID != fmt.Sprint(donations[state]) || ds.Data[0].KeyCount != 1 {
					t.Fatal(ds)
				}
				_, ks, err := e.service.bindingSourcesPage(ctx, role, actor, mid, donations[state], state, pagination.Default())
				if err != nil || len(ks.Data) != 1 || ks.Data[0].DonationKeyID != fmt.Sprint(keys[state]) {
					t.Fatal(ks, err)
				}
			}
		}
	}
	for _, source := range []string{"automatic", "manual"} {
		q := CandidateQuery{DonationID: donations["available"], DonationKeyID: keys["available"], KeyState: "available", Source: source, Query: "member-manual"}
		out, err := e.service.candidatesPage(ctx, roleAdmin, 0, mid, q, pagination.Default())
		want := "0"
		if source == "manual" {
			want = "1"
		}
		if err != nil || out.Pagination.TotalItems != want {
			t.Fatalf("combined %+v: %+v %v", q, out, err)
		}
	}
	e.clock.Store(until)
	out, err := e.service.candidatesPage(ctx, roleAdmin, 0, mid, CandidateQuery{KeyState: "rate_limited"}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "0" {
		t.Fatal(out, err)
	}
	out, err = e.service.candidatesPage(ctx, roleAdmin, 0, mid, CandidateQuery{KeyState: "available"}, pagination.Default())
	if err != nil || out.Pagination.TotalItems != "4" {
		t.Fatal(out, err)
	}
}

func TestTraineeCandidateStateFiltersScopeBothUnionBranches(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	model := e.createModel(t, 'B')
	mid, _ := parsePositiveID(model.ID)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, mid)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	seedRoutingChannel(t, e, channel, "Example", "https://example.test/v1")
	mainDonation, mainKey, _ := e.seedCandidateWithConnector(t, owner, "main", "shared-auto", connectorcontract.TypeOpenAICompatible, channel)
	_, customKey, _ := e.seedCandidate(t, owner, 'x', "private-auto")
	for _, kid := range []int64{mainKey, customKey} {
		exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,'member-manual','member-manual',1,?,?)`, kid, routingTestNow, routingTestNow)
	}
	api := httpAPI{service: e.service}
	for _, state := range []string{"", "all", "available", "disabled"} {
		for _, source := range []string{"", "automatic", "manual"} {
			for _, numbered := range []bool{false, true} {
				query := url.Values{"source": {source}}
				if state != "" {
					query.Set("key_state", state)
				}
				if numbered {
					query.Set("page", "1")
					query.Set("page_size", "10")
				} else {
					query.Set("limit", "50")
				}
				r := httptest.NewRequest(http.MethodGet, "/candidates?"+query.Encode(), nil)
				r.SetPathValue("id", model.ID)
				w := httptest.NewRecorder()
				api.candidates(w, r, roleSteward, trainee)
				var out Page[StewardBindingCandidate]
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
					t.Fatalf("%s %d %s %v", query, w.Code, w.Body, err)
				}
				want := 2
				if source == "automatic" {
					want = 1
				}
				if state == "disabled" {
					want = 0
				}
				if len(out.Data) != want || numbered && out.Pagination.TotalItems != fmt.Sprint(want) {
					t.Fatalf("%s: %+v", query, out)
				}
				for _, item := range out.Data {
					if item.DonationKeyID != fmt.Sprint(mainKey) {
						t.Fatalf("non-mainstream leaked: %+v", item)
					}
				}
			}
		}
		ds, _, err := e.service.bindingSourcesPage(context.Background(), roleSteward, trainee, mid, 0, state, pagination.Default())
		want := 1
		if state == "disabled" {
			want = 0
		}
		if err != nil || len(ds.Data) != want || want == 1 && ds.Data[0].ID != fmt.Sprint(mainDonation) {
			t.Fatal(ds, err)
		}
	}
}

func TestBindingKeyStateHTTPValidationAndCursorScope(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'C')
	var firstDonation int64
	for i := 0; i < 4; i++ {
		did, kid, _ := e.seedCandidate(t, owner, byte('a'+i), "upstream")
		if i == 0 {
			firstDonation = did
		}
		if i >= 2 {
			if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE id=?`, kid); err != nil {
				t.Fatal(err)
			}
		}
	}
	api := httpAPI{service: e.service}
	request := func(route, query string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		r.SetPathValue("id", model.ID)
		r.SetPathValue("donationId", fmt.Sprint(firstDonation))
		w := httptest.NewRecorder()
		if route == "candidates" {
			api.candidates(w, r, roleAdmin, 0)
		} else {
			api.bindingSources(w, r, roleAdmin, 0, route == "keys")
		}
		return w
	}
	for _, route := range []string{"candidates", "donations", "keys"} {
		for _, paging := range []string{"limit=1", "page=1&page_size=10"} {
			for _, bad := range []string{"key_state=", "key_state=bad", "key_state=available&key_state=all", "key_state=Available"} {
				w := request(route, paging+"&"+bad)
				if w.Code != 400 {
					t.Fatalf("%s %s %s: %d %s", route, paging, bad, w.Code, w.Body)
				}
			}
		}
	}
	for _, route := range []string{"candidates", "donations"} {
		for _, state := range []string{"", "available"} {
			query := "limit=1"
			if state != "" {
				query += "&key_state=" + state
			}
			w := request(route, query)
			var page struct {
				NextCursor *string `json:"next_cursor"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.NextCursor == nil {
				t.Fatalf("first %s: %d %s %v", route, w.Code, w.Body, err)
			}
			cursor := "&cursor=" + url.QueryEscape(*page.NextCursor)
			if state == "" {
				query += "&key_state=all"
			}
			if got := request(route, query+cursor); got.Code != 200 {
				t.Fatalf("matching scope %s: %d %s", route, got.Code, got.Body)
			}
			if got := request(route, "limit=1&key_state=disabled"+cursor); got.Code != 400 {
				t.Fatalf("changed state %s: %d %s", route, got.Code, got.Body)
			}
		}
	}
}
