package donation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestSourceKeyStateFiltersMatchProjectionAndSummary(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "state-owner", nil, false)
	level := int64(6)
	steward := e.seedUser(t, "state-steward", &level, false)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	states := []string{"available", "disabled", "suspended", "exhausted", "rate_limited", "pending", "expired", "ended"}
	ids := map[string]string{}
	var until = e.clock.Load() + 60
	for i, state := range states {
		d, physical := createBrowseDonation(t, e, owner, byte('a'+i), "https://state.example.test/v1")
		ids[state] = d.ID
		key := parseTestID(t, d.Keys[0].ID)
		if state != "pending" {
			exec(`UPDATE donations SET status='approved' WHERE id=?`, d.ID)
		}
		// Lower-priority cooldown must never hide disabled/suspended/exhausted/lifecycle states.
		if state != "available" {
			exec(`INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at) VALUES(?,?,?)`, key, until, e.clock.Load())
		}
		switch state {
		case "disabled":
			exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, physical)
		case "suspended":
			caseID, err := db.GenerateOpaqueID("rpc_")
			if err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at) VALUES(?,?,'openai-compatible','https://state.example.test/v1','pending_review','complete',1,1,?,0,0,0,?)`, caseID, make([]byte, 32), until, e.clock.Load())
			exec(`INSERT INTO endpoint_key_suspensions(endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, physical, caseID, e.clock.Load())
		case "exhausted":
			exec(`UPDATE donation_keys SET input_token_limit_mag=nbi_u128(0),input_token_reserve=1,output_token_reserve=1 WHERE id=?`, key)
		case "expired":
			exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, e.clock.Load(), key)
		case "ended":
			exec(`UPDATE donations SET status='deleted',terminal_at=? WHERE id=?`, e.clock.Load(), d.ID)
			exec(`DELETE FROM donation_key_memberships WHERE donation_key_id=?`, key)
			exec(`UPDATE donation_keys SET enabled=0,ended_reason='terminated',ended_at=?,report_match_until=? WHERE id=?`, e.clock.Load(), e.clock.Load()+7776000, key)
		}
	}
	all, err := e.service.SourcesAdminPage(ctx, SourceFilter{Scope: "all"}, pagination.Default())
	if err != nil || len(all.Data) != 1 || all.Data[0].KeyCount != "8" {
		t.Fatalf("all=%+v %v", all, err)
	}
	source := all.Data[0].SourceKey
	for _, state := range states {
		filter := SourceFilter{Scope: "all", KeyState: state}
		sources, err := e.service.SourcesStewardPage(ctx, steward, filter, pagination.Default())
		if err != nil || len(sources.Data) != 1 || sources.Data[0].KeyCount != "1" || sources.Data[0].DonationCount != "1" {
			t.Fatalf("%s sources=%+v %v", state, sources, err)
		}
		keys, err := e.service.SourceKeysStewardPage(ctx, steward, source, filter, pagination.Default())
		if err != nil || len(keys.Data) != 1 || keys.Data[0].CharityState != state || keys.Data[0].DonationID != ids[state] {
			t.Fatalf("%s keys=%+v %v", state, keys, err)
		}
		summary, err := e.service.DonationsAdminPage(ctx, ManagementFilter{Query: ids[state]}, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range summary.Data {
			if item.ID == ids[state] {
				found = true
				if item.StateCounts[state] != "1" {
					t.Fatalf("%s summary=%+v", state, item)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s summary", state)
		}
	}
	for _, state := range []string{"expired", "ended"} {
		out, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{KeyState: state}, pagination.Default())
		if err != nil || out.Pagination.TotalItems != "0" {
			t.Fatalf("active %s=%+v %v", state, out, err)
		}
	}
	e.clock.Store(until)
	limited, err := e.service.SourcesAdminPage(ctx, SourceFilter{Scope: "all", KeyState: "rate_limited"}, pagination.Default())
	if err != nil || limited.Pagination.TotalItems != "0" {
		t.Fatalf("elapsed cooldown=%+v %v", limited, err)
	}
	available, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{KeyState: "available"}, pagination.Default())
	if err != nil || available.Pagination.TotalItems != "2" {
		t.Fatalf("expired cooldown available=%+v %v", available, err)
	}
}

