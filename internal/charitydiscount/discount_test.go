package charitydiscount

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
)

func instant(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Unix()
}

func schedule(zone string, fallback int, periods ...Period) Config {
	return Config{Enabled: true, TimeZone: zone, DefaultPercent: fallback, Periods: periods}
}

func requireEffective(t *testing.T, fixed Fixed, recurring Config, at, source string, percent int, next string) {
	t.Helper()
	now := instant(t, at)
	got, err := Evaluate(fixed, recurring, now)
	if err != nil {
		t.Fatal(err)
	}
	var expectedNext *int64
	if next != "" {
		expectedNext = new(instant(t, next))
	}
	if got.Source != source || got.Percent != percent || !reflect.DeepEqual(got.NextChangeAt, expectedNext) {
		t.Fatalf("at %s: got %+v next=%v, want source=%s percent=%d next=%v", at, got, got.NextChangeAt, source, percent, expectedNext)
	}
	if value, err := PercentAt(fixed, recurring, now); err != nil || value != percent {
		t.Fatalf("billing percent=%d error=%v, want %d", value, err, percent)
	}
}

func TestFixedSwitchTakesPrecedenceOutsideWindow(t *testing.T) {
	recurring := schedule("UTC", 50)
	start, end := instant(t, "2026-10-20T09:00:00Z"), instant(t, "2026-10-20T12:00:00Z")
	fixed := Fixed{Enabled: true, Percent: 20, StartAt: &start, EndAt: &end}
	requireEffective(t, fixed, recurring, "2026-10-20T08:59:59Z", "fixed", 100, "2026-10-20T09:00:00Z")
	requireEffective(t, fixed, recurring, "2026-10-20T09:00:00Z", "fixed", 20, "2026-10-20T12:00:00Z")
	requireEffective(t, fixed, recurring, "2026-10-20T12:00:00Z", "fixed", 100, "")
	fixed.Enabled = false
	requireEffective(t, fixed, recurring, "2026-10-20T12:00:00Z", "recurring", 50, "")
	fixed.Enabled, fixed.Percent = true, 100
	requireEffective(t, fixed, recurring, "2026-10-20T08:00:00Z", "fixed", 100, "")
	fixed.Percent, fixed.EndAt = 0, &start
	requireEffective(t, fixed, recurring, "2026-10-20T08:00:00Z", "fixed", 100, "")
	fixed.StartAt, fixed.EndAt = nil, nil
	requireEffective(t, fixed, recurring, "2026-10-20T08:00:00Z", "fixed", 0, "")
	requireEffective(t, Fixed{}, Default(), "2026-10-20T08:00:00Z", "none", 100, "")
}

func TestWeeklyPricesAndActualChanges(t *testing.T) {
	config := schedule("Asia/Shanghai", 50,
		Period{[]int{1, 2, 3, 4, 5}, "09:00", "12:00", 100},
		Period{[]int{1, 2, 3, 4, 5}, "14:00", "18:00", 100})
	requireEffective(t, Fixed{}, config, "2026-10-20T00:59:59Z", "recurring", 50, "2026-10-20T01:00:00Z")
	requireEffective(t, Fixed{}, config, "2026-10-20T01:00:00Z", "recurring", 100, "2026-10-20T04:00:00Z")
	requireEffective(t, Fixed{}, config, "2026-10-20T04:00:00Z", "recurring", 50, "2026-10-20T06:00:00Z")
	requireEffective(t, Fixed{}, config, "2026-10-23T10:00:00Z", "recurring", 50, "2026-10-26T01:00:00Z")
	config = schedule("UTC", 100, Period{[]int{7}, "22:00", "02:00", 0})
	requireEffective(t, Fixed{}, config, "2026-10-25T21:59:59Z", "recurring", 100, "2026-10-25T22:00:00Z")
	requireEffective(t, Fixed{}, config, "2026-10-26T00:00:00Z", "recurring", 0, "2026-10-26T02:00:00Z")
	requireEffective(t, Fixed{}, config, "2026-10-26T02:00:00Z", "recurring", 100, "2026-11-01T22:00:00Z")
	config = schedule("UTC", 100, Period{[]int{1}, "22:00", "00:00", 40})
	requireEffective(t, Fixed{}, config, "2026-10-26T23:00:00Z", "recurring", 40, "2026-10-27T00:00:00Z")
	config = schedule("UTC", 100, Period{[]int{1}, "10:00", "11:00", 50}, Period{[]int{1}, "11:00", "12:00", 50})
	requireEffective(t, Fixed{}, config, "2026-10-26T10:30:00Z", "recurring", 50, "2026-10-26T12:00:00Z")
	config = schedule("UTC", 100, Period{[]int{1, 2, 3, 4, 5, 6, 7}, "00:00", "24:00", 50})
	requireEffective(t, Fixed{}, config, "2026-10-26T10:30:00Z", "recurring", 50, "")
}

