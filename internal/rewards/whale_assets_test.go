package rewards

import (
	"errors"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	whaleasset "github.com/waiting-here/NonbiriAPI/internal/whalehaven/assets"
)

func TestWhaleCurrencyRedemptionBroadcastAndConcurrentSpending(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	if _, err := f.db.Exec("UPDATE site_config SET value='0' WHERE key='games_enabled'"); err != nil {
		t.Fatal(err)
	}
	in := input("code")
	in.Code = "WHALE-WELCOME"
	in.Rewards = []rewardasset.Item{{Asset: whaleasset.Pearls, Quantity: "6"}, {Asset: whaleasset.Shards, Quantity: "5"}}
	code := f.create(t, in, "whale-welcome")
	key := f.nextKey()
	first, err := f.s.Redeem(ctx, f.user, key, code.Code)
	if err != nil || len(first.Results) != 2 {
		t.Fatal(first, err)
	}
	replay, err := f.s.Redeem(ctx, f.user, key, code.Code)
	if err != nil || replay.ID != first.ID {
		t.Fatal(replay, err)
	}
	repeat, err := f.s.Redeem(ctx, f.user, f.nextKey(), code.Code)
	if err != nil || !repeat.AlreadyClaimed {
		t.Fatal(repeat, err)
	}

	in = input("broadcast")
	in.Conditions = Condition{Op: "compare", Field: "asset", Asset: whaleasset.Pearls, Compare: "gte", Value: "6"}
	in.Rewards = []rewardasset.Item{{Asset: whaleasset.Shards, Quantity: "2"}}
	campaign := f.create(t, in, "whale-broadcast")
	if campaign.Matched != 1 {
		t.Fatal("asset condition matched", campaign.Matched)
	}
	if n, err := f.s.ProcessBatch(ctx, 10); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := f.s.ProcessBatch(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var got whaleasset.Balance
	if err := f.db.QueryRow("SELECT pearls,shards FROM whale_haven_assets WHERE user_id=?", f.user).Scan(&got.Pearls, &got.Shards); err != nil || got != (whaleasset.Balance{Pearls: 6, Shards: 7}) {
		t.Fatal(got, err)
	}
	if f.scalar(t, "SELECT count(*) FROM whale_haven_profiles") != 0 {
		t.Fatal("site reward adopted a whale")
	}
	rows, err := f.db.Query("SELECT reason,operation_id FROM whale_haven_movements WHERE user_id=? ORDER BY id", f.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][2]string{{"redemption_code", code.ID}, {"redemption_code", code.ID}, {"site_reward", campaign.ID}} {
		var reason, id string
		if !rows.Next() {
			t.Fatal("missing movement")
		}
		if err := rows.Scan(&reason, &id); err != nil || [2]string{reason, id} != want {
			t.Fatal(reason, id, err)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected duplicate movement")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			tx, err := f.db.BeginTx(ctx, nil)
			if err == nil {
				defer tx.Rollback()
				_, err = AdjustResourceTx(ctx, tx, f.user, f.admin, f.at.Load(), ResourceAdjustment{Asset: whaleasset.Pearls, Quantity: "6", ExpectedQuantity: "6", Direction: "decrease", Reason: "Remove duplicate award"})
				if err == nil {
					err = tx.Commit()
				}
			}
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	if f.scalar(t, "SELECT pearls FROM whale_haven_assets WHERE user_id=?", f.user) != 0 {
		t.Fatal("balance after concurrent debit")
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := whaleasset.MoveTx(ctx, tx, f.user, f.at.Load()*1000, whaleasset.Balance{Pearls: -1}, "exchange", ""); !errors.Is(err, whaleasset.ErrCapacity) {
		t.Fatal("negative balance admitted", err)
	}
	if err := whaleasset.MoveTx(ctx, tx, f.user, f.at.Load()*1000, whaleasset.Balance{Shards: -5}, "conch_draw", ""); err != nil {
		t.Fatal(err)
	}
	balances, err := whaleasset.RewardBalancesTx(ctx, tx, f.user)
	if err != nil || balances[whaleasset.Shards] != "2" {
		t.Fatal(balances, err)
	}
}
