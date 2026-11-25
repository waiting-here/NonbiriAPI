package duel_test

import (
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/finished_bidding.sql.txt
var finishedBiddingSQL string

func finishedChallengeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newUnrecoveredFixture(t, "bidding")
	loadHistoricalFixture(t, f, finishedBiddingSQL)
	f.clock.Store(200)
	if _, err := f.s.RecoverBeforeListenAt(f.ctx, 200, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestHistoricalChallengeSettlementExportAndOwnership(t *testing.T) {
	f := finishedChallengeFixture(t)
	page, err := f.s.History(f.ctx, f.identity(0), duel.PageInput{Mode: "ai", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	r := page.Items[0]
	if r.Outcome != "win" || r.AI == nil || !r.AI.FirstClear || r.AI.Reward != "2" {
		t.Fatal(r)
	}
	details, err := f.s.HistoryDetail(f.ctx, f.identity(0), r.ID)
	if err != nil || len(details.Sources) == 0 {
		t.Fatal(details, err)
	}
	if _, err := f.s.HistoryDetail(f.ctx, f.identity(1), r.ID); !errors.Is(err, duel.ErrNotFound) {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, _, err := f.s.ExportTx(f.ctx, tx, f.users[0], f.clock.Load(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	exported := raw.(duel.Export).AI
	if exported == nil || len(exported.Clears) != 1 || len(exported.Memories) != 1 || len(exported.Snapshots) != 1 {
		t.Fatalf("legacy export: %+v", exported)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"random_seed", "definition_json", "decision_id"} {
		if strings.Contains(string(data), private) {
			t.Fatal(private)
		}
	}
}