func TestDSTUsesEveryActualWallClock(t *testing.T) {
	fold := schedule("America/New_York", 100, Period{[]int{7}, "01:30", "02:30", 50})
	for _, test := range []struct {
		at, next string
		percent  int
	}{
		{"2026-11-01T05:29:59Z", "2026-11-01T05:30:00Z", 100},
		{"2026-11-01T05:30:00Z", "2026-11-01T06:00:00Z", 50},
		{"2026-11-01T06:00:00Z", "2026-11-01T06:30:00Z", 100},
		{"2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z", 50},
	} {
		requireEffective(t, Fixed{}, fold, test.at, "recurring", test.percent, test.next)
	}
	gap := schedule("America/New_York", 100, Period{[]int{7}, "02:30", "03:30", 50})
	requireEffective(t, Fixed{}, gap, "2026-03-08T06:59:59Z", "recurring", 100, "2026-03-08T07:00:00Z")
	requireEffective(t, Fixed{}, gap, "2026-03-08T07:00:00Z", "recurring", 50, "2026-03-08T07:30:00Z")
	gap.Periods[0].StartTime, gap.Periods[0].EndTime = "02:00", "03:00"
	requireEffective(t, Fixed{}, gap, "2026-03-08T06:00:00Z", "recurring", 100, "2026-03-15T06:00:00Z")

	halfHour := schedule("Australia/Lord_Howe", 100, Period{[]int{7}, "01:45", "02:15", 50})
	requireEffective(t, Fixed{}, halfHour, "2026-04-04T14:45:00Z", "recurring", 50, "2026-04-04T15:00:00Z")
	requireEffective(t, Fixed{}, halfHour, "2026-04-04T15:00:00Z", "recurring", 100, "2026-04-04T15:15:00Z")
	requireEffective(t, Fixed{}, halfHour, "2026-04-04T15:15:00Z", "recurring", 50, "2026-04-04T15:45:00Z")
	halfHour.Periods[0].StartTime, halfHour.Periods[0].EndTime = "02:15", "02:45"
	requireEffective(t, Fixed{}, halfHour, "2026-10-03T15:29:59Z", "recurring", 100, "2026-10-03T15:30:00Z")
	requireEffective(t, Fixed{}, halfHour, "2026-10-03T15:30:00Z", "recurring", 50, "2026-10-03T15:45:00Z")

	skippedDate := schedule("Pacific/Apia", 100, Period{[]int{5}, "12:00", "13:00", 50})
	requireEffective(t, Fixed{}, skippedDate, "2011-12-29T21:00:00Z", "recurring", 100, "2012-01-05T22:00:00Z")
}

