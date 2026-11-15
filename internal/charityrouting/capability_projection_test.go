package charityrouting

import "testing"

func TestCapabilityKeepsReturnedStatisticsAndCurrentAccess(t *testing.T) {
	env := newBrowseProjectionFixture(t, 4, 1)
	check := func(want int, state string) {
		t.Helper()
		got, err := env.service.Capability(t.Context(), env.caller, routingTestNow)
		if err != nil || len(got.Models) != want || got.State != state {
			t.Fatalf("count=%d state=%s err=%v", len(got.Models), got.State, err)
		}
		for _, model := range got.Models {
			rate := model.RecentSuccess
			if rate.Success != 100 || rate.Failure != 100 || rate.Cancelled != 100 || rate.SampleCount != 200 || rate.Rate == nil || *rate.Rate != 0.5 {
				t.Fatalf("model %s statistics=%+v", model.ID, rate)
			}
		}
	}
	check(3, "available")
	if _, err := env.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=CASE WHEN model_id=2 THEN 63 ELSE 0 END`); err != nil {
		t.Fatal(err)
	}
	check(1, "available")
	if _, err := env.store.DB().Exec(`UPDATE donation_keys SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	check(0, "no_candidates")
	if _, err := env.store.DB().Exec(`UPDATE charity_models SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	check(0, "no_models")
}
