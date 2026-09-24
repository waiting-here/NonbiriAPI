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
