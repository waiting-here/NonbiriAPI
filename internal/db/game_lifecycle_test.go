package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestFishingStateAckAndOwnershipLifecycle(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
	ctx := context.Background()
	started, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "lifecycle-start"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Pending == nil || state.Unrevealed != nil || state.Pending.Outcome != nil {
		t.Fatalf("pending state = %#v, err=%v", state, err)
	}
	other, err := trig.store.CreateDiscordUser("lifecycle-other", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := trig.store.AckFishingRound(ctx, other.ID, started.RoundID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner ack = %v, want not found", err)
	}
	settled, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID})
	if err != nil || settled.State != GameCommitted || settled.Outcome == nil {
		t.Fatalf("settle = %#v, %v", settled, err)
	}
	state, err = trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Pending != nil || state.Unrevealed == nil || state.HasMoreUnrevealed {
		t.Fatalf("unrevealed state = %#v, err=%v", state, err)
	}
	if err := trig.store.AckFishingRound(ctx, trig.user.ID, started.RoundID); err != nil {
		t.Fatal(err)
	}
	if err := trig.store.AckFishingRound(ctx, trig.user.ID, started.RoundID); err != nil {
		t.Fatalf("idempotent ack: %v", err)
	}
	state, err = trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Unrevealed != nil {
		t.Fatalf("acked state = %#v, err=%v", state, err)
	}
	if _, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "lifecycle-next"}); err != nil {
		t.Fatalf("terminal round did not release next start: %v", err)
	}
}

func TestFishingStatePagesEarliestUnacknowledgedResults(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
	ctx := context.Background()
	first, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "state-page-first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: first.RoundID}); err != nil {
		t.Fatal(err)
	}
	trig.clock.Advance(time.Second)
	second, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "state-page-second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: second.RoundID}); err != nil {
		t.Fatal(err)
	}

	state, err := trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Unrevealed == nil || state.Unrevealed.RoundID != first.RoundID || !state.HasMoreUnrevealed {
		t.Fatalf("first unrevealed page = %#v, err=%v", state, err)
	}
	if err := trig.store.AckFishingRound(ctx, trig.user.ID, first.RoundID); err != nil {
		t.Fatal(err)
	}
	state, err = trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Unrevealed == nil || state.Unrevealed.RoundID != second.RoundID || state.HasMoreUnrevealed {
		t.Fatalf("second unrevealed page = %#v, err=%v", state, err)
	}
	if err := trig.store.AckFishingRound(ctx, trig.user.ID, second.RoundID); err != nil {
		t.Fatal(err)
	}
	state, err = trig.store.GetFishingState(ctx, trig.user.ID)
	if err != nil || state.Unrevealed != nil || state.HasMoreUnrevealed {
		t.Fatalf("fully acknowledged state = %#v, err=%v", state, err)
	}
}

