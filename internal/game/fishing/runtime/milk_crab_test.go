package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func enableMilkCrab(t *testing.T, f *gameFixture) {
	t.Helper()
	if _, err := f.database.Exec(`UPDATE site_config SET value='1000' WHERE key='game_fishing_milk_crab_chance_bps'`); err != nil {
		t.Fatal(err)
	}
}

func TestMilkCrabFrozenAuthorityReplayAndLifetimeKind(t *testing.T) {
	f := newGameFixture(t, legendSource(2, 137, 1, 1, 1, 0))
	enableMilkCrab(t, f)
	user := f.seedUser("milk-authority", fixtureFunding)
	ctx := context.Background()
	input := StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9900)}
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.startLegacy(ctx, input)
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	var blue, milk int
	if err = f.database.QueryRow(`SELECT blue_fish_chance_bps,milk_crab_chance_bps FROM game_fishing_batches WHERE id=?`, pending.BatchID).Scan(&blue, &milk); err != nil || blue != 1000 || milk != 1000 {
		t.Fatal(blue, milk, err)
	}
	if _, err = f.database.Exec(`UPDATE game_fishing_batches SET milk_crab_chance_bps=0 WHERE id=?`, pending.BatchID); err == nil {
		t.Fatal("accepted milk chance changed")
	}
	if _, err = f.database.Exec(`UPDATE site_config SET value='0' WHERE key='game_fishing_milk_crab_chance_bps'`); err != nil {
		t.Fatal(err)
	}
	calls := f.random.callCount()
	f.service.beforeSettlement = nil
	if _, err = f.service.settle(ctx, pending.BatchID, user, fixtureNow, false); err != nil {
		t.Fatal(err)
	}
	result, _, err := f.service.startLegacy(ctx, input)
	if err != nil || result == nil || !result.IdempotentReplay || f.random.callCount() != calls {
		t.Fatal(result, err)
	}
	out := result.Outcomes[0]
	if out.MilkCrabLengthCM == nil || *out.MilkCrabLengthCM != "203" || out.BlueFatFishLengthCM != nil || out.SpeciesKey != "koi" || out.SizeCM != 137 {
		t.Fatal(out)
	}
	for _, query := range []string{`UPDATE game_fishing_outcome_lengths SET kind='blue'`, `UPDATE game_fishing_best_lengths SET kind='blue'`} {
		if _, err = f.database.Exec(query); err == nil {
			t.Fatal("presentation kind changed", query)
		}
	}
	state, err := f.service.FishingState(ctx, user)
	if err != nil || state.Unrevealed == nil || state.Unrevealed.Outcomes[0].MilkCrabLengthCM == nil {
		t.Fatal(state, err)
	}
	// Equal later blue catches keep the first accepted milk-crab record.
	f.random.values = legendSource(0, 137, 0, 1, 1, 0).values
	f.clock.Add(1)
	input.IdempotencyKey = validTestKey(9901)
	second, _, err := f.service.startLegacy(ctx, input)
	if err != nil || second == nil {
		t.Fatal(second, err)
	}
	if second.Outcomes[0].Reward != out.Reward {
		t.Fatal("presentation changed reward")
	}
	for _, board := range []string{"single", "recent_single"} {
		got, err := f.service.FishingLeaderboard(ctx, user, board)
		if err != nil || len(got.Entries) != 1 || got.Entries[0].MilkCrabLengthCM == nil || *got.Entries[0].MilkCrabLengthCM != "203" || got.Entries[0].BlueFatFishLengthCM != nil {
			t.Fatal(board, got, err)
		}
	}
	f.clock.Store(fixtureNow + 1 + int64(rankWindow.Seconds()))
	if _, err = f.service.Lifecycle().Cleanup(ctx, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 || f.scalar(`SELECT COUNT(*) FROM game_fishing_length_facts`) != 0 {
		t.Fatal("expired batches retained")
	}
	got, err := f.service.FishingLeaderboard(ctx, user, "single")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].MilkCrabLengthCM == nil || *got.Entries[0].MilkCrabLengthCM != "203" {
		t.Fatal("lost lifetime kind", got, err)
	}
}

