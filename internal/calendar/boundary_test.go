package calendar

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustInstant(t *testing.T, text string) int64 {
	t.Helper()
	value, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return value.Unix()
}

func TestResolveRealTransitionSizes(t *testing.T) {
	for _, tc := range []struct{ zone, input, local, utc, adjustment string }{
		{"Europe/Berlin", "2026-03-29T02:30:23", "2026-03-29T03:30:23", "2026-03-29T01:30:23Z", AdjustmentGapShifted},
		{"Australia/Lord_Howe", "2026-10-04T02:15:23", "2026-10-04T02:45:23", "2026-10-03T15:45:23Z", AdjustmentGapShifted},
		{"Australia/Lord_Howe", "2026-04-05T01:45:23", "2026-04-05T01:45:23", "2026-04-04T15:15:23Z", AdjustmentFoldLater},
		{"Pacific/Apia", "2011-12-30T12:34:56", "2011-12-31T12:34:56", "2011-12-30T22:34:56Z", AdjustmentGapShifted},
		{"Pacific/Kiritimati", "1994-12-31T12:34:56", "1995-01-01T12:34:56", "1994-12-31T22:34:56Z", AdjustmentGapShifted},
		{"Asia/Kathmandu", "2026-06-15T12:00:01", "2026-06-15T12:00:01", "2026-06-15T06:15:01Z", AdjustmentNone},
		{"America/Indiana/Winamac", "2007-03-11T03:00:00", "2007-03-11T05:00:00", "2007-03-11T09:00:00Z", AdjustmentGapShifted},
		{"America/Godthab", "2026-06-15T12:00:00", "2026-06-15T12:00:00", "2026-06-15T13:00:00Z", AdjustmentNone},
	} {
		t.Run(tc.zone+tc.input, func(t *testing.T) {
			result, err := Resolve(tc.input, tc.zone)
			if err != nil || result.Instant != mustInstant(t, tc.utc) || result.Local != tc.local || result.Adjustment != tc.adjustment {
				t.Fatalf("resolve = %+v, %v; want %s %s %s", result, err, tc.utc, tc.local, tc.adjustment)
			}
		})
	}
}

func TestCalendarShiftClampAndActualHours(t *testing.T) {
	for _, tc := range []struct {
		zone, from, interval, to string
		subtract                 bool
	}{
		{"UTC", "2026-01-31T10:00:23Z", "month", "2026-02-28T10:00:23Z", false},
		{"UTC", "2024-01-31T10:00:23Z", "month", "2024-02-29T10:00:23Z", false},
		{"UTC", "2026-03-31T10:00:23Z", "month", "2026-02-28T10:00:23Z", true},
		{"UTC", "2026-02-28T10:00:23Z", "month", "2026-03-28T10:00:23Z", false},
		{"America/New_York", "2026-03-07T17:00:23Z", "day", "2026-03-08T16:00:23Z", false},
		{"America/New_York", "2026-10-31T16:00:23Z", "day", "2026-11-01T17:00:23Z", false},
		{"America/New_York", "2026-03-08T06:00:23Z", "5h", "2026-03-08T11:00:23Z", false},
		{"America/New_York", "2026-03-08T06:30:23Z", "1h", "2026-03-08T07:30:23Z", false},
		{"America/New_York", "2026-11-01T05:30:23Z", "1h", "2026-11-01T06:30:23Z", false},
		{"America/New_York", "2026-11-01T06:30:23Z", "1h", "2026-11-01T05:30:23Z", true},
		{"Australia/Lord_Howe", "2026-10-03T15:15:23Z", "1h", "2026-10-03T16:15:23Z", false},
		{"Pacific/Apia", "2011-12-30T09:30:23Z", "1h", "2011-12-30T10:30:23Z", false},
		{"UTC", "1970-01-01T00:00:00Z", "1h", "1969-12-31T23:00:00Z", true},
		{"America/New_York", "2026-03-15T16:00:23Z", "week", "2026-03-08T16:00:23Z", true},
		{"Pacific/Apia", "2011-12-29T22:34:56Z", "day", "2011-12-30T22:34:56Z", false},
		{"UTC", "1970-01-01T00:00:00Z", "day", "1969-12-31T00:00:00Z", true},
	} {
		fn := Add
		if tc.subtract {
			fn = Subtract
		}
		result, err := fn(mustInstant(t, tc.from), tc.interval, tc.zone)
		if err != nil || result != mustInstant(t, tc.to) {
			t.Errorf("shift %+v = %d, %v", tc, result, err)
		}
	}
	if _, err := Add(MaxInstant, "day", "UTC"); err == nil {
		t.Fatal("accepted overflowing end")
	}
	if _, err := Subtract(0, "5h", "Local"); err == nil {
		t.Fatal("accepted host Local")
	}
}

