package calendar

import (
	"testing"
	"time"
)

func recurrenceInstant(value string) int64 {
	t, _ := time.Parse(time.RFC3339, value)
	return t.Unix()
}

func TestExactRecurrenceOriginalCalendarAnchor(t *testing.T) {
	tests := []struct {
		name, anchor, interval, zone, after string
		want                                []string
		adjustment                          string
	}{
		{"utc-month", "2026-10-27T03:00:01", "month", "UTC", "2026-10-02T00:00:00Z", []string{"2026-10-27T03:00:01", "2026-11-27T03:00:01", "2026-12-27T03:00:01"}, AdjustmentNone},
		{"month-end", "2027-01-31T03:00:01", "month", "UTC", "2027-01-31T03:00:01Z", []string{"2027-02-28T03:00:01", "2027-03-31T03:00:01", "2027-04-30T03:00:01"}, AdjustmentNone},
		{"leap-month", "2028-01-31T03:00:01", "month", "UTC", "2028-01-31T03:00:01Z", []string{"2028-02-29T03:00:01", "2028-03-31T03:00:01", "2028-04-30T03:00:01"}, AdjustmentNone},
		{"daily-gap", "2027-03-13T02:30:01", "day", "America/New_York", "2027-03-13T07:30:01Z", []string{"2027-03-14T03:30:01", "2027-03-15T02:30:01", "2027-03-16T02:30:01"}, AdjustmentGapShifted},
		{"weekly-fold", "2027-10-31T01:30:01", "week", "America/New_York", "2027-10-31T05:30:01Z", []string{"2027-11-07T01:30:01", "2027-11-14T01:30:01", "2027-11-21T01:30:01"}, AdjustmentFoldLater},
		{"skipped-day", "2011-12-29T12:00:00", "day", "Pacific/Apia", "2011-12-29T22:00:00Z", []string{"2011-12-31T12:00:00", "2012-01-01T12:00:00", "2012-01-02T12:00:00"}, AdjustmentGapShifted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			transitions, err := NextRecurrences(tc.anchor, tc.interval, tc.zone, recurrenceInstant(tc.after))
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range tc.want {
				if transitions[i].Local != want {
					t.Fatalf("transition %d=%+v, want %s", i, transitions[i], want)
				}
			}
			if transitions[0].Adjustment != tc.adjustment {
				t.Fatalf("adjustment=%s", transitions[0].Adjustment)
			}
			if tc.name == "weekly-fold" && transitions[0].OffsetSeconds != -18000 {
				t.Fatal("fold selected earlier clock")
			}
		})
	}
}

func TestExactRecurrencePartialAndBoundary(t *testing.T) {
	effective := recurrenceInstant("2026-10-02T00:00:00Z")
	first := recurrenceInstant("2026-10-27T03:00:01Z")
	for _, interval := range []string{"day", "week", "month"} {
		partial, err := RecurrencePeriod(first-1, effective, "2026-10-27T03:00:01", interval, "UTC")
		if err != nil || partial != (Period{effective, first}) {
			t.Fatalf("partial=%+v err=%v", partial, err)
		}
		boundary, err := RecurrencePeriod(first, effective, "2026-10-27T03:00:01", interval, "UTC")
		if err != nil || boundary.Start != first || boundary.End <= first {
			t.Fatalf("boundary=%+v err=%v", boundary, err)
		}
	}
	for _, anchor := range []string{"2026-02-30T03:00:01", "1960-01-01T00:00:00", "2026-10-27T03:00"} {
		if _, err := RecurrencePeriod(first, effective, anchor, "day", "UTC"); err == nil {
			t.Fatalf("accepted %s", anchor)
		}
	}
}