func TestFailureSelectionKeyStateBoundsResetRangeAndCursor(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "state-reset-owner", nil, false)
	e.seedUser(t, "", nil, true)
	source := seedBrowseScale(t, e, owner, 205, false)
	if _, err := e.store.DB().Exec(`UPDATE donations SET status='approved'; UPDATE donation_keys SET enabled=CASE WHEN id%2=0 THEN 0 ELSE 1 END,failure_streak=nbi_u128(5)`); err != nil {
		t.Fatal(err)
	}
	api := httpAPI{service: e.service}
	selectPage := func(view, state string, cursor *string) (*httptest.ResponseRecorder, FailureResetSelection) {
		t.Helper()
		selection := map[string]any{"view": view, "key_state": state, "scope": "all", "q": "source-00000"}
		if view == "source_keys" {
			selection["source_key"] = source
			selection["idle"] = "yes"
		}
		body, _ := json.Marshal(map[string]any{"selection": selection, "cursor": cursor})
		w := resetHTTP(t, &api, reviewerAdmin, 0, routeAdminFailureReset+"/selection", string(body), "S", "", "")
		var out FailureResetSelection
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return w, out
	}
	for _, view := range []string{"sources", "source_keys"} {
		w, page := selectPage(view, "available", nil)
		if w.Code != 200 || len(page.Items) != 50 || page.NextCursor == nil {
			t.Fatalf("%s %d %+v %s", view, w.Code, page, w.Body)
		}
		for _, ref := range page.Items {
			if parseTestID(t, ref.KeyID)%2 == 0 {
				t.Fatal("disabled key selected", ref)
			}
		}
		if changed, _ := selectPage(view, "disabled", page.NextCursor); changed.Code != 400 {
			t.Fatal("state changed under cursor", changed.Code)
		}
		// The reset endpoint receives only the selected references. Other states retain their streak.
		if view == "source_keys" {
			body, _ := json.Marshal(map[string]any{"items": page.Items})
			w := resetHTTP(t, &api, reviewerAdmin, 0, routeAdminFailureReset, string(body), "T", "", "")
			var result FailureResetBatch
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Counts.Reset != "50" {
				t.Fatalf("reset %d %s %v", w.Code, w.Body, err)
			}
			var unchanged int
			if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_keys WHERE id%2=0 AND failure_streak=nbi_u128(5)`).Scan(&unchanged); err != nil || unchanged != 102 {
				t.Fatal("reset crossed state", unchanged, err)
			}
		}
		w, second := selectPage(view, "available", page.NextCursor)
		if w.Code != 200 || len(second.Items) != 50 || second.Items[0].KeyID != "101" || second.NextCursor == nil {
			t.Fatalf("second %s %d %+v", view, w.Code, second)
		}
		w, last := selectPage(view, "available", second.NextCursor)
		if w.Code != 200 || len(last.Items) != 3 || last.Items[2].KeyID != "205" || last.NextCursor != nil {
			t.Fatalf("last %s %d %+v", view, w.Code, last)
		}
	}
	// Adding the optional field does not invalidate cursors issued before it existed.
	selection := failureSelection{View: "sources", Source: SourceFilter{Scope: "all"}}
	old, err := e.service.selectFailureReset(context.Background(), reviewerAdmin, 0, selection, "")
	if err != nil || old.NextCursor == nil {
		t.Fatal(old, err)
	}
	selection.Source.KeyState = "all"
	if _, err := e.service.selectFailureReset(context.Background(), reviewerAdmin, 0, selection, *old.NextCursor); err != nil {
		t.Fatal("all cursor compatibility", err)
	}
	for _, body := range []string{
		`{"selection":{"view":"sources","key_state":""},"cursor":null}`,
		`{"selection":{"view":"sources","key_state":"bad"},"cursor":null}`,
		`{"selection":{"view":"sources","key_state":"available","key_state":"all"},"cursor":null}`,
		`{"selection":{"view":"donations","key_state":"all"},"cursor":null}`,
		`{"selection":{"view":"donation_keys","donation_id":"1","key_state":"all"},"cursor":null}`,
	} {
		if w := resetHTTP(t, &api, reviewerAdmin, 0, routeAdminFailureReset+"/selection", body, "U", "", ""); w.Code != 400 {
			t.Fatalf("invalid selector %s: %d %s", body, w.Code, w.Body)
		}
	}
}

func TestSourceKeyStateFiltersCountBeforeWindowAndCombineSelectors(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "many-state-owner", nil, false)
	for i := 0; i < 24; i++ {
		d, _ := createBrowseDonation(t, e, owner, byte('a'+i), fmt.Sprintf("https://source-%02d.example.test/v1", i))
		if _, err := e.store.DB().Exec(`UPDATE donations SET status='approved' WHERE id=?`, d.ID); err != nil {
			t.Fatal(err)
		}
		if i == 23 {
			if _, err := e.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE donation_id=?`, d.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, state := range []string{"", "all", "available"} {
		out, err := e.service.SourcesAdminPage(ctx, SourceFilter{KeyState: state, Query: "source-", Handling: "pending"}, pagination.Request{Page: 99, Size: 10})
		want := "24"
		if state == "available" {
			want = "23"
		}
		if err != nil || out.Pagination.TotalItems != want || out.Pagination.Page != "3" {
			t.Fatalf("%s=%+v %v", state, out, err)
		}
		if state == "available" && len(out.Data) != 3 {
			t.Fatal(out)
		}
	}
	sources, err := e.service.SourcesAdminPage(ctx, SourceFilter{KeyState: "disabled"}, pagination.Default())
	if err != nil || len(sources.Data) != 1 {
		t.Fatal(sources, err)
	}
	source := sources.Data[0].SourceKey
	for _, idle := range []string{"yes", "no"} {
		out, err := e.service.SourceKeysAdminPage(ctx, source, SourceFilter{KeyState: "disabled", Query: "source-23", Handling: "pending", Idle: idle}, pagination.Default())
		want := "1"
		if idle == "no" {
			want = "0"
		}
		if err != nil || out.Pagination.TotalItems != want {
			t.Fatalf("idle %s=%+v %v", idle, out, err)
		}
	}
	api := httpAPI{service: e.service}
	for _, keys := range []bool{false, true} {
		for _, query := range []string{"key_state=", "key_state=bad", "key_state=available&key_state=all", "key_state=Available", "key_state=available&cursor=x"} {
			r := httptest.NewRequest(http.MethodGet, "/sources?"+query, nil)
			r.SetPathValue("source_key", source)
			w := httptest.NewRecorder()
			api.sourcesNumbered(w, r, UserPrincipal{}, reviewerAdmin, keys)
			if w.Code != 400 {
				t.Fatalf("keys=%t %s: %d %s", keys, query, w.Code, w.Body)
			}
		}
	}
}
