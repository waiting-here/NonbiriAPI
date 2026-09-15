package randomness

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ExportForUser excludes spectator-only tables and derives disclosure after
// the owning game exporters have finalized any lazy expiry in the same tx.
func ExportForUser(ctx context.Context, tx *sql.Tx, user, now int64, limit, byteLimit int) ([]Proof, error) {
	if user <= 0 || now < 0 || limit < 1 || limit > 10000 || byteLimit < 1 || byteLimit > 16<<20 {
		return nil, ErrInvalid
	}
	cutoff := now - 30*24*60*60
	rows, err := tx.QueryContext(ctx, `SELECT p.game_key,p.resource_id FROM game_random_proofs p JOIN (
		SELECT id FROM game_fishing_batches WHERE user_id=?1 AND (state='reserved' OR settled_at>?2)
		UNION SELECT id FROM game_linklink_sessions WHERE user_id=?1
		UNION SELECT session_id FROM game_linklink_summaries WHERE user_id=?1 AND terminal_at>?2
		UNION SELECT s.id FROM game_rps_sessions s JOIN game_rps_seats u ON u.session_id=s.id WHERE u.user_id=?1
		UNION SELECT s.session_id FROM game_rps_summaries s JOIN game_rps_summary_seats u ON u.session_id=s.session_id WHERE u.user_id=?1 AND s.terminal_at>?2
		UNION SELECT s.id FROM game_duel_sessions s JOIN game_duel_seats u ON u.session_id=s.id WHERE u.user_id=?1 AND (s.state='active' OR s.terminal_at>?2)
		UNION SELECT s.id FROM game_blackjack_sessions s JOIN game_blackjack_entries u ON u.session_id=s.id WHERE u.user_id=?1 AND (s.terminal_at IS NULL OR s.terminal_at>?2) AND (u.state IN ('seated','playing','settled') OR s.phase='cancelled')
	) owned ON owned.id=p.resource_id ORDER BY p.game_key,p.resource_id LIMIT ?3`, user, cutoff, limit+1)
	if err != nil {
		return nil, err
	}
	type identity struct{ game, id string }
	var ids []identity
	for rows.Next() {
		var i identity
		if err := rows.Scan(&i.game, &i.id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		return nil, ErrLimit
	}
	result, bytes := make([]Proof, 0, len(ids)), 2
	for _, id := range ids {
		p, err := ReadForUser(ctx, tx, id.game, id.id, user, now)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, ErrInvalid
		}
		body, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		bytes += len(body) + 1
		if bytes > byteLimit {
			return nil, ErrLimit
		}
		result = append(result, *p)
	}
	return result, nil
}
