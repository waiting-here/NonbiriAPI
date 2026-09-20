package antiabuse

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestPersonalPenaltyExportOwnProjectionAndIndependentExpiry(t *testing.T) {
	f := newAbuseFixture(t)
	user, other := f.user(), f.user()
	f.set(KeyCharityViolationDeductMilli, "1000")
	f.set(KeyCharityViolationBanSeconds, "10")
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]p/m", 1); err != nil {
		t.Fatal(err)
	}
	start := f.clock.Load()
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	data, err := ExportTx(context.Background(), tx, user, start, 100)
	if err != nil || len(data) != 2 || data[0].Actions[0].RequestID == nil {
		t.Fatal(data, err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"rules", "statistics", "threshold", "count", "evidence", "actor_user_id", "content_chars", "window"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatal("unsafe owner projection", forbidden)
		}
	}
	data, err = ExportTx(context.Background(), tx, other, start, 100)
	if err != nil || len(data) != 0 {
		t.Fatal("cross-owner export", data, err)
	}
	if _, err = ExportTx(context.Background(), tx, user, start, 1); !errors.Is(err, ErrExportTooLarge) {
		t.Fatal("export truncated", err)
	}
	data, err = ExportTx(context.Background(), tx, user, start+31*86400, 100)
	if err != nil || len(data) != 2 {
		t.Fatal(data, err)
	}
	for _, c := range data {
		if c.Actions[0].RequestID != nil {
			t.Fatal("expired log link", c)
		}
		if c.Kind == "ban" && (c.EndedAt == nil || *c.EndedAt != start+10) {
			t.Fatal("delayed actual end", c)
		}
	}
	data, err = ExportTx(context.Background(), tx, user, start+10+RetentionSeconds, 100)
	if err != nil || len(data) != 0 {
		t.Fatal("expired export", data, err)
	}
	if err := DeleteTx(context.Background(), tx, user); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"abuse_cases", "abuse_actions", "abuse_evidence", "abuse_windows", "abuse_window_events"} {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestLongWindowSurvivesPenaltyAndRequestRetention(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	f.set(KeyRPMBanThreshold, "2")
	f.set(KeyRPMBanDurationSeconds, "1")
	f.set(KeyRPMBanWindowSeconds, "315360000")
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(100 * 86400)
	if err := f.service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.restart()
	if f.service.events != 1 {
		t.Fatal("long window followed log retention")
	}
	if err := f.service.RPMDenied(context.Background(), user, ratelimit.RPMUserLimit); err != nil {
		t.Fatal(err)
	}
	if f.bans.Load() != 1 || f.scalar(`SELECT count(*) FROM abuse_evidence`) != 2 {
		t.Fatal("restored long window did not trigger")
	}
}
