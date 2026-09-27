package logapi

import "testing"

func TestAccessRelativeWindowUsesServerClock(t *testing.T) {
	const now = int64(1800000000)
	f, err := parseAccessFilter("lookback_hours=168&page_size=100", now)
	if err != nil || f.To != now || f.From != now-7*86400 {
		t.Fatalf("unexpected window: %+v %v", f, err)
	}
	for _, raw := range []string{"lookback_hours=0", "lookback_hours=721", "lookback_hours=01", "lookback_hours=24&from=1", "lookback_hours=24&to=1800000000", "lookback_hours=24&lookback_hours=1"} {
		if _, err := parseAccessFilter(raw, now); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestNumberedAccessWatermarkRequiresFixedWindow(t *testing.T) {
	const now = int64(1800000000)
	valid := "page=2&page_size=20&from=1799990000&to=1800000000&watermark=9&expected_total=21"
	if _, err := parseAccessFilter(valid, now); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"watermark=9&from=1799990000&to=1800000000",
		"page=2&watermark=9",
		"page=2&from=1799990000&watermark=9",
		"page=2&expected_total=21",
	} {
		if _, err := parseAccessFilter(raw, now); err == nil {
			t.Fatalf("accepted unstable numbered selection %q", raw)
		}
	}
}
