package runtime

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestFishingLeaderboardReadSnapshotsDoNotBlockWriter(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	public := fixture.seedUser("snapshot-public", fixtureFunding)
	banned := fixture.seedUser("snapshot-banned", fixtureFunding)
	for index, userID := range []int64{public, banned} {
		if _, _, err := fixture.service.startLegacy(t.Context(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(460 + index)}); err != nil {
			t.Fatal(err)
		}
	}
	queryNow := fixtureNow + int64(rankWindow/time.Second) - 1
	if _, err := fixture.database.Exec(`UPDATE users SET game_profile_public=1 WHERE id IN (?,?)`, public, banned); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=?,banned_reason='fixture' WHERE id=?`, queryNow+1, banned); err != nil {
		t.Fatal(err)
	}
	for _, board := range []string{"single", "recent_single", "total"} {
		t.Run(board, func(t *testing.T) {
			fixture.clock.Store(queryNow)
			if _, err := fixture.database.Exec(`UPDATE users SET guild_nick='Before write' WHERE id=?`, public); err != nil {
				t.Fatal(err)
			}
			want, err := fixture.service.FishingLeaderboard(t.Context(), public, board)
			if err != nil || len(want.Entries) != 2 {
				t.Fatalf("initial board: %+v, %v", want, err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			reading, release := make(chan struct{}), make(chan struct{})
			fixture.service.beforeLeaderboardQuery = func(ctx context.Context, tx *sql.Tx) error {
				var name string
				// Establish the SQLite snapshot before holding the live read transaction.
				if err := tx.QueryRowContext(ctx, `SELECT guild_nick FROM users WHERE id=?`, public).Scan(&name); err != nil {
					return err
				}
				close(reading)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			defer func() { fixture.service.beforeLeaderboardQuery = nil }()
			type response struct {
				board FishingLeaderboard
				err   error
			}
			done := make(chan response, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				result, err := fixture.service.FishingLeaderboard(ctx, public, board)
				done <- response{result, err}
			}()
			defer func() {
				cancel()
				<-finished
			}()
			select {
			case <-reading:
			case result := <-done:
				t.Fatalf("leaderboard failed before opening its snapshot: %v", result.err)
			case <-ctx.Done():
				t.Fatal("leaderboard did not open its snapshot", ctx.Err())
			}
			writer, reader := fixture.store.StatsByPool()
			if writer.InUse != 0 || reader.InUse != 1 {
				t.Fatalf("leaderboard occupancy: writer=%d reader=%d", writer.InUse, reader.InUse)
			}
			// The reader stays paused until this ordinary write has committed.
			if _, err := fixture.database.ExecContext(ctx, `UPDATE users SET guild_nick='After write' WHERE id=?`, public); err != nil {
				t.Fatal("leaderboard snapshot blocked the writer", err)
			}
			fixture.clock.Store(queryNow + 2)
			close(release)
			result := <-done
			if result.err != nil || !equalJSONValue(t, result.board, want) {
				t.Fatalf("snapshot or frozen query time changed: got=%+v want=%+v err=%v", result.board, want, result.err)
			}
			fixture.service.beforeLeaderboardQuery = nil
			fixture.clock.Store(queryNow)
			latest, err := fixture.service.FishingLeaderboard(ctx, public, board)
			me := ownLeaderboardRow(latest)
			if err != nil || me == nil || me.Identity.DisplayName != "After write" {
				t.Fatalf("subsequent snapshot missed committed write: %+v, %v", latest, err)
			}
		})
	}
}

func TestFishingLeaderboardCanceledReadReleasesPool(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("cancel-ranking", fixtureFunding)
	for _, board := range []string{"single", "recent_single", "total"} {
		t.Run(board, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			reading := make(chan struct{})
			fixture.service.beforeLeaderboardQuery = func(ctx context.Context, tx *sql.Tx) error {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_fishing_rank_facts`).Scan(&count); err != nil {
					return err
				}
				close(reading)
				<-ctx.Done()
				return ctx.Err()
			}
			defer func() { fixture.service.beforeLeaderboardQuery = nil }()
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				_, err := fixture.service.FishingLeaderboard(ctx, userID, board)
				done <- err
			}()
			defer func() {
				cancel()
				<-finished
			}()
			select {
			case <-reading:
			case err := <-done:
				t.Fatalf("leaderboard failed before cancellation: %v", err)
			case <-ctx.Done():
				t.Fatal("leaderboard did not open its snapshot", ctx.Err())
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled leaderboard returned %v", err)
			}
			// Borrow every reader so an asynchronous rollback must finish; no sleep
			// or instantaneous pool-stat assertion is needed.
			checkCtx, stop := context.WithTimeout(t.Context(), 10*time.Second)
			defer stop()
			var connections []*sql.Conn
			defer func() {
				for _, conn := range connections {
					_ = conn.Close()
				}
			}()
			for range fixture.store.ReadDB().Stats().MaxOpenConnections {
				conn, err := fixture.store.ReadDB().Conn(checkCtx)
				if err != nil {
					t.Fatal("canceled leaderboard retained a reader", err)
				}
				connections = append(connections, conn)
			}
			if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_aggregates`) != 0 {
				t.Fatal("canceled read created ranking state")
			}
		})
	}
}