func TestNaturalPeriodsUseCalendarAnchors(t *testing.T) {
	for _, tc := range []struct {
		zone, now, interval, start, end string
		weekday                         int
	}{
		{"America/New_York", "2026-03-08T12:00:00Z", "day", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", 0},
		{"America/New_York", "2026-11-01T12:00:00Z", "day", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", 0},
		{"Pacific/Apia", "2011-12-30T10:00:00Z", "day", "2011-12-30T10:00:00Z", "2011-12-31T10:00:00Z", 0},
		{"Pacific/Apia", "2011-12-30T09:59:59Z", "day", "2011-12-29T10:00:00Z", "2011-12-30T10:00:00Z", 0},
		{"UTC", "2026-03-08T12:00:00Z", "week", "2026-03-02T00:00:00Z", "2026-03-09T00:00:00Z", 1},
		{"UTC", "2026-03-08T12:00:00Z", "week", "2026-03-08T00:00:00Z", "2026-03-15T00:00:00Z", 7},
		{"Asia/Shanghai", "2026-02-01T00:00:00Z", "month", "2026-01-31T16:00:00Z", "2026-02-28T16:00:00Z", 0},
	} {
		period, err := NaturalPeriod(mustInstant(t, tc.now), tc.interval, tc.zone, tc.weekday)
		if err != nil || period.Start != mustInstant(t, tc.start) || period.End != mustInstant(t, tc.end) {
			t.Errorf("period %+v = %+v, %v", tc, period, err)
		}
	}
	for _, tc := range []struct {
		interval string
		day      int
	}{{"1h", 0}, {"5h", 0}, {"week", 0}, {"week", 8}, {"day", 1}, {"unknown", 0}} {
		if _, err := NaturalPeriod(0, tc.interval, "UTC", tc.day); err == nil {
			t.Errorf("accepted invalid natural interval %+v", tc)
		}
	}
}

func TestRegistryNamesAreFixedAndReturnedByValue(t *testing.T) {
	for _, name := range []string{"UTC", "CET", "EST5EDT", "NZ", "Etc/GMT+5", "US/Eastern"} {
		if !ValidZone(name) {
			t.Errorf("missing embedded alias %s", name)
		}
	}
	for _, name := range []string{"Local", "", "../UTC", "/etc/localtime", "C:\\zoneinfo", "utc"} {
		if ValidZone(name) {
			t.Errorf("accepted unregistered zone %s", name)
		}
		if _, err := Resolve("2026-06-15T12:00:00", name); err == nil {
			t.Errorf("resolved unregistered zone %s", name)
		}
	}
	first := Zones()
	if !slices.IsSorted(first) {
		t.Fatal("zone names not sorted")
	}
	first[0] = "tampered"
	if slices.Contains(Zones(), "tampered") {
		t.Fatal("caller mutated the registry")
	}
}

func TestRegistryIgnoresHostZoneArchive(t *testing.T) {
	if os.Getenv("CALENDAR_HOST_PROBE") == "1" {
		result, err := Resolve("2026-06-15T12:00:00", "America/New_York")
		if err != nil || result.OffsetSeconds != -4*3600 {
			t.Fatalf("host archive changed resolution: %+v %v", result, err)
		}
		return
	}
	archive, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip)))
	if err != nil {
		t.Fatal(err)
	}
	utc, err := archive.Open("UTC")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(utc)
	if err != nil {
		t.Fatal(err)
	}
	if err := utc.Close(); err != nil {
		t.Fatal(err)
	}
	var forged bytes.Buffer
	writer := zip.NewWriter(&forged)
	entry, err := writer.Create("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "zoneinfo.zip")
	if err := os.WriteFile(path, forged.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"UTC", "Asia/Tokyo"} {
		command := exec.Command(os.Args[0], "-test.run=^TestRegistryIgnoresHostZoneArchive$")
		command.Env = append(os.Environ(), "CALENDAR_HOST_PROBE=1", "ZONEINFO="+path, "TZ="+zone)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("host %s: %v\n%s", zone, err, output)
		}
	}
}

