package randomness

import (
	"context"
	"database/sql"
)

// ReadForUser derives both ownership and disclosure from authoritative parent
// rows in one snapshot. Callers must also revalidate the live login in this tx.
func ReadForUser(ctx context.Context, tx *sql.Tx, game, resource string, user, now int64) (*Proof, error) {
	if user <= 0 || now < 0 || now > 253402300799 || !validText(resource, 64) {
		return nil, ErrInvalid
	}
	cutoff := now - 30*24*60*60
	var query string
	var args []any
	switch game {
	case "fishing":
		query = `SELECT state IN ('committed','released') FROM game_fishing_batches WHERE id=? AND user_id=? AND (state='reserved' OR settled_at>?)`
		args = []any{resource, user, cutoff}
	case "linklink":
		query = `SELECT 0 FROM game_linklink_sessions WHERE id=? AND user_id=? UNION ALL SELECT 1 FROM game_linklink_summaries WHERE session_id=? AND user_id=? AND terminal_at>?`
		args = []any{resource, user, resource, user, cutoff}
	case "rps":
		query = `SELECT 0 FROM game_rps_sessions s JOIN game_rps_seats p ON p.session_id=s.id WHERE s.id=? AND p.user_id=? UNION ALL SELECT 1 FROM game_rps_summaries s JOIN game_rps_summary_seats p ON p.session_id=s.session_id WHERE s.session_id=? AND p.user_id=? AND s.terminal_at>?`
		args = []any{resource, user, resource, user, cutoff}
	case "bidding", "likes":
		query = `SELECT s.state='terminal' FROM game_duel_sessions s JOIN game_duel_seats p ON p.session_id=s.id WHERE s.game_key=? AND s.id=? AND p.user_id=? AND (s.state='active' OR s.terminal_at>?)`
		args = []any{game, resource, user, cutoff}
	case "blackjack":
		query = `SELECT s.phase IN ('result','cancelled') FROM game_blackjack_sessions s WHERE s.id=? AND (s.terminal_at IS NULL OR s.terminal_at>?) AND ((s.started_at<=? AND s.started_at+60>?) OR EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=s.id AND e.user_id=? AND (e.state IN ('seated','playing','settled') OR s.phase='cancelled')))`
		args = []any{resource, cutoff, now, now, user}
	default:
		return nil, ErrInvalid
	}
	var terminal bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&terminal); err != nil {
		return nil, err
	}
	return PublicTx(ctx, tx, game, resource, terminal)
}
