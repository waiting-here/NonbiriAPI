package charityrouting

import (
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBrowseQuotaFastPathPreservesModelPriceAndTokenLimits(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	cheap := catalogCreate(t, env, 'a', "cheap", nil, true)
	dear := catalogCreate(t, env, 'b', "dear", nil, true)
	cheapID := catalogBind(t, env, cheap, key, 'c')
	dearID := catalogBind(t, env, dear, key, 'd')
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE charity_models SET request_user_price=2000 WHERE id=?", cheapID)
	exec("UPDATE charity_models SET request_user_price=4000 WHERE id=?", dearID)
	exec("UPDATE donation_keys SET price_limit_mag=nbi_u128(3000) WHERE id=?", key)
	check := func(label string, wantCheap bool) {
		t.Helper()
		t.Run(label, func(t *testing.T) {
			models, err := env.service.ListAvailableModels(t.Context(), env.caller, env.clock.Load(), MaxAvailableModels)
			count := 0
			if wantCheap {
				count = 1
			}
			if err != nil || len(models) != count || (count == 1 && models[0].ModelID != cheapID) {
				t.Fatalf("public models=%+v err=%v", models, err)
			}
			capability, err := env.service.Capability(t.Context(), env.caller, env.clock.Load())
			if err != nil || len(capability.Models) != count || (count == 1 && capability.Models[0].ID != fmt.Sprint(cheapID)) {
				t.Fatalf("capability=%+v err=%v", capability, err)
			}
			catalog, err := env.service.Catalog(t.Context(), env.caller, CatalogFilter{}, pagination.Default())
			if err != nil || len(catalog.Models) != 2 {
				t.Fatalf("catalog=%+v err=%v", catalog, err)
			}
			for _, model := range catalog.Models {
				if model.CurrentlyAvailable != (model.ID == fmt.Sprint(cheapID) && wantCheap) {
					t.Fatalf("model price/limit conflated: %+v", model)
				}
			}
		})
	}
	check("no additional quota still uses each model price", true)
	exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(0),input_token_reserve=1,output_token_reserve=1 WHERE id=?", key)
	check("zero input limit is not unlimited", false)
	exec("UPDATE donation_keys SET input_token_limit_mag=nbi_u128(1),output_token_limit_mag=nbi_u128(0) WHERE id=?", key)
	check("zero output limit is not unlimited", false)
	exec("UPDATE donation_keys SET output_token_limit_mag=nbi_u128(1) WHERE id=?", key)
	check("both token dimensions fit exactly", true)
	exec("UPDATE donation_keys SET input_tokens_reserved=nbi_u128(1) WHERE id=?", key)
	check("input reservations consume remaining", false)
	exec("UPDATE donation_keys SET input_tokens_reserved=nbi_u128(0),input_token_limit_mag=NULL,output_token_limit_mag=NULL,token_limit_mag=nbi_u128(1) WHERE id=?", key)
	check("no independent limits keeps combined token reserve", false)
	exec("UPDATE donation_keys SET token_limit_mag=nbi_u128(2) WHERE id=?", key)
	check("combined token reserve fits exactly", true)
	exec("UPDATE donation_keys SET input_token_reserve=NULL,output_token_reserve=NULL,token_limit_mag=nbi_u128(9) WHERE id=?", key)
	check("legacy reserve still applies", false)
	exec("UPDATE donation_keys SET token_limit_mag=nbi_u128(10),call_limit_mag=nbi_u128(0) WHERE id=?", key)
	check("cumulative call limit still applies", false)
	exec("UPDATE donation_keys SET call_limit_mag=NULL WHERE id=?", key)
	check("legacy reserve fits exactly", true)
	for _, limit := range []string{"1.999", "2"} {
		tx, err := env.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		alignment := "first_success"
		err = donationquota.Replace(t.Context(), tx, key, env.clock.Load(), []donationquota.RuleInput{{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: "credits", Limit: limit}})
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		check("recurring credit quota "+limit, limit == "2")
	}
	exec("UPDATE donation_quota_epochs SET retired_at=? WHERE (rule_id,epoch) IN (SELECT id,current_epoch FROM donation_quota_rules WHERE donation_key_id=? AND current_epoch IS NOT NULL)", env.clock.Load(), key)
	check("retired current epoch retains original rejection", false)
	exec("UPDATE donation_quota_epochs SET retired_at=NULL WHERE (rule_id,epoch) IN (SELECT id,current_epoch FROM donation_quota_rules WHERE donation_key_id=? AND current_epoch IS NOT NULL)", key)
	tx, err := env.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = donationquota.Replace(t.Context(), tx, key, env.clock.Load(), nil); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var historical, current int
	if err = env.store.DB().QueryRow("SELECT COUNT(*),COUNT(current_epoch) FROM donation_quota_rules WHERE donation_key_id=?", key).Scan(&historical, &current); err != nil || historical == 0 || current != 0 {
		t.Fatalf("retired rule fixture: historical=%d current=%d err=%v", historical, current, err)
	}
	check("historical null epochs do not restrict availability", true)

}
