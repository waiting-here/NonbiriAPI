package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

// 4728 is the first legend value in the unchanged worm sample space.
func legendSource(species, size int, tail ...uint64) *scriptedSource {
	return &scriptedSource{values: append([]uint64{4728, uint64(species), uint64(size - 100)}, tail...)}
}

func TestFishingEasterEggKeepsOriginalSpeciesRewardAndReplay(t *testing.T) {
	for index, species := range []string{"yellowcheek", "taimen", "koi"} {
		t.Run(species, func(t *testing.T) {
			f := newGameFixture(t, legendSource(index, 137, 0, 99, 1, 0))
			user := f.seedUser("egg-reward", fixtureFunding)
			input := StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(800)}
			result, pending, err := f.service.StartFishing(context.Background(), input)
			if err != nil || pending != nil || result == nil {
				t.Fatalf("start: %+v %+v %v", result, pending, err)
			}
			outcome := result.Outcomes[0]
			if outcome.BlueFatFishLengthCM == nil || *outcome.BlueFatFishLengthCM != "203" || outcome.SpeciesKey != species || outcome.SizeCM != 137 || outcome.Tier != "legend" {
				t.Fatalf("presentation: %+v", outcome)
			}
			rules, err := fishing.Compile(fishing.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			original, err := rules.Roll(fishing.BaitWorm, legendSource(index, 137))
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Reward != game.FormatAmount(original.Settlement.PayoutMilli) || result.PayoutTotal != outcome.Reward {
				t.Fatalf("reward changed: %+v vs %+v", result, original)
			}
			calls := f.random.callCount()
			replay, _, err := f.service.StartFishing(context.Background(), input)
			if err != nil || replay == nil || !replay.IdempotentReplay || *replay.Outcomes[0].BlueFatFishLengthCM != "203" || f.random.callCount() != calls {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			state, err := f.service.FishingState(context.Background(), user)
			if err != nil || state.Unrevealed == nil || *state.Unrevealed.Outcomes[0].BlueFatFishLengthCM != "203" {
				t.Fatalf("restore: %+v %v", state, err)
			}
			for _, board := range []string{"single", "recent_single"} {
				got, err := f.service.FishingLeaderboard(context.Background(), user, board)
				if err != nil || len(got.Entries) != 1 || got.Entries[0].SpeciesKey != species || got.Entries[0].BlueFatFishLengthCM == nil || *got.Entries[0].BlueFatFishLengthCM != "203" {
					t.Fatalf("%s: %+v %v", board, got, err)
				}
			}
			if err := f.service.AcknowledgeFishing(context.Background(), user, result.BatchID); err != nil {
				t.Fatal(err)
			}
			state, err = f.service.FishingState(context.Background(), user)
			if err != nil || state.Unrevealed != nil {
				t.Fatalf("ack: %+v %v", state, err)
			}
			if f.scalar(`SELECT COUNT(*) FROM game_fishing_length_facts`) != 1 || f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle'`) != 1 {
				t.Fatal("ACK/replay changed settlement or ranking")
			}
		})
	}
}