// Independently check every POSIX continuation's offsets against the loaded
// registry. This covers all future years regardless of transition dates.
func TestPinnedContinuationOffsetsAndLookbackBound(t *testing.T) {
	pattern := regexp.MustCompile(`^(?:<[^>]+>|[A-Za-z]{3,})([+-]?\d+(?::\d+(?::\d+)?)?)(?:(?:<[^>]+>|[A-Za-z]{3,})([+-]?\d+(?::\d+(?::\d+)?)?)?(,.*)?)?$`)
	archive, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		zone, err := lookupZone(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if data[4] == 0 {
			continue
		}
		parts := bytes.Split(data, []byte{'\n'})
		footer := string(parts[len(parts)-2])
		if footer == "" {
			continue
		}
		matches := pattern.FindStringSubmatch(footer)
		if matches == nil {
			t.Fatalf("unsupported continuation %s: %s", file.Name, footer)
		}
		parseOffset := func(value string) int {
			sign := -1
			if strings.HasPrefix(value, "-") {
				sign = 1
			}
			value = strings.TrimLeft(value, "+-")
			seconds := 0
			for index, part := range strings.Split(value, ":") {
				n, err := strconv.Atoi(part)
				if err != nil {
					t.Fatal(err)
				}
				seconds += n * []int{3600, 60, 1}[index]
			}
			return sign * seconds
		}
		standard := parseOffset(matches[1])
		offsets := []int{standard}
		if matches[3] != "" {
			dst := standard + 3600
			if matches[2] != "" {
				dst = parseOffset(matches[2])
			}
			offsets = append(offsets, dst)
		}
		for _, offset := range offsets {
			if !slices.Contains(zone.offsets, offset) {
				t.Fatalf("%s continuation offset %d absent from types", file.Name, offset)
			}
			if offset <= -86400 || offset >= 86400 {
				t.Fatalf("offset exceeds lookback proof: %s", file.Name)
			}
		}
	}
	if int64(31*86400+2*86400) >= MaxLookbackSeconds {
		t.Fatal("retention proof no longer fits")
	}
}

