package donationquota

import (
	"testing"
)

func TestHourlyReplacementRetainsDispatchedEpoch(t *testing.T) {
	q := newQuotaDB(t)
	previous := replace(t, q, testNow, rule("calls", "2"))[0].RuleInput
	amounts := Amounts{Calls: mag(1)}
	old, err := newClaim(t, q, testNow, amounts)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, old, testNow); err != nil {
		t.Fatal(err)
	}
	previous.Interval = "1h"
	replace(t, q, testNow+1, previous)
	// A late first response keeps the five-hour epoch selected at dispatch.
	start(t, q, old, testNow+2)
	terminal(t, q, old, testNow+3, amounts, true)
	current, err := newClaim(t, q, testNow+4, amounts)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, current, testNow+4); err != nil {
		t.Fatal(err)
	}
	start(t, q, current, testNow+5)
	terminal(t, q, current, testNow+6, amounts, true)
	for _, tc := range []struct {
		claim             string
		epoch, start, end int64
	}{
		{old, 1, testNow + 2, testNow + 18002},
		{current, 2, testNow + 5, testNow + 3605},
	} {
		var epoch, start, end int64
		if err := q.QueryRow(`SELECT r.epoch,p.start_at,p.end_at FROM donation_quota_receipts r JOIN donation_quota_periods p
ON p.rule_id=r.rule_id AND p.epoch=r.epoch AND p.start_at=r.period_start WHERE r.claim_id=?`, tc.claim).Scan(&epoch, &start, &end); err != nil || epoch != tc.epoch || start != tc.start || end != tc.end {
			t.Fatalf("epoch assignment = %d %d %d, want %+v: %v", epoch, start, end, tc, err)
		}
	}
	requireUsage(t, views(t, q, testNow+6)[0], "1", "0", "1", "available")
}

func TestHourlyRuleValidation(t *testing.T) {
	r := rule("calls", "1")
	r.Interval = "1h"
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.Alignment = ptr("calendar")
	if Validate(r) == nil {
		t.Fatal("hourly calendar alignment accepted")
	}
	r.Mode, r.Alignment = "sliding", nil
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.WeekStartsOn = ptr(1)
	if Validate(r) == nil {
		t.Fatal("hourly week start accepted")
	}
}