func TestFishingEasterEggRandomFailureRollsBackWholeTenDrawBatch(t *testing.T) {
	for _, failure := range []string{"selection", "tail", "never ending tail", "sidecar write"} {
		t.Run(failure, func(t *testing.T) {
			values := make([]uint64, 0, 40)
			for i := 0; i < 10; i++ {
				values = append(values, 4728, uint64(i%3), 100)
			}
			source := &scriptedSource{values: values}
			switch failure {
			case "selection":
				source.failAt = 31
			case "tail":
				source.failAt = 34
			case "never ending tail":
				source.values = append(source.values, 0)
				source.max = true
			}
			f := newGameFixture(t, source)
			user := f.seedUser("egg-fail", fixtureFunding)
			if failure == "sidecar write" {
				if _, err := f.database.Exec(`CREATE TRIGGER reject_egg BEFORE INSERT ON game_fishing_outcome_lengths BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
					t.Fatal(err)
				}
			}
			operations := f.scalar(`SELECT COUNT(*) FROM credit_operations`)
			seq := f.scalar(`SELECT last_ledger_seq FROM credit_capacity`)
			result, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(801)})
			if err == nil || result != nil || pending != nil {
				t.Fatalf("failed draw accepted: %+v %+v %v", result, pending, err)
			}
			for _, table := range []string{"game_fishing_batches", "game_fishing_outcomes", "game_fishing_outcome_lengths", "game_fishing_best_lengths", "game_fishing_length_facts", "user_activity_daily"} {
				if f.scalar(`SELECT COUNT(*) FROM `+table) != 0 {
					t.Fatal("failed draw left", table)
				}
			}
			if f.scalar(`SELECT COUNT(*) FROM credit_operations`) != operations || f.scalar(`SELECT last_ledger_seq FROM credit_capacity`) != seq || f.scalar(`SELECT COUNT(*) FROM idempotency_records`) != 0 {
				t.Fatal("failed draw changed financial/idempotency state")
			}
		})
	}
}

func TestFishingTenDrawBatchRanksItsLongestCatchWithoutChangingRewards(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmt.Sprint(decorated), func(t *testing.T) {
			sizes := []int{110, 199, 200, 150, 120, 160, 190, 200, 133, 145}
			original := make([]uint64, 0, 30)
			for ordinal, size := range sizes {
				original = append(original, 4728, uint64(ordinal%3), uint64(size-100))
			}
			values := append([]uint64(nil), original...)
			for ordinal := range sizes {
				if decorated && (ordinal == 4 || ordinal == 7) {
					values = append(values, 0, 1, 1, 1, 1, 0) // Equal 205 cm lengths retain the earlier ordinal.
				} else {
					values = append(values, 1)
				}
			}
			f := newGameFixture(t, &scriptedSource{values: values})
			user := f.seedUser("ten-catch-maximum", fixtureFunding)
			result, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(804)})
			if err != nil || pending != nil || result == nil || len(result.Outcomes) != 10 {
				t.Fatalf("start: %+v %+v %v", result, pending, err)
			}
			rules, err := fishing.Compile(fishing.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			draws, err := rules.RollBatch(fishing.BaitWorm, 10, &scriptedSource{values: original})
			if err != nil {
				t.Fatal(err)
			}
			var total int64
			for ordinal, draw := range draws {
				outcome := result.Outcomes[ordinal]
				if outcome.Reward != game.FormatAmount(draw.Settlement.PayoutMilli) || outcome.SizeCM != sizes[ordinal] {
					t.Fatalf("economic draw changed at %d: %+v", ordinal, outcome)
				}
				total += draw.Settlement.PayoutMilli
			}
			if result.PayoutTotal != game.FormatAmount(total) {
				t.Fatal("batch payout changed", result.PayoutTotal, total)
			}
			wantOrdinal, wantLength := 2, "200"
			if decorated {
				wantOrdinal, wantLength = 4, "205"
			}
			for _, table := range []string{"game_fishing_best", "game_fishing_length_facts"} {
				if f.scalar(`SELECT ordinal FROM `+table) != int64(wantOrdinal) {
					t.Fatal("wrong batch maximum in", table)
				}
			}
			for _, board := range []string{"single", "recent_single"} {
				got, err := f.service.FishingLeaderboard(context.Background(), user, board)
				if err != nil || len(got.Entries) != 1 {
					t.Fatalf("%s: %+v %v", board, got, err)
				}
				row := got.Entries[0]
				length := fmt.Sprint(row.SizeCM)
				if row.BlueFatFishLengthCM != nil {
					length = *row.BlueFatFishLengthCM
				}
				if length != wantLength || row.SpeciesKey != result.Outcomes[wantOrdinal].SpeciesKey {
					t.Fatal("wrong ranked length or original species", board, row)
				}
			}
		})
	}
}

func TestFishingLengthFactFailureRecoversWithoutRerollOrPartialPayout(t *testing.T) {
	f := newGameFixture(t, legendSource(2, 100, 0, 0))
	user := f.seedUser("egg-recover", fixtureFunding)
	if _, err := f.database.Exec(`CREATE TRIGGER reject_length_fact BEFORE INSERT ON game_fishing_length_facts BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	result, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(802)})
	if err != nil || result != nil || pending == nil {
		t.Fatalf("pending: %+v %+v %v", result, pending, err)
	}
	for _, table := range []string{"game_fishing_best", "game_fishing_best_lengths", "game_fishing_rank_facts", "game_fishing_rank_aggregates", "game_fishing_length_facts"} {
		if f.scalar(`SELECT COUNT(*) FROM `+table) != 0 {
			t.Fatal("partial settlement left", table)
		}
	}
	if f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle'`) != 0 || f.scalar(`SELECT COUNT(*) FROM game_fishing_outcome_lengths`) != 1 {
		t.Fatal("failed settlement lost draw or credited payout")
	}
	calls := f.random.callCount()
	if _, err := f.database.Exec(`DROP TRIGGER reject_length_fact`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`UPDATE game_fishing_batches SET attempt_count=10,next_attempt_at=NULL,last_error_class='settlement_failed',retry_exhausted=1 WHERE id=? AND state='reserved'`, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	result, pending, err = f.service.RecoverFishing(context.Background(), RecoverInput{UserID: user, BatchID: pending.BatchID, IdempotencyKey: validTestKey(803)})
	if err != nil || pending != nil || result == nil || *result.Outcomes[0].BlueFatFishLengthCM != "201" || f.random.callCount() != calls {
		t.Fatalf("recover: %+v %+v %v", result, pending, err)
	}
	if f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle'`) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_length_facts`) != 1 {
		t.Fatal("recovery not exact once")
	}
}

func TestFishingRollingLengthExpiresWhileLifetimeSnapshotSurvives(t *testing.T) {
	f := newGameFixture(t, legendSource(2, 200, 0, 1, 1, 0))
	user := f.seedUser("rolling-length", fixtureFunding)
	first, _, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(810)})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Store(fixtureNow + 10)
	f.random.values = legendSource(0, 100, 0, 1, 0).values
	second, _, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(811)})
	if err != nil {
		t.Fatal(err)
	}
	window := int64(rankWindow.Seconds())
	for _, test := range []struct {
		now  int64
		want string
	}{{first.SettledAt + window - 1, "203"}, {first.SettledAt + window, "202"}, {second.SettledAt + window, ""}} {
		f.clock.Store(test.now)
		board, err := f.service.FishingLeaderboard(context.Background(), user, "recent_single")
		if err != nil {
			t.Fatal(err)
		}
		if board.WindowStart == nil || *board.WindowStart != test.now-window {
			t.Fatal("window", board.WindowStart)
		}
		if test.want == "" {
			if len(board.Entries) != 0 {
				t.Fatal("expired catch remained", board)
			}
		} else if len(board.Entries) != 1 || *board.Entries[0].BlueFatFishLengthCM != test.want {
			t.Fatal("window maximum", board)
		}
	}
	// The clock alone hides expired facts even before physical cleanup.
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_length_facts`) != 2 {
		t.Fatal("read unexpectedly mutated facts")
	}
	if _, err := f.service.Lifecycle().Cleanup(context.Background(), f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"game_fishing_batches", "game_fishing_outcomes", "game_fishing_outcome_lengths", "game_fishing_length_facts"} {
		if f.scalar(`SELECT COUNT(*) FROM `+table) != 0 {
			t.Fatal("retention left", table)
		}
	}
	board, err := f.service.FishingLeaderboard(context.Background(), user, "single")
	if err != nil || len(board.Entries) != 1 || board.Entries[0].SpeciesKey != "koi" || *board.Entries[0].BlueFatFishLengthCM != "203" || f.scalar(`SELECT COUNT(*) FROM game_fishing_best WHERE batch_id IS NULL AND ordinal IS NULL`) != 1 {
		t.Fatalf("lifetime snapshot: %+v %v", board, err)
	}
}

func TestFishingHugeLengthsAreExactAcrossAuthorityExportAndDeletion(t *testing.T) {
	for _, length := range []string{"9007199254740993", strings.Repeat("9", 128)} {
		t.Run(fmt.Sprintf("digits-%d", len(length)), func(t *testing.T) {
			f := newGameFixture(t, legendSource(1, 137, 1))
			user := f.seedUser("huge-egg", fixtureFunding)
			f.service.beforeSettlement = func(string) error { return errInjected }
			_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(820)})
			if err != nil || pending == nil {
				t.Fatal(pending, err)
			}
			if _, err = f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,0,?)`, pending.BatchID, length); err != nil {
				t.Fatal(err)
			}
			f.service.beforeSettlement = nil
			result, err := f.service.settle(context.Background(), pending.BatchID, user, f.clock.Load(), false)
			if err != nil {
				t.Fatal(err)
			}
			if *result.Outcomes[0].BlueFatFishLengthCM != length || result.Outcomes[0].SizeCM != 137 {
				t.Fatal("inexact outcome", result)
			}
			tx, err := f.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			exported, err := f.service.Lifecycle().ExportTx(context.Background(), tx, user, f.clock.Load(), 100)
			_ = tx.Rollback()
			if err != nil || exported.Single == nil || exported.RollingBest == nil || *exported.Single.BlueFatFishLengthCM != length || *exported.RollingBest.BlueFatFishLengthCM != length {
				t.Fatalf("export: %+v %v", exported, err)
			}
			body, err := json.Marshal(exported)
			if err != nil || !strings.Contains(string(body), `"blue_fat_fish_length_cm":"`+length+`"`) {
				t.Fatal("inexact JSON", string(body), err)
			}
			guard, err := f.service.Lifecycle().BeginUserDeletion(user)
			if err != nil {
				t.Fatal(err)
			}
			tx, err = f.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = guard.Prepare(context.Background(), tx, f.clock.Load()+1); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if !guard.Commit() {
				t.Fatal("delete guard")
			}
			for _, table := range []string{"game_fishing_outcome_lengths", "game_fishing_best_lengths", "game_fishing_length_facts"} {
				if f.scalar(`SELECT COUNT(*) FROM `+table) != 0 {
					t.Fatal("deletion left", table)
				}
			}
			if _, err = f.service.settle(context.Background(), result.BatchID, user, f.clock.Load()+2, false); !errors.Is(err, ErrNotFound) {
				t.Fatal("late settlement", err)
			}
		})
	}
}

