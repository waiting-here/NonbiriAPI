package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestBrowsePredicateMatchesAdmissionAcrossReservationsAndCalendarWindows(t *testing.T) {
	cases := []struct {
		name, mode, interval, alignment, zone string
		week                                  *int
		at                                    int64
	}{
		{"first-five-hours", "reset", "5h", "first_success", "UTC", nil, testNow},
		{"first-hour", "reset", "1h", "first_success", "UTC", nil, testNow},
		{"spring-day", "reset", "day", "calendar", "America/New_York", nil, time.Date(2027, 3, 14, 6, 30, 0, 0, time.UTC).Unix()},
		{"natural-week", "reset", "week", "calendar", "Asia/Kolkata", ptr(7), testNow},
		{"natural-month", "reset", "month", "calendar", "Asia/Kolkata", nil, testNow},
		{"sliding-five-hours", "sliding", "5h", "", "UTC", nil, testNow},
		{"sliding-hour", "sliding", "1h", "", "UTC", nil, testNow},
		{"sliding-fold", "sliding", "day", "", "America/New_York", nil, time.Date(2027, 11, 7, 5, 30, 0, 0, time.UTC).Unix()},
		{"sliding-date-skip", "sliding", "week", "", "Pacific/Apia", nil, time.Date(2011, 12, 29, 12, 0, 0, 0, time.UTC).Unix()},
		{"sliding-month-end", "sliding", "month", "", "Asia/Kolkata", nil, time.Date(2027, 1, 31, 12, 0, 0, 0, time.UTC).Unix()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newQuotaDB(t)
			rules := []RuleInput{rule("calls", "2"), rule("tokens", "14"), rule("credits", "0.026")}
			for i := range rules {
				rules[i].Mode, rules[i].Interval, rules[i].TimeZone, rules[i].WeekStartsOn = tc.mode, tc.interval, tc.zone, tc.week
				if tc.alignment == "" {
					rules[i].Alignment = nil
				} else {
					rules[i].Alignment = ptr(tc.alignment)
				}
			}
			replace(t, q, tc.at, rules...)
			probe := Amounts{Calls: mag(1), Tokens: mag(7), Credits: mag(13)}
			assertBrowseAvailability(t, q, tc.at, probe, true)
			first, err := newClaim(t, q, tc.at, probe)
			if err != nil {
				t.Fatal(err)
			}
			assertBrowseAvailability(t, q, tc.at, probe, true)
			second, err := newClaim(t, q, tc.at, probe)
			if err != nil {
				t.Fatal(err)
			}
			assertBrowseAvailability(t, q, tc.at, probe, false)
			if err := dispatch(t, q, first, tc.at); err != nil {
				t.Fatal(err)
			}
			if err := dispatch(t, q, second, tc.at); err != nil {
				t.Fatal(err)
			}
			start(t, q, first, tc.at)
			terminal(t, q, first, tc.at+1, Amounts{Calls: mag(1), Tokens: mag(9), Credits: mag(15)}, true)
			terminal(t, q, second, tc.at+1, Amounts{}, false)
			assertBrowseAvailability(t, q, tc.at+1, probe, false)
			assertBrowseAvailability(t, q, tc.at+1, Amounts{Calls: mag(1), Tokens: mag(1), Credits: mag(1)}, true)
			assertBrowseAvailability(t, q, tc.at-100, probe, false)
			assertBrowseAvailability(t, q, tc.at+36*86400, probe, true)
		})
	}
}

func TestBrowsePredicateEmptyRetiredAndZeroLimits(t *testing.T) {
	q := newQuotaDB(t)
	probe := Amounts{Calls: mag(1), Tokens: mag(1), Credits: mag(0)}
	assertBrowseAvailability(t, q, testNow, probe, true)
	replace(t, q, testNow, rule("credits", "0"))
	assertBrowseAvailability(t, q, testNow, probe, false)
	replace(t, q, testNow+1)
	assertBrowseAvailability(t, q, testNow+1, probe, true)
	replace(t, q, testNow+2, rule("credits", "340282366920938463463374607431768211.455"))
	assertBrowseAvailability(t, q, testNow+2, probe, true)
}

