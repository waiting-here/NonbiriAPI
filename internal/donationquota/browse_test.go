package donationquota

import (
	"context"
	"database/sql"
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
