package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestExactEntryRequiresFutureResetWhileExistingAnchorEditsRemainUsable(t *testing.T) {
	q := newQuotaDB(t)
	first := time.Date(2026, 10, 27, 3, 0, 1, 0, time.UTC).Unix()
	r := rule("calls", "2")
	r.Interval, r.Alignment, r.AnchorLocal = "month", ptr("exact_time"), ptr("2026-10-27T03:00:01")
	for _, now := range []int64{first, first + 1} {
		err := transaction(t, q, func(tx *sql.Tx) error { return Replace(context.Background(), tx, 1, now, []RuleInput{r}) })
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("new rule accepted current/past first reset: %v", err)
		}
	}
	if len(views(t, q, first+1)) != 0 {
		t.Fatal("failed entry left a rule behind")
	}
	old := replace(t, q, first, rule("calls", "2"))[0].RuleInput
	r.ID = old.ID
	for _, now := range []int64{first, first + 1} {
		err := transaction(t, q, func(tx *sql.Tx) error { return Replace(context.Background(), tx, 1, now, []RuleInput{r}) })
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("alignment entry accepted current/past reset: %v", err)
		}
	}
	r.AnchorLocal = ptr("2026-10-27T03:00:02")
	v := replace(t, q, first, r)[0]
	r = v.RuleInput
	r.Limit = "3"
	replace(t, q, first+10, r)
	var epoch int
	if err := q.QueryRow("SELECT current_epoch FROM donation_quota_rules WHERE id=?", *r.ID).Scan(&epoch); err != nil || epoch != 2 {
		t.Fatalf("ordinary edit changed epoch=%d: %v", epoch, err)
	}
	r.Interval, r.TimeZone = "week", "Europe/Berlin"
	v = replace(t, q, first+11, r)[0]
	if *v.AnchorLocal != "2026-10-27T03:00:02" {
		t.Fatal("calendar edit required anchor replacement")
	}
	if err := q.QueryRow("SELECT current_epoch FROM donation_quota_rules WHERE id=?", *r.ID).Scan(&epoch); err != nil || epoch != 3 {
		t.Fatalf("structural calendar edit epoch=%d: %v", epoch, err)
	}
}

func TestExactAnchorSurvivesDatabaseReopen(t *testing.T) {
	q := newQuotaDB(t)
	r := rule("calls", "2")
	r.Interval, r.Alignment, r.AnchorLocal = "month", ptr("exact_time"), ptr("2027-01-31T03:00:01")
	now := time.Date(2027, 1, 31, 3, 0, 1, 0, time.UTC).Unix()
	replace(t, q, now-1, r)
	var sequence int
	var name, path string
	if err := q.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, at := range []struct{ now, end time.Time }{
		{time.Date(2027, 2, 28, 3, 0, 1, 0, time.UTC), time.Date(2027, 3, 31, 3, 0, 1, 0, time.UTC)},
		{time.Date(2027, 3, 31, 3, 0, 1, 0, time.UTC), time.Date(2027, 4, 30, 3, 0, 1, 0, time.UTC)},
	} {
		v := views(t, reopened, at.now.Unix())[0]
		if *v.AnchorLocal != "2027-01-31T03:00:01" || *v.PeriodEnd != at.end.Unix() {
			t.Fatalf("reopened=%+v", v)
		}
	}
}

func TestExactResetPartialBoundaryEditsAndOldEpochSettlement(t *testing.T) {
	q := newQuotaDB(t)
	first := time.Date(2026, 10, 27, 3, 0, 1, 0, time.UTC).Unix()
	effective := first - 86400
	r := rule("calls", "3")
	r.Interval, r.Alignment, r.AnchorLocal = "month", ptr("exact_time"), ptr("2026-10-27T03:00:01")
	v := replace(t, q, effective, r)[0]
	if *v.PeriodStart != effective || *v.PeriodEnd != first || v.State != "available" {
		t.Fatalf("initial=%+v", v)
	}
	amounts := Amounts{Calls: mag(1)}
	old, err := newClaim(t, q, first-2, amounts)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, old, first-2); err != nil {
		t.Fatal(err)
	}
	start(t, q, old, first-1)
	v = views(t, q, first-1)[0]
	requireUsage(t, v, "1", "0", "2", "available")
	r = v.RuleInput
	r.Limit = "4"
	v = replace(t, q, first-1, r)[0]
	if *v.AnchorLocal != "2026-10-27T03:00:01" {
		t.Fatal("limit edit lost anchor")
	}
	requireUsage(t, v, "1", "0", "3", "available")
	v = views(t, q, first)[0]
	if *v.PeriodStart != first || *v.PeriodEnd != time.Date(2026, 11, 27, 3, 0, 1, 0, time.UTC).Unix() {
		t.Fatalf("boundary=%+v", v)
	}
	requireUsage(t, v, "0", "0", "4", "available")
	late, err := newClaim(t, q, first+1, amounts)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, late, first+1); err != nil {
		t.Fatal(err)
	}
	r.AnchorLocal = ptr("2026-10-28T03:00:01")
	replace(t, q, first+2, r)
	start(t, q, late, first+3)
	terminal(t, q, late, first+4, amounts, true)
	terminal(t, q, old, first+4, amounts, true)
	v = views(t, q, first+4)[0]
	requireUsage(t, v, "0", "0", "4", "available")
	var epoch int
	if err := q.QueryRow("SELECT epoch FROM donation_quota_receipts WHERE claim_id=?", late).Scan(&epoch); err != nil || epoch != 1 {
		t.Fatalf("old epoch=%d %v", epoch, err)
	}
}

func TestExactRuleValidation(t *testing.T) {
	r := rule("calls", "1")
	r.Interval, r.Alignment, r.AnchorLocal = "day", ptr("exact_time"), ptr("2026-10-27T03:00:01")
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*RuleInput){
		func(r *RuleInput) { r.AnchorLocal = nil }, func(r *RuleInput) { r.Interval = "1h" }, func(r *RuleInput) { r.WeekStartsOn = ptr(1) },
		func(r *RuleInput) { r.Alignment = ptr("calendar") }, func(r *RuleInput) { r.AnchorLocal = ptr("2026-02-30T00:00:00") },
	} {
		bad := r
		edit(&bad)
		if Validate(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