func assertBrowseAvailability(t *testing.T, q *sql.DB, at int64, amounts Amounts, want bool) {
	t.Helper()
	tx, err := q.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := int64(0)
	if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	available, err := Available(context.Background(), tx, 1, at, amounts)
	if err != nil {
		t.Fatal(err)
	}
	var fromSet bool
	predicate := AvailabilityPredicate("dk.id", "?", amounts.Credits.Big().String(), amounts.Tokens.Big().String())
	if err := tx.QueryRow(`SELECT `+predicate+` FROM donation_keys dk WHERE dk.id=1`, at).Scan(&fromSet); err != nil {
		t.Fatal(err)
	}
	if available != want || fromSet != available {
		t.Fatalf("at=%s: SQL=%v admission=%v want=%v", strconv.FormatInt(at, 10), fromSet, available, want)
	}
	var after int64
	if err := tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatalf("browse wrote data: %d->%d, %v", before, after, err)
	}
}

func TestBrowseSlidingWindowProjectionRetainsReservationsAndWideLimits(t *testing.T) {
	q, now := newBrowseWindowFixture(t, 4, 1)
	probe := Amounts{Calls: mag(1)}
	for _, at := range []int64{now - 1, now, now + 60, now + 2*86400} {
		assertBrowseAvailability(t, q, at, probe, true)
	}
	if _, err := q.Exec(`UPDATE donation_quota_epochs SET limit_mag=nbi_u128(5),pending_reserved=nbi_u128(1)`); err != nil {
		t.Fatal(err)
	}
	assertBrowseAvailability(t, q, now, probe, false)
	assertBrowseAvailability(t, q, now+60, probe, false)
	assertBrowseAvailability(t, q, now+86400-2, probe, true)
	assertBrowseAvailability(t, q, now+2*86400, probe, true)
	if _, err := q.Exec(`UPDATE donation_quota_buckets SET used_mag=CASE WHEN success_at=? THEN X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF' ELSE zeroblob(16) END`, testNow+1); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(`UPDATE donation_quota_epochs SET limit_mag=X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF',window_used=X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF',pending_reserved=zeroblob(16)`); err != nil {
		t.Fatal(err)
	}
	assertBrowseAvailability(t, q, now, probe, false)
	assertBrowseAvailability(t, q, now+60, probe, false)
	assertBrowseAvailability(t, q, now+2*86400, probe, true)
}

