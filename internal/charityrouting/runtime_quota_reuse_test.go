package charityrouting

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func quotaSnapshotFixture(t *testing.T, candidates, distinct int) (*routingTestEnv, int64, []int64) {
	t.Helper()
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	modelID, err := parsePositiveID(model.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]int64, distinct)
	endpointKeys := make([]int64, distinct)
	for i := range distinct {
		_, keys[i], endpointKeys[i] = e.seedCandidateWithConnector(t, owner, fmt.Sprintf("quota-%d", i), fmt.Sprintf("quota-%d", i), connectorcontract.TypeOpenAICompatible)
	}
	selections := make([]BindingSelection, candidates)
	for i := range candidates {
		k := i % distinct
		upstream := fmt.Sprintf("quota-%d", i)
		if i >= distinct {
			if _, err := e.store.DB().Exec(`INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?1,?2,?2,1,?3,?3)`, keys[k], upstream, routingTestNow); err != nil {
				t.Fatal(err)
			}
		}
		selections[i] = BindingSelection{DonationKeyID: fmt.Sprint(keys[k]), UpstreamModelID: upstream}
	}
	if _, err := e.service.AddBindingsAdmin(t.Context(), modelID, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, selections), BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	return e, modelID, keys
}

func TestRuntimeQuotaReuseRemainsTransactionLocal(t *testing.T) {
	e, modelID, keys := quotaSnapshotFixture(t, 3, 2)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want map[int64]int, wantErr error) {
		t.Helper()
		snapshot, err := e.service.chatSnapshot(t.Context(), modelID, routingTestNow, []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
		if !errors.Is(err, wantErr) {
			t.Fatalf("snapshot error=%v, want %v", err, wantErr)
		}
		if wantErr != nil {
			return
		}
		counts := make(map[int64]int)
		bindings := make(map[int64]bool)
		upstreams := make(map[string]bool)
		for _, c := range snapshot.Candidates() {
			counts[c.DonationKeyID]++
			if bindings[c.BindingID] || upstreams[c.UpstreamModelID] {
				t.Fatal("candidate identity was reused")
			}
			bindings[c.BindingID], upstreams[c.UpstreamModelID] = true, true
		}
		if len(counts) != len(want) {
			t.Fatalf("candidate counts=%v, want %v", counts, want)
		}
		for key, n := range want {
			if counts[key] != n {
				t.Fatalf("candidate counts=%v, want %v", counts, want)
			}
		}
	}
	replace := func(key int64, limit string) {
		t.Helper()
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		alignment := "first_success"
		var rules []donationquota.RuleInput
		if limit != "" {
			rules = []donationquota.RuleInput{{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: "calls", Limit: limit}}
		}
		if err = donationquota.Replace(t.Context(), tx, key, routingTestNow, rules); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	both := map[int64]int{keys[0]: 2, keys[1]: 1}
	check(both, nil)
	exec("UPDATE donation_keys SET input_token_reserve=4,output_token_reserve=6,input_token_limit_mag=nbi_u128(0) WHERE id=?", keys[0])
	check(map[int64]int{keys[1]: 1}, nil)
	exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(4) WHERE id=?", keys[0])
	check(both, nil)
	replace(keys[0], "0")
	check(map[int64]int{keys[1]: 1}, nil)
	replace(keys[1], "0")
	check(nil, donationquota.ErrLimited)
	replace(keys[0], "1")
	check(map[int64]int{keys[0]: 2}, nil)
	replace(keys[1], "1")
	exec("UPDATE donation_keys SET call_limit_mag=nbi_u128(0) WHERE id=?", keys[0])
	check(map[int64]int{keys[1]: 1}, nil)
	exec("UPDATE donation_keys SET call_limit_mag=NULL WHERE id=?", keys[0])
	check(both, nil)
	// A non-null current reference must keep the full epoch validation,
	// including a corrupt reference to an already-retired epoch.
	exec("UPDATE donation_quota_epochs SET retired_at=? WHERE (rule_id,epoch) IN (SELECT id,current_epoch FROM donation_quota_rules WHERE donation_key_id=?)", routingTestNow, keys[0])
	check(nil, donationquota.ErrInvariant)
	// Budget rejection still precedes the later epoch-validation stage.
	exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(0) WHERE id=?", keys[0])
	check(map[int64]int{keys[1]: 1}, nil)
	exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(4) WHERE id=?", keys[0])
	exec("UPDATE donation_quota_epochs SET retired_at=NULL WHERE (rule_id,epoch) IN (SELECT id,current_epoch FROM donation_quota_rules WHERE donation_key_id=?)", keys[0])
	check(both, nil)
	replace(keys[0], "")
	replace(keys[1], "")
	var historical, current int
	if err := e.store.DB().QueryRow("SELECT COUNT(*),COUNT(current_epoch) FROM donation_quota_rules").Scan(&historical, &current); err != nil || historical == 0 || current != 0 {
		t.Fatalf("retired rules: historical=%d current=%d err=%v", historical, current, err)
	}
	check(both, nil)
	// No configured limit does not excuse an overflowing token budget.
	exec("UPDATE donation_keys SET input_token_limit_mag=NULL,input_tokens_used=X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF' WHERE id=?", keys[0])
	check(nil, donationquota.ErrInvariant)
	exec("UPDATE donation_keys SET input_tokens_used=zeroblob(16) WHERE id=?", keys[0])
	check(both, nil)
}