func TestNormalizationAndStrictReplacement(t *testing.T) {
	input := schedule("UTC", 50, Period{[]int{5, 1, 3}, "22:00", "00:00", 0})
	normalized, err := Normalize(input)
	if err != nil || !reflect.DeepEqual(normalized.Periods[0].Weekdays, []int{1, 3, 5}) {
		t.Fatalf("normalize=%+v err=%v", normalized, err)
	}
	normalized.Periods[0].Weekdays[0] = 2
	if input.Periods[0].Weekdays[0] != 5 {
		t.Fatal("normalization changed caller storage")
	}
	encoded, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(encoded)
	if err != nil || got.Periods[0].Percent != 0 || got.Periods[0].Weekdays[0] != 1 {
		t.Fatalf("round trip=%+v err=%v", got, err)
	}
	base, _ := Encode(Default())
	for _, invalid := range []string{
		"null", "{}", strings.Replace(base, `"enabled":false,`, "", 1),
		strings.Replace(base, `"enabled":false`, `"enabled":null`, 1),
		strings.Replace(base, `"enabled":false`, `"enabled":false,"enabled":true`, 1),
		strings.Replace(base, `"enabled":false`, `"Enabled":false`, 1),
		strings.Replace(base, `"periods":[]`, `"periods":null`, 1),
		strings.Replace(base, `"periods":[]`, `"periods":[null]`, 1),
		strings.Replace(base, `"periods":[]`, `"periods":[{"weekdays":[1],"start_time":"00:00","end_time":"01:00"}]`, 1),
		strings.Replace(base, `"periods":[]`, `"periods":[],"extra":true`, 1),
		strings.Replace(base, `"default_percent":100`, `"default_percent":1e2`, 1),
		strings.Repeat(" ", MaxBytes) + base,
	} {
		if _, err := Decode(invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid JSON %q: %v", invalid, err)
		}
	}
}

func TestScheduleValidationBoundsAndOverlap(t *testing.T) {
	valid := schedule("UTC", 50)
	for hour := range MaxPeriods {
		valid.Periods = append(valid.Periods, Period{[]int{1, 2, 3, 4, 5, 6, 7}, fmt.Sprintf("%02d:00", hour), fmt.Sprintf("%02d:00", hour+1), 100})
	}
	encoded, err := Encode(valid)
	if err != nil || len(encoded) > MaxBytes {
		t.Fatalf("legal full schedule size=%d error=%v", len(encoded), err)
	}
	var decoded Config
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	tests := []Config{
		schedule("Local", 50), schedule("../UTC", 50), schedule("UTC", -1), schedule("UTC", 101),
		schedule("UTC", 50, Period{[]int{1}, "00:00", "00:00", 50}),
		schedule("UTC", 50, Period{[]int{1}, "24:00", "01:00", 50}),
		schedule("UTC", 50, Period{[]int{1}, "1:00", "02:00", 50}),
		schedule("UTC", 50, Period{[]int{1}, "01:60", "02:00", 50}),
		schedule("UTC", 50, Period{[]int{0}, "01:00", "02:00", 50}),
		schedule("UTC", 50, Period{[]int{1, 1}, "01:00", "02:00", 50}),
		schedule("UTC", 50, Period{nil, "01:00", "02:00", 50}),
		schedule("UTC", 50, Period{[]int{7}, "22:00", "02:00", 50}, Period{[]int{1}, "01:00", "03:00", 50}),
		schedule("UTC", 50, Period{[]int{1}, "01:00", "04:00", 50}, Period{[]int{1}, "02:00", "03:00", 50}),
	}
	tooMany := valid.Clone()
	tooMany.Periods = append(tooMany.Periods, Period{[]int{1}, "16:00", "17:00", 100})
	tests = append(tests, tooMany)
	for _, input := range tests {
		input.Enabled = false
		if _, err := Normalize(input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid disabled configuration %+v: %v", input, err)
		}
	}
}

func TestInstantBounds(t *testing.T) {
	config := schedule("UTC", 50, Period{[]int{1}, "00:00", "01:00", 0})
	for _, at := range []int64{-1, calendar.MaxInstant + 1} {
		if _, err := Evaluate(Fixed{}, config, at); !errors.Is(err, ErrInvalid) {
			t.Fatalf("instant %d: %v", at, err)
		}
	}
	got, err := Evaluate(Fixed{}, config, calendar.MaxInstant)
	if err != nil || got.NextChangeAt != nil {
		t.Fatalf("last instant=%+v error=%v", got, err)
	}
}
