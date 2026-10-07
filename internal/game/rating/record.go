package rating

import (
	"context"
	"database/sql"
	"errors"
)

// RecordTx reads the settled result from the game host. It shares the ledger
// transaction, so neither ratings nor a win can survive a failed settlement.
func RecordTx(ctx context.Context, tx *sql.Tx, game, session string) error {
	var state, economy, outcome string
	var winner sql.NullInt64
	var at int64
	if err := tx.QueryRowContext(ctx, `SELECT state,economy,outcome,winner_seat,terminal_at FROM game_duel_sessions WHERE id=? AND game_key=?`, session, game).Scan(&state, &economy, &outcome, &winner, &at); err != nil {
		return err
	}
	if state != "terminal" || economy != "pvp" {
		return errors.New("rating: result is not a settled player match")
	}
	if outcome == "system_cancelled" {
		return nil
	}
	var recorded int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_duel_results WHERE session_id=?`, session).Scan(&recorded); err != nil {
		return err
	}
	if recorded == 2 {
		return nil
	}
	if recorded != 0 {
		return errors.New("rating: incomplete match results")
	}
	var users [2]int64
	before := [2]int{Initial, Initial}
	for seat := range 2 {
		if err := tx.QueryRowContext(ctx, `SELECT user_id FROM game_duel_seats WHERE session_id=? AND seat_no=? AND participant_kind='human'`, session, seat).Scan(&users[seat]); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT rating FROM game_duel_ratings WHERE game_key=? AND user_id=?`, game, users[seat]).Scan(&before[seat])
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	score := 0.5
	if winner.Valid {
		score = 1 - float64(winner.Int64)
	}
	after := Next(before, score)
	for seat, user := range users {
		result := 1
		if winner.Valid {
			result = 0
			if int64(seat) == winner.Int64 {
				result = 2
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_results(session_id,game_key,user_id,result,rating_before,rating_after,settled_at) VALUES(?,?,?,?,?,?,?)`, session, game, user, result, before[seat], after[seat], at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_ratings(game_key,user_id,rating,played,updated_at) VALUES(?,?,?,1,?) ON CONFLICT(game_key,user_id) DO UPDATE SET rating=excluded.rating,played=game_duel_ratings.played+1,updated_at=excluded.updated_at`, game, user, after[seat], at); err != nil {
			return err
		}
	}
	return nil
}