func TestAllRegistryTransitionsAndCalendarWindowBounds(t *testing.T) {
	until := time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)
	transitions := 0
	for name, zone := range embeddedRegistry().zones {
		cursor := time.Date(1840, 1, 1, 0, 0, 0, 0, time.UTC).In(zone.location)
		for steps := 0; cursor.Before(until); steps++ {
			if steps > 3000 {
				t.Fatalf("unbounded transition iteration: %s", name)
			}
			_, end := cursor.ZoneBounds()
			if end.IsZero() || !end.Before(until) {
				break
			}
			if !end.After(cursor) {
				// Go's POSIX continuation returns a nominal 365-day year end
				// after the final transition, including during leap years.
				cursor = time.Date(cursor.UTC().Year()+1, 1, 1, 0, 0, 0, 0, time.UTC).In(zone.location)
				continue
			}
			_, before := end.Add(-time.Second).Zone()
			_, after := end.Zone()
			cursor = end
			if before == after {
				continue
			}
			transitions++
			if after-before > 86400 || before-after > 86400 {
				t.Fatalf("transition larger than a day: %s", name)
			}
			wall := time.Unix(end.Unix()+int64(min(before, after))+int64(absOffset(after-before)/2), 0).UTC()
			result, err := Resolve(wall.Format(wallLayout), name)
			wantAdjustment := AdjustmentGapShifted
			if after < before {
				wantAdjustment = AdjustmentFoldLater
			}
			want := end.Unix() + int64(absOffset(after-before)/2)
			if err != nil || result.Instant != want || result.Adjustment != wantAdjustment {
				t.Fatalf("%s at %s (%d -> %d): %+v %v, want %d %s", name, end, before, after, result, err, want, wantAdjustment)
			}
			if end.Unix() < 1 {
				continue
			}
			for _, now := range []int64{end.Unix() - 1, end.Unix(), end.Unix() + 1} {
				left, err := Subtract(now, "month", name)
				if err != nil || left > now || now-left > MaxLookbackSeconds {
					t.Fatalf("%s month left at %d = %d %v", name, now, left, err)
				}
				period, err := NaturalPeriod(now, "day", name, 0)
				if err != nil || period.Start > now || period.End <= now || period.Start >= period.End {
					t.Fatalf("%s natural day at %d = %+v %v", name, now, period, err)
				}
			}
		}
	}
	if transitions < 10000 {
		t.Fatalf("unexpected transition coverage: %d", transitions)
	}
	t.Logf("checked %d transitions across %d embedded zones", transitions, len(Zones()))
}

func absOffset(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestFutureNewYorkTransitions(t *testing.T) {
	for _, year := range []int{2100, 2400, 9998} {
		marchSunday := 8 + (7-int(time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC).Weekday()))%7
		input := fmt.Sprintf("%04d-03-%02dT02:30:00", year, marchSunday)
		result, err := Resolve(input, "America/New_York")
		want := time.Date(year, 3, marchSunday, 7, 30, 0, 0, time.UTC).Unix()
		if err != nil || result.Instant != want || result.Adjustment != AdjustmentGapShifted {
			t.Fatalf("future %s = %+v %v", input, result, err)
		}
	}
}

func TestSlidingLeftCanMoveBackAndReadmitRetainedFacts(t *testing.T) {
	before, err := Subtract(mustInstant(t, "2026-11-01T05:59:59Z"), "day", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	after, err := Subtract(mustInstant(t, "2026-11-01T06:00:00Z"), "day", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	fact := mustInstant(t, "2026-10-31T05:30:00Z")
	if before != mustInstant(t, "2026-10-31T05:59:59Z") || after != mustInstant(t, "2026-10-31T05:00:00Z") || fact > before || fact <= after {
		t.Fatalf("fall-back must let retained fact reenter: before=%d after=%d fact=%d", before, after, fact)
	}
}

func TestStrictLocalClockAndRange(t *testing.T) {
	for _, input := range []string{"2026-02-29T00:00:00", "2026-06-15 12:00:00", "2026-06-15T24:00:00", "2026-06-15T12:60:00", "2026-06-15T12:00:60", "2026-06-15T12:00", "2026-06-15T12:00:00Z", "2026-06-15T12:00:00.0", "２０２６-06-15T12:00:00"} {
		if _, err := Resolve(input, "UTC"); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	for _, input := range []struct {
		local   string
		instant int64
	}{{"0000-01-01T00:00:00", MinInternalInstant}, {"1970-01-01T00:00:00", 0}, {"9999-12-31T23:59:59", MaxInstant}} {
		result, err := Resolve(input.local, "UTC")
		if err != nil || result.Instant != input.instant {
			t.Errorf("boundary %+v = %+v, %v", input, result, err)
		}
	}
}
