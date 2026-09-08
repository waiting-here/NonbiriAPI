package calendar

import (
	"testing"
	"time"
)

func TestCalendarZonesIncludesCommonAndUTC(t *testing.T) {
	zones := Zones()
	if len(zones) < 100 {
		t.Fatalf("zone list too short: %d", len(zones))
	}
	want := []string{"UTC", "America/New_York", "Asia/Shanghai", "Asia/Kolkata", "Australia/Adelaide"}
	for _, z := range want {
		found := false
		for _, actual := range zones {
			if actual == z {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Zones() missing %q", z)
		}
	}
}

func TestCalendarResolveNormal(t *testing.T) {
	r, err := Resolve("2026-06-15T12:00:00", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 15, 4, 0, 0, 0, time.UTC).Unix()
	if r.Instant != want {
		t.Fatalf("instant = %d, want %d", r.Instant, want)
	}
	if r.OffsetSeconds != 8*3600 {
		t.Fatalf("offset = %d, want 28800", r.OffsetSeconds)
	}
	if r.Adjustment != AdjustmentNone || r.Local != "2026-06-15T12:00:00" {
		t.Fatalf("adjustment/local = %q/%q, want none/2026-06-15T12:00:00", r.Adjustment, r.Local)
	}
}

func TestCalendarResolveHalfHourOffset(t *testing.T) {
	r, err := Resolve("2026-06-15T12:00:00", "Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 15, 6, 30, 0, 0, time.UTC).Unix()
	if r.Instant != want {
		t.Fatalf("instant = %d, want %d", r.Instant, want)
	}
	if r.OffsetSeconds != 5*3600+30*60 {
		t.Fatalf("offset = %d, want 19800", r.OffsetSeconds)
	}
	if r.Adjustment != AdjustmentNone {
		t.Fatalf("adjustment = %q, want none", r.Adjustment)
	}
}

func TestCalendarResolveUTC(t *testing.T) {
	r, err := Resolve("2026-06-15T12:00:00", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC).Unix()
	if r.Instant != want || r.OffsetSeconds != 0 || r.Adjustment != AdjustmentNone {
		t.Fatalf("resolve UTC = %+v, want instant=%d offset=0 none", r, want)
	}
}

// America/New_York spring forward 2026-03-08 02:00->03:00. 02:30 does not
// exist; it shifts forward to 03:30 EDT (07:30Z).
func TestCalendarResolveGapShifted(t *testing.T) {
	r, err := Resolve("2026-03-08T02:30:00", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC).Unix()
	if r.Instant != want {
		t.Fatalf("gap instant = %d, want %d (07:30Z)", r.Instant, want)
	}
	if r.OffsetSeconds != -4*3600 {
		t.Fatalf("gap offset = %d, want -14400 (EDT)", r.OffsetSeconds)
	}
	if r.Adjustment != AdjustmentGapShifted {
		t.Fatalf("adjustment = %q, want gap_shifted", r.Adjustment)
	}
	if r.Local != "2026-03-08T03:30:00" {
		t.Fatalf("gap local = %q, want 2026-03-08T03:30:00", r.Local)
	}
}

// America/New_York fall back 2026-11-01 02:00->01:00. 01:30 repeats; the
// later instant (EST, 06:30Z) is chosen.
func TestCalendarResolveFoldLater(t *testing.T) {
	r, err := Resolve("2026-11-01T01:30:00", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC).Unix()
	if r.Instant != want {
		t.Fatalf("fold instant = %d, want %d (06:30Z EST)", r.Instant, want)
	}
	if r.OffsetSeconds != -5*3600 {
		t.Fatalf("fold offset = %d, want -18000 (EST)", r.OffsetSeconds)
	}
	if r.Adjustment != AdjustmentFoldLater {
		t.Fatalf("adjustment = %q, want fold_later", r.Adjustment)
	}
	if r.Local != "2026-11-01T01:30:00" {
		t.Fatalf("fold local = %q, want 2026-11-01T01:30:00", r.Local)
	}
}

func TestCalendarResolveRejectsInvalid(t *testing.T) {
	if _, err := Resolve("bad", "UTC"); err == nil {
		t.Fatal("accepted malformed local")
	}
	if _, err := Resolve("2026-06-15T12:00:00", "Not/A/Zone"); err == nil {
		t.Fatal("accepted unknown zone")
	}
	if _, err := Resolve("2026-13-40T12:00:00", "UTC"); err == nil {
		t.Fatal("accepted impossible month/day")
	}
}
