package continuity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestGameStartWindowSurvivesRepeatedDeletionAndRestart(t *testing.T) {
	database, identities := fixture(t)
	ctx := context.Background()
	now := time.Unix(100, 123456789)
	user := insertUser(t, database, "456")
	makeLimiter := func() *game.StartLimiter {
		l, err := game.NewStartLimiter(game.StartLimiterConfig{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.AttachContinuity(continuity.StartWindows{Service: identities}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return l
	}
	limiter := makeLimiter()
	for range game.FishingStartsPerMinute {
		tx := transaction(t, database)
		r, _, err := limiter.ReserveTx(ctx, tx, user)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		r.Commit()
	}
	for generation := range 3 {
		commit, abort, err := limiter.BeginUserDeletionContext(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		tx := transaction(t, database)
		if err := limiter.PreserveWindowTx(ctx, tx, user, now.Unix()); err != nil {
			abort()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		commit()
		user = insertUser(t, database, "456")
		if generation == 1 {
			limiter = makeLimiter()
		}
		tx = transaction(t, database)
		if _, _, err := limiter.ReserveTx(ctx, tx, user); !errors.Is(err, game.ErrStartRateLimited) {
			t.Fatal("re-registration reset starts", err)
		}
		tx.Rollback()
		var count int
		database.QueryRow(`SELECT count(*) FROM identity_window_events WHERE kind='game_start'`).Scan(&count)
		if count != game.FishingStartsPerMinute {
			t.Fatal("import doubled count", count)
		}
	}
	now = now.Add(time.Minute + time.Millisecond)
	tx := transaction(t, database)
	r, _, err := limiter.ReserveTx(ctx, tx, user)
	if err != nil {
		t.Fatal("original expiry extended", err)
	}
	tx.Rollback()
	r.Release()
}
