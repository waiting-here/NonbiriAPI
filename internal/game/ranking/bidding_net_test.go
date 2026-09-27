package ranking

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func resetBiddingRebuild(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM game_rank_totals WHERE board='bidding_net_profit'`,
		`DELETE FROM game_bidding_net_rebuild_events`,
		`DELETE FROM game_bidding_net_rebuild_totals`,
		`UPDATE game_bidding_net_rebuild SET state='pending',watermark=NULL,last_seq=zeroblob(16),history_coverage_start=NULL,missing_events=0,updated_at=0 WHERE id=1`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) advanceBidding(now int64) {
	f.t.Helper()
	for range 100 {
		ready := false
		f.tx(func(tx *sql.Tx) error {
			var err error
			ready, err = advanceBiddingTx(context.Background(), tx, now)
			return err
		})
		if ready {
			return
		}
	}
	f.t.Fatal("bidding rebuild did not complete")
}

func TestBiddingNetUsesActualReturnsAndLossesWithoutChangingLegacyProfit(t *testing.T) {
	f := newFixture(t)
	u, loser := f.user(true), f.user(false)
	e := f.epoch
	f.add(u, e, -1000, 8000, "bidding")
	f.add(u, e+1, 300, 0, "bidding")
	f.add(u, e+2, -200, 400, "bidding")
	f.add(loser, e+2, 100, 700, "bidding")
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "0" {
		t.Fatal("unpublished total", got)
	}
	status := f.read(u, biddingBoard, "7d", 1, e+2)
	if status.RebuildStatus != "scanning" || len(status.Rows) != 0 {
		t.Fatal("partial board exposed", status)
	}
	f.advanceBidding(e + 2)
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "900" {
		t.Fatal(got)
	}
	if got, _, _ := f.total(u, "bidding", "7d"); got != "8400" {
		t.Fatal("legacy board changed", got)
	}
	board := f.read(u, biddingBoard, "7d", 1, e+2)
	if board.RebuildStatus != "completed" || len(board.Rows) != 1 || board.Rows[0].Amount != "0.9" || board.Rows[0].Identity.Kind != "public" || board.MissingEvents == nil || *board.MissingEvents != "0" {
		t.Fatal(board)
	}
	f.advance(e + week)
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "-100" {
		t.Fatal("rolling boundary", got)
	}
	if rows := f.read(u, biddingBoard, "7d", 1, e+week).Rows; len(rows) != 0 {
		t.Fatal(rows)
	}
	f.advance(e + week + 2)
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "0" {
		t.Fatal(got)
	}
}

func TestBiddingBackfillCheckpointBufferCoverageAndDelete(t *testing.T) {
	f := newFixture(t)
	u, removed := f.user(false), f.user(true)
	e := f.epoch
	for i := range 1100 {
		f.add(u, e+int64(i%3), -1, 2, "bidding")
	}
	f.add(removed, e+1, -40, 50, "bidding")
	// An old event can retain a positive-profit history record after its
	// loss facts disappear. That loss is an explicit coverage gap, not zero.
	f.add(u, e+1, 10, 20, "bidding")
	if _, err := f.db.Exec(`UPDATE game_rank_events SET loss_sign=NULL,loss_mag=NULL,charity_expires_at=NULL WHERE user_id=? AND source_id=?`, u, fmt.Sprintf("source_%d", f.serial)); err != nil {
		t.Fatal(err)
	}
	resetBiddingRebuild(t, f.db)
	f.tx(func(tx *sql.Tx) error {
		ready, err := advanceBiddingTx(context.Background(), tx, e+2)
		if ready || err != nil {
			t.Fatalf("first batch ready=%v err=%v", ready, err)
		}
		return nil
	})
	var state string
	var last []byte
	if err := f.db.QueryRow(`SELECT state,last_seq FROM game_bidding_net_rebuild WHERE id=1`).Scan(&state, &last); err != nil || state != "scanning" || len(last) != 16 || string(last) == string(make([]byte, 16)) {
		t.Fatal(state, last, err)
	}
	// This source is newer than the fixed watermark and must enter the buffer.
	f.add(u, e+3, -7, 0, "bidding")
	if _, err := f.db.Exec(`DELETE FROM users WHERE id=?`, removed); err != nil {
		t.Fatal(err)
	}
	f.advanceBidding(e + 3)
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "1107" {
		t.Fatal("backfill or buffer double count", got)
	}
	if got, _, _ := f.total(removed, biddingBoard, "7d"); got != "0" {
		t.Fatal("deleted user revived", got)
	}
	board := f.read(u, biddingBoard, "7d", 1, e+3)
	if board.MissingEvents == nil || *board.MissingEvents != "1" || board.HistoryCoverageStart == nil || *board.HistoryCoverageStart != e {
		t.Fatal("coverage", board)
	}
	var buffered, scratch int
	if err := f.db.QueryRow(`SELECT count(*) FROM game_bidding_net_rebuild_events`).Scan(&buffered); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM game_bidding_net_rebuild_totals`).Scan(&scratch); err != nil {
		t.Fatal(err)
	}
	if buffered != 0 || scratch != 0 {
		t.Fatal(buffered, scratch)
	}
	// Completed projection receives subsequent settlements without reopening
	// the historical scan.
	f.add(u, e+4, 5, 0, "bidding")
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "1102" {
		t.Fatal(got)
	}
}

func TestBiddingExpiryWhileScanningDoesNotResurrectOldEvents(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	e := f.epoch
	for range 1100 {
		f.add(u, e, -1, 1, "bidding")
	}
	resetBiddingRebuild(t, f.db)
	f.tx(func(tx *sql.Tx) error { _, err := advanceBiddingTx(context.Background(), tx, e); return err })
	// Expiry can span several transactions while a scanned shadow total exists.
	f.advance(e + week)
	f.advanceBidding(e + week)
	if got, _, _ := f.total(u, biddingBoard, "7d"); got != "0" {
		t.Fatal(got)
	}
	if board := f.read(u, biddingBoard, "7d", 1, e+week); len(board.Rows) != 0 {
		t.Fatal(board)
	}
}

func TestBiddingScanKeepsLaterExpiryAchievement(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	e := f.epoch
	f.add(u, e, -10, 10, "bidding")
	for range 1000 {
		f.add(u, e+1, -1, 1, "bidding")
	}
	resetBiddingRebuild(t, f.db)
	f.tx(func(tx *sql.Tx) error { _, err := advanceBiddingTx(context.Background(), tx, e+1); return err })
	f.add(u, e+2, -7, 7, "bidding")
	f.advance(e + week)
	f.advanceBidding(e + week)
	amount, at, _ := f.total(u, biddingBoard, "7d")
	if amount != "1007" || at != e+week {
		t.Fatalf("total/achievement=%s/%d, want 1007/%d", amount, at, e+week)
	}
}