func TestFishingRecentLengthTop20PrivacyAndBanExclusion(t *testing.T) {
	f := newGameFixture(t, nil)
	var first, requester int64
	for index := 0; index < 21; index++ {
		user := f.seedUser(fmt.Sprintf("recent-rank-%d", index), fixtureFunding)
		if index == 0 {
			first = user
		}
		if index == 20 {
			requester = user
		}
		f.random.values = legendSource(0, 200-index, 1).values
		result, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(850 + index)})
		if err != nil || pending != nil || result == nil {
			t.Fatalf("seed %d: %+v %+v %v", index, result, pending, err)
		}
	}
	board, err := f.service.FishingLeaderboard(context.Background(), requester, "recent_single")
	if err != nil || len(board.Entries) != 20 || board.Me == nil || board.Me.Rank != "21" || board.Me.SizeCM != 180 || !board.Me.IsMe {
		t.Fatalf("top20/me: %+v %v", board, err)
	}
	for _, row := range board.Entries {
		if row.Identity.Kind != "anonymous" || row.IsMe {
			t.Fatal("anonymous identity", row)
		}
	}
	if _, err = f.database.Exec(`UPDATE users SET is_banned=1,banned_reason='fixture' WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec(`UPDATE users SET game_profile_public=1,guild_nick='Public recent angler' WHERE id=?`, requester); err != nil {
		t.Fatal(err)
	}
	board, err = f.service.FishingLeaderboard(context.Background(), requester, "recent_single")
	if err != nil || len(board.Entries) != 20 || board.Me != nil || board.Entries[19].Rank != "20" || !board.Entries[19].IsMe || board.Entries[19].Identity.DisplayName != "Public recent angler" {
		t.Fatalf("ban/public: %+v %v", board, err)
	}
}

func TestFishingPresentationConstraintsRejectHostileStates(t *testing.T) {
	f := newGameFixture(t, legendSource(2, 137, 1))
	user := f.seedUser("length-constraints", fixtureFunding)
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(880)})
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	for _, length := range []any{nil, "", "200", "0201", "201.0", "２０１", "201\x00", strings.Repeat("9", 129)} {
		if _, err := f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,0,?)`, pending.BatchID, length); err == nil {
			t.Fatalf("invalid length accepted: %#v", length)
		}
	}
	if _, err := f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,1,'201')`, pending.BatchID); err == nil {
		t.Fatal("foreign ordinal accepted")
	}
	if _, err := f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,0,'1000')`, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`UPDATE game_fishing_outcome_lengths SET length_cm='999'`); err == nil {
		t.Fatal("rerolled presentation")
	}
	if _, err := f.database.Exec(`UPDATE game_fishing_outcomes SET species_key='taimen' WHERE batch_id=?`, pending.BatchID); err == nil {
		t.Fatal("changed original species behind presentation")
	}
	f.service.beforeSettlement = nil
	if _, err := f.service.settle(context.Background(), pending.BatchID, user, f.clock.Load(), false); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM game_fishing_outcome_lengths`, `UPDATE game_fishing_best_lengths SET length_cm='999'`, `UPDATE game_fishing_length_facts SET size_cm=200`, `UPDATE game_fishing_best SET size_cm=200`} {
		if _, err := f.database.Exec(query); err == nil {
			t.Fatalf("inconsistent terminal mutation accepted: %s", query)
		}
	}
	other := f.seedUser("nonlegend-length", fixtureFunding)
	f.random.values = []uint64{0, 0}
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, junk, err := f.service.StartFishing(context.Background(), StartInput{UserID: other, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(881)})
	if err != nil || junk == nil {
		t.Fatal(junk, err)
	}
	if _, err := f.database.Exec(`INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm) VALUES(?,0,'201')`, junk.BatchID); err == nil {
		t.Fatal("nonlegend presentation accepted")
	}
}