func TestMilkCrabHugeLengthExportAndDeletionOrders(t *testing.T) {
	for _, settleFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete-first", true: "settle-first"}[settleFirst], func(t *testing.T) {
			f := newGameFixture(t, legendSource(1, 137, 9))
			enableMilkCrab(t, f)
			user := f.seedUser("milk-delete", fixtureFunding)
			ctx := context.Background()
			f.service.beforeSettlement = func(string) error { return errInjected }
			_, pending, err := f.service.startLegacy(ctx, StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9910)})
			if err != nil || pending == nil {
				t.Fatal(pending, err)
			}
			length := strings.Repeat("9", 128)
			if _, err = f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm,kind) VALUES(?,0,?,'milk')`, pending.BatchID, length); err != nil {
				t.Fatal(err)
			}
			f.service.beforeSettlement = nil
			if settleFirst {
				result, err := f.service.settle(ctx, pending.BatchID, user, fixtureNow, false)
				if err != nil || result.Outcomes[0].MilkCrabLengthCM == nil || *result.Outcomes[0].MilkCrabLengthCM != length {
					t.Fatal(result, err)
				}
				tx, err := f.database.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				exported, err := f.service.Lifecycle().ExportTx(ctx, tx, user, fixtureNow, 100)
				_ = tx.Rollback()
				if err != nil || exported.Single == nil || exported.RollingBest == nil || exported.Single.MilkCrabLengthCM == nil || *exported.Single.MilkCrabLengthCM != length || *exported.RollingBest.MilkCrabLengthCM != length {
					t.Fatal(exported, err)
				}
				body, err := json.Marshal(exported)
				if err != nil || !strings.Contains(string(body), `"milk_crab_length_cm":"`+length+`"`) {
					t.Fatal("inexact export", err)
				}
			}
			guard, err := f.service.Lifecycle().BeginUserDeletion(user)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = guard.Prepare(ctx, tx, fixtureNow+1); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if !guard.Commit() {
				t.Fatal("delete guard")
			}
			for _, table := range []string{"game_fishing_batches", "game_fishing_outcome_lengths", "game_fishing_best_lengths", "game_fishing_length_facts"} {
				if f.scalar(`SELECT COUNT(*) FROM `+table) != 0 {
					t.Fatal(table)
				}
			}
			if _, err = f.service.settle(ctx, pending.BatchID, user, fixtureNow+2, false); !errors.Is(err, ErrNotFound) {
				t.Fatal("late callback", err)
			}
			if !settleFirst {
				if f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_release' AND source_id=?`, pending.BatchID) != 1 {
					t.Fatal("refund missing")
				}
				tx, err = f.database.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				wallet, err := ledger.UserAccount(ctx, tx, user)
				_ = tx.Rollback()
				if err != nil || wallet.Balance.Decimal() != "1000000000" {
					t.Fatal(wallet, err)
				}
			}
		})
	}
}

func TestMilkCrabBudgetRollsBackAcceptedWork(t *testing.T) {
	source := legendSource(2, 137, 1)
	source.max = true
	f := newGameFixture(t, source)
	enableMilkCrab(t, f)
	user := f.seedUser("milk-budget", fixtureFunding)
	before := f.scalar(`SELECT COUNT(*) FROM credit_operations`)
	result, pending, err := f.service.startLegacy(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9920)})
	if err == nil || result != nil || pending != nil {
		t.Fatal(result, pending, err)
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 || f.scalar(`SELECT COUNT(*) FROM credit_operations`) != before || f.scalar(`SELECT COUNT(*) FROM idempotency_records`) != 0 {
		t.Fatal("failed milk tail left accepted work")
	}
}

func TestMilkCrabBatchTieRetainsFirstOrdinal(t *testing.T) {
	values := make([]uint64, 0, 42)
	for i := 0; i < 10; i++ {
		values = append(values, 4728, 2, 37)
	}
	values = append(values, 1, 0, 0, 0)
	for i := 0; i < 8; i++ {
		values = append(values, 9)
	}
	f := newGameFixture(t, &scriptedSource{values: values})
	enableMilkCrab(t, f)
	user := f.seedUser("milk-batch", fixtureFunding)
	result, _, err := f.service.startLegacy(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(9930)})
	if err != nil || result == nil || result.Outcomes[0].MilkCrabLengthCM == nil || result.Outcomes[1].BlueFatFishLengthCM == nil {
		t.Fatal(result, err)
	}
	var ordinal int
	var milk, blue *string
	if err = f.database.QueryRow(`SELECT ordinal,milk_crab_length_cm,blue_fat_fish_length_cm FROM game_fishing_length_facts`).Scan(&ordinal, &milk, &blue); err != nil || ordinal != 0 || milk == nil || *milk != "201" || blue != nil {
		t.Fatal(ordinal, milk, blue, err)
	}
}
