package charityrouting

import (
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func TestModelQuotasCatalogAndRuntimeFilterOnlyExactUpstream(t *testing.T) {
	e, modelID, keys := quotaSnapshotFixture(t, 2, 1)
	zero := "0"
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = donationquota.SetModelLimits(t.Context(), tx, keys[0], routingTestNow, "quota-0", donationquota.ModelLimits{Tokens: &zero}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		snapshot, err := e.service.chatSnapshot(t.Context(), modelID, routingTestNow, []contract.Type{contract.TypeOpenAICompatible})
		if err != nil {
			t.Fatal(err)
		}
		candidates := snapshot.Candidates()
		if len(candidates) != want {
			t.Fatal(candidates)
		}
		if want == 1 && candidates[0].UpstreamModelID != "quota-1" {
			t.Fatal(candidates)
		}
		var available bool
		query := `WITH cx AS (SELECT ? AS decision_now,5 AS token_reserve,1 AS charity_enabled) SELECT ` + catalogAvailableSQL() + ` FROM charity_models cm CROSS JOIN cx WHERE cm.id=?`
		if err = e.store.DB().QueryRow(query, routingTestNow, modelID).Scan(&available); err != nil || !available {
			t.Fatal(available, err)
		}
	}
	check(1)
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = donationquota.SetModelLimits(t.Context(), tx, keys[0], routingTestNow, "quota-0", donationquota.ModelLimits{}); err != nil {
		t.Fatal(err)
	}
	alignment := "first_success"
	if err = donationquota.ReplaceModel(t.Context(), tx, keys[0], routingTestNow, "quota-0", []donationquota.RuleInput{{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: "tokens", Limit: "0"}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check(1)
	tx, err = e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = donationquota.ReplaceModel(t.Context(), tx, keys[0], routingTestNow, "quota-0", nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check(2)
}