func TestFishingRetentionDeletesOnlyOldTerminalRows(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
	ctx := context.Background()
	old, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "retention-old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: old.RoundID}); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "retention-pending"}); err != nil {
		t.Fatal(err)
	}
	oldUnix := trig.clock.Now().Unix() - int64(GameRoundRetention/time.Second) - 1
	if _, err := trig.store.DB().Exec(`UPDATE game_rounds SET created_at=?,settled_at=?,revealed_at=NULL,updated_at=? WHERE id=?`, oldUnix, oldUnix, oldUnix, old.RoundID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE game_settlements SET created_at=?,finalized_at=?,updated_at=? WHERE id=?`, oldUnix, oldUnix, oldUnix, old.SettlementID); err != nil {
		t.Fatal(err)
	}
	cutoff := trig.clock.Now().Unix() - int64(GameRoundRetention/time.Second)
	deleted, stopped, err := trig.store.CleanupFishingRetentionBefore(ctx, cutoff, 200, 100)
	if err != nil || stopped || deleted != 1 {
		t.Fatalf("retention = deleted %d stopped %v err %v", deleted, stopped, err)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds WHERE id=?`, old.RoundID); got != 0 {
		t.Fatalf("old round remains: %d", got)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds WHERE settled_at IS NULL`); got != 1 {
		t.Fatalf("pending was swept: %d", got)
	}
}

func TestFishingRetentionKeepsCutoffAndPreservesBestAndLedgerCorrelation(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
	ctx := context.Background()
	old, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "retention-correlation-old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: old.RoundID}); err != nil {
		t.Fatal(err)
	}
	trig.clock.Advance(time.Second)
	boundary, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "retention-correlation-boundary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: boundary.RoundID}); err != nil {
		t.Fatal(err)
	}

	cutoff := trig.clock.Now().Unix() - int64(GameRoundRetention/time.Second)
	oldUnix := cutoff - 1
	if _, err := trig.store.DB().Exec(`UPDATE game_rounds SET created_at=?,settled_at=?,revealed_at=NULL,updated_at=? WHERE id=?`, oldUnix, oldUnix, oldUnix, old.RoundID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE game_settlements SET created_at=?,finalized_at=?,updated_at=? WHERE id=?`, oldUnix, oldUnix, oldUnix, old.SettlementID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE game_rounds SET created_at=?,settled_at=?,updated_at=? WHERE id=?`, cutoff, cutoff, cutoff, boundary.RoundID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE game_settlements SET finalized_at=?,updated_at=? WHERE id=?`, cutoff, cutoff, boundary.SettlementID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,?,?,?,?,?)`,
		trig.user.ID, old.RoundID, "koi", "legend", 100, oldUnix); err != nil {
		t.Fatal(err)
	}

	deleted, stopped, err := trig.store.CleanupFishingRetentionBefore(ctx, cutoff, 200, 100)
	if err != nil || stopped || deleted != 1 {
		t.Fatalf("retention = deleted %d stopped %v err %v", deleted, stopped, err)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds WHERE id=?`, old.RoundID); got != 0 {
		t.Fatalf("old round remains: %d", got)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements WHERE id=?`, old.SettlementID); got != 0 {
		t.Fatalf("old settlement remains: %d", got)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds WHERE id=?`, boundary.RoundID); got != 1 {
		t.Fatalf("cutoff round was removed: %d", got)
	}
	var bestRound sql.NullString
	if err := trig.store.DB().QueryRow(`SELECT round_id FROM game_fishing_best WHERE user_id=?`, trig.user.ID).Scan(&bestRound); err != nil {
		t.Fatal(err)
	}
	if bestRound.Valid {
		t.Fatalf("best correlation survived as non-null: %q", bestRound.String)
	}
	if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE user_id=? AND game_settlement_id=?`, trig.user.ID, old.SettlementID); got != 2 {
		t.Fatalf("old ledger correlation lost: %d", got)
	}
}

func TestFishingDeleteCascadesAndLateSettlementOrAckCannotResurrect(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
	ctx := context.Background()
	started, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "delete-race",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := trig.store.DeleteUserAccount(ctx, trig.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late settle = %v, want not found", err)
	}
	if err := trig.store.AckFishingRound(ctx, trig.user.ID, started.RoundID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late ack = %v, want not found", err)
	}
	for table := range map[string]struct{}{
		"game_settlements": {}, "game_rounds": {}, "game_fishing_outcomes": {},
		"game_fishing_best": {}, "credit_ledger": {},
	} {
		if got := gameCount(t, trig.store, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Fatalf("deleted account left %s rows=%d", table, got)
		}
	}
}

func TestFishingLeaderboardDeterministicAndPrivateProjection(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	ctx := context.Background()
	second, err := trig.store.CreateDiscordUser("leader-second", "second", "hash2")
	if err != nil {
		t.Fatal(err)
	}
	third, err := trig.store.CreateDiscordUser("leader-third", "third", "hash3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET game_profile_public=1,guild_nick='public second' WHERE id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET game_profile_public=0 WHERE id=?`, third.ID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		userID  int64
		species string
		size    int
		caught  int64
	}{
		{trig.user.ID, "koi", 100, 20},
		{second.ID, "taimen", 100, 10},
		{third.ID, "yellowcheek", 100, 5},
	} {
		if _, err := trig.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,NULL,?,?,?,?)`, row.userID, row.species, "legend", row.size, row.caught); err != nil {
			t.Fatal(err)
		}
	}
	board, err := trig.store.ListFishingLeaderboard(ctx, trig.user.ID, FishingLeaderboardSingle, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 3 || board.Mine == nil || board.Mine.Rank != 3 || board.Entries[0].UserID != third.ID {
		t.Fatalf("leaderboard = %#v", board)
	}
	if board.Entries[0].ProfilePublic || !board.Entries[1].ProfilePublic || board.Entries[2].ProfilePublic {
		t.Fatalf("privacy projection = %#v", board.Entries)
	}
}

func insertLeaderboardRound(t *testing.T, store *Store, userID int64, index int, settledAt, payout int64) string {
	t.Helper()
	settlementID := fmt.Sprintf("leader-settlement-%02d", index)
	roundID := fmt.Sprintf("leader-round-%02d", index)
	createdAt := settledAt - 1
	hash := make([]byte, 32)
	hash[0] = byte(index)
	if _, err := store.DB().Exec(`INSERT INTO game_settlements(
		id,user_id,game_type,game_version,state,entry_milli,payout_milli,auto_settle_at,created_at,finalized_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, settlementID, userID, "fishing", 1, "committed", 2_500_000, payout,
		createdAt+120, createdAt, settledAt, settledAt); err != nil {
		t.Fatalf("insert leaderboard settlement %s: %v", settlementID, err)
	}
	if _, err := store.DB().Exec(`INSERT INTO game_rounds(
		id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,event_seq,created_at,settled_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, roundID, userID, "fishing", 1, settlementID, hash, hash, 2, createdAt, settledAt, settledAt); err != nil {
		t.Fatalf("insert leaderboard round %s: %v", roundID, err)
	}
	if _, err := store.DB().Exec(`INSERT INTO game_fishing_outcomes(round_id,bait,species_key,tier,size_cm) VALUES(?,?,?,?,?)`,
		roundID, "worm", "koi", "legend", 100); err != nil {
		t.Fatalf("insert leaderboard outcome %s: %v", roundID, err)
	}
	return roundID
}

func TestFishingLeaderboardsTopTwentyMineTieCutoffAndEligibility(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	ctx := context.Background()
	now := trig.clock.Now()
	nowUnix := now.Unix()
	users := make([]*User, 0, 25)
	users = append(users, trig.user)
	for index := 1; index < 25; index++ {
		user, err := trig.store.CreateDiscordUser(fmt.Sprintf("leader-board-%02d", index), fmt.Sprintf("leader-board-%02d", index), "")
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, user)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET game_profile_public=1,guild_nick='public board user' WHERE id=?`, users[1].ID); err != nil {
		t.Fatal(err)
	}
	for index, user := range users {
		caughtAt := nowUnix
		size := 101
		if index == 0 {
			size, caughtAt = 100, nowUnix+1
		}
		if _, err := trig.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,?,?,?,?,?)`,
			user.ID, nil, "koi", "legend", size, caughtAt); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := trig.store.EnsureAdminUser("leader-board-admin")
	if err != nil {
		t.Fatal(err)
	}
	banned, err := trig.store.CreateDiscordUser("leader-board-banned", "banned", "")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := trig.store.CreateDiscordUser("leader-board-deleted", "deleted", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, nowUnix+3600, banned.ID); err != nil {
		t.Fatal(err)
	}
	for index, user := range []*User{admin, banned, deleted} {
		if _, err := trig.store.DB().Exec(`INSERT INTO game_fishing_best(user_id,round_id,species_key,tier,size_cm,caught_at) VALUES(?,?,?,?,?,?)`,
			user.ID, nil, "koi", "legend", 200, nowUnix-int64(index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := trig.store.DeleteUserAccount(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}

	single, err := trig.store.ListFishingLeaderboard(ctx, trig.user.ID, FishingLeaderboardSingle, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Entries) != 20 || single.Mine == nil || single.Mine.Rank != 25 {
		t.Fatalf("single top20/me = entries %d mine %#v", len(single.Entries), single.Mine)
	}
	if single.Entries[0].SizeCM != single.Entries[1].SizeCM || single.Entries[0].UserID >= single.Entries[1].UserID {
		t.Fatalf("single tie order = %#v, %#v", single.Entries[0], single.Entries[1])
	}
	if !single.Entries[0].ProfilePublic || single.Entries[0].GuildNick != "public board user" {
		t.Fatalf("single public projection = %#v", single.Entries[0])
	}
	for _, entry := range single.Entries {
		if entry.UserID == admin.ID || entry.UserID == banned.ID || entry.UserID == deleted.ID {
			t.Fatalf("ineligible single entry = %#v", entry)
		}
	}

	for index, user := range users {
		payout := int64(100)
		if index == 0 {
			payout = 1
		}
		insertLeaderboardRound(t, trig.store, user.ID, index, nowUnix, payout)
	}
	oldUser, err := trig.store.CreateDiscordUser("leader-board-old", "old", "")
	if err != nil {
		t.Fatal(err)
	}
	cutoff := nowUnix - int64(GameRoundRetention/time.Second)
	insertLeaderboardRound(t, trig.store, oldUser.ID, 25, cutoff-1, 10_000)
	insertLeaderboardRound(t, trig.store, admin.ID, 26, nowUnix, 10_000)
	insertLeaderboardRound(t, trig.store, banned.ID, 27, nowUnix, 10_000)

	total, err := trig.store.ListFishingLeaderboard(ctx, trig.user.ID, FishingLeaderboardTotal, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(total.Entries) != 20 || total.Mine == nil || total.Mine.Rank != 25 || total.WindowStart == nil || total.WindowStart.Unix() != cutoff {
		t.Fatalf("total top20/me = entries %d mine %#v window %#v", len(total.Entries), total.Mine, total.WindowStart)
	}
	if total.Entries[0].TotalCreditsMilli != total.Entries[1].TotalCreditsMilli || total.Entries[0].UserID >= total.Entries[1].UserID {
		t.Fatalf("total tie order = %#v, %#v", total.Entries[0], total.Entries[1])
	}
	for _, entry := range total.Entries {
		if entry.UserID == admin.ID || entry.UserID == banned.ID || entry.UserID == oldUser.ID {
			t.Fatalf("ineligible/expired total entry = %#v", entry)
		}
	}
}