func TestBrowseSlidingDeltaRejectsWideCounterDrift(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(strconv.FormatBool(overflow), func(t *testing.T) {
			q, now := newBrowseWindowFixture(t, 4, 1, true)
			at := now + 1
			if overflow {
				if _, err := q.Exec(`UPDATE donation_quota_buckets SET used_mag=CASE WHEN success_at=(SELECT MAX(success_at) FROM donation_quota_buckets) THEN X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF' ELSE zeroblob(16) END`); err != nil {
					t.Fatal(err)
				}
				if _, err := q.Exec(`UPDATE donation_quota_epochs SET window_used=X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF'`); err != nil {
					t.Fatal(err)
				}
				if _, err := q.Exec(`INSERT INTO donation_quota_buckets SELECT rule_id,epoch,?,nbi_u128(1),zeroblob(16) FROM donation_quota_epochs`, now+1); err != nil {
					t.Fatal(err)
				}
				at = now + 1
			} else {
				if _, err := q.Exec(`UPDATE donation_quota_epochs SET window_used=zeroblob(16)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Available(t.Context(), q, 1, at, Amounts{Calls: mag(1)}); !errors.Is(err, ErrInvariant) {
				t.Fatalf("runtime drift error: %v", err)
			}
			var available bool
			err := q.QueryRowContext(t.Context(), "SELECT "+AvailabilityPredicate("1", "?", "0", "0"), at).Scan(&available)
			if err == nil {
				t.Fatalf("browse accepted counter drift: %v", available)
			}
		})
	}
}

func TestBrowseModelSlidingWindowMatchesExactUpstream(t *testing.T) {
	q := newQuotaDB(t)
	r := rule("tokens", "3")
	r.Mode, r.Interval, r.Alignment = "sliding", "day", nil
	err := transaction(t, q, func(tx *sql.Tx) error {
		if err := ReplaceModel(t.Context(), tx, 1, testNow, "selected", []RuleInput{r}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO donation_quota_buckets SELECT id,1,?,nbi_u128(2),zeroblob(16) FROM donation_quota_rules WHERE upstream_model_id='selected'`, testNow+1); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE donation_quota_epochs SET window_left=?,window_at=?,last_observed_at=?,window_used=nbi_u128(2),window_reserved=zeroblob(16)`, testNow+1-86400, testNow+1, testNow+1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"selected", "unrelated"} {
		for _, at := range []int64{testNow + 1, testNow + 2, testNow + 61, testNow + 86402} {
			want, err := ModelAvailable(t.Context(), q, 1, at, model, TokenVector{Total: 2}.Amounts())
			if err != nil {
				t.Fatal(err)
			}
			var got bool
			err = q.QueryRowContext(t.Context(), "SELECT "+ModelAvailabilityPredicate("1", "?1", "?2", "0", "2"), model, at).Scan(&got)
			if err != nil || got != want {
				t.Fatalf("model=%s at=%d browse=%v runtime=%v err=%v", model, at, got, want, err)
			}
		}
	}
}

func TestBrowseStartedReservationLeavesAndReentersSlidingWindow(t *testing.T) {
	q := newQuotaDB(t)
	instant := func(text string) int64 {
		value, err := time.Parse(time.RFC3339, text)
		if err != nil {
			t.Fatal(err)
		}
		return value.Unix()
	}
	success := instant("2026-03-08T07:30:00Z")
	before := instant("2026-03-09T06:59:00Z")
	after := instant("2026-03-09T07:00:00Z")
	r := rule("tokens", "10")
	r.Mode, r.Interval, r.TimeZone, r.Alignment = "sliding", "day", "America/New_York", nil
	replace(t, q, success, r)
	reserved := Amounts{Calls: mag(1), Tokens: mag(7)}
	id, err := newClaim(t, q, success, reserved)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, success); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, success)
	requireUsage(t, views(t, q, success)[0], "0", "7", "3", "available")
	var nonzero bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM donation_quota_buckets b JOIN donation_quota_epochs e ON e.rule_id=b.rule_id AND e.epoch=b.epoch JOIN donation_quota_receipts r ON r.rule_id=b.rule_id AND r.epoch=b.epoch AND r.success_at=b.success_at WHERE r.claim_id=? AND r.state='started' AND b.reserved_mag=nbi_u128(7) AND e.window_reserved=nbi_u128(7))`, id).Scan(&nonzero); err != nil || !nonzero {
		t.Fatalf("missing live window reservation: %v %v", nonzero, err)
	}
	probe := Amounts{Tokens: mag(4)}
	for _, at := range []int64{success, success + 1, success + 60} {
		assertBrowseAvailability(t, q, at, probe, false)
	}
	advanceTo := func(at int64) {
		t.Helper()
		if err := transaction(t, q, func(tx *sql.Tx) error {
			epochs, err := currentEpochs(t.Context(), tx, 1)
			if err != nil {
				return err
			}
			return advance(t.Context(), tx, &epochs[0], at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertBrowseAvailability(t, q, before, probe, true)
	advanceTo(before)
	requireUsage(t, views(t, q, before)[0], "0", "0", "10", "available")
	// The retained started receipt reenters when the calendar left edge moves
	// backwards; no settlement has replaced its reservation with actual usage.
	assertBrowseAvailability(t, q, after, probe, false)
	advanceTo(after)
	requireUsage(t, views(t, q, after)[0], "0", "7", "3", "available")
	for _, at := range []int64{after + 1, after + 60} {
		assertBrowseAvailability(t, q, at, probe, false)
	}
	terminal(t, q, id, after+61, Amounts{Calls: mag(1), Tokens: mag(3)}, true)
	requireUsage(t, views(t, q, after+61)[0], "3", "0", "7", "available")
	assertBrowseAvailability(t, q, after+61, Amounts{Tokens: mag(7)}, true)
	assertBrowseAvailability(t, q, after+61, Amounts{Tokens: mag(8)}, false)
	var settled bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM donation_quota_receipts WHERE claim_id=? AND state='settled' AND actual_mag=nbi_u128(3) AND remaining_reserved_mag IS NULL) AND NOT EXISTS(SELECT 1 FROM donation_quota_buckets WHERE reserved_mag<>zeroblob(16)) AND NOT EXISTS(SELECT 1 FROM donation_quota_epochs WHERE window_reserved<>zeroblob(16) OR pending_reserved<>zeroblob(16))`, id).Scan(&settled); err != nil || !settled {
		t.Fatalf("settlement left a reservation: %v %v", settled, err)
	}
}

func TestBrowseModelConsumptionMatchesRuntime(t *testing.T) {
	q := newQuotaDB(t)
	for _, tc := range []struct {
		name, metric, limit string
		recurring           bool
		price               int64
		want                bool
	}{
		{"total-price-exact", "credits", "1.125", false, 1125, true},
		{"total-price-short", "credits", "1.125", false, 1126, false},
		{"free-closed", "credits", "0", false, 0, false},
		{"call-closed", "calls", "0", false, 0, false},
		{"sliding-price-short", "credits", "1.125", true, 1126, false},
		{"sliding-call-closed", "calls", "0", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := transaction(t, q, func(tx *sql.Tx) error {
				if tc.recurring {
					r := rule(tc.metric, tc.limit)
					r.Mode, r.Interval, r.Alignment = "sliding", "1h", nil
					return ReplaceModel(t.Context(), tx, 1, testNow, tc.name, []RuleInput{r})
				}
				limits := ModelLimits{}
				if tc.metric == "credits" {
					limits.Credits = &tc.limit
				} else {
					limits.Calls = &tc.limit
				}
				return SetModelLimits(t.Context(), tx, 1, testNow, tc.name, limits)
			}); err != nil {
				t.Fatal(err)
			}
			for _, model := range []string{tc.name, "unlimited"} {
				want := tc.want || model == "unlimited"
				runtime, err := ModelAvailable(t.Context(), q, 1, testNow, model, Amounts{Calls: mag(1), Credits: mag(tc.price)})
				if err != nil || runtime != want {
					t.Fatal(model, runtime, err)
				}
				var browse bool
				if err := q.QueryRow("SELECT "+ModelAvailabilityPredicate("1", "?1", "?2", "?3", "0"), model, testNow, tc.price).Scan(&browse); err != nil || browse != want {
					t.Fatal(model, browse, err)
				}
			}
		})
	}
}

func TestModelMaximumCreditBudgetRemainsLimited(t *testing.T) {
	q := newQuotaDB(t)
	_, err := q.Exec(`INSERT INTO donation_model_quotas(donation_key_id,upstream_model_id,created_at,price_limit_mag,price_used_mag,price_reserved_mag) VALUES(1,'max',?,X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF',X'FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF',nbi_u128(1))`, testNow)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ModelUsage(t.Context(), q, 1, "max")
	if err != nil || *v.Remaining.Credits != "0" || v.Used.Credits != "340282366920938463463374607431768211.455" {
		t.Fatal(v, err)
	}
	available, err := ModelAvailable(t.Context(), q, 1, testNow, "max", Amounts{Credits: mag(1), Calls: mag(1)})
	if err != nil || available {
		t.Fatal(available, err)
	}
	var listed bool
	if err = q.QueryRow("SELECT "+ModelAvailabilityPredicate("1", "'max'", "?", "1", "0"), testNow).Scan(&listed); err != nil || listed {
		t.Fatal(listed, err)
	}
}
