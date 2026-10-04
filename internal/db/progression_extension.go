package db

import (
	"context"
	"database/sql"
)

func seedProgressionState(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO game_statistics_epoch(id,started_at,rules_version) VALUES(1,?,1);
INSERT INTO game_rank_counters(id,next_event_seq) VALUES(1,X'00000000000000000000000000000001')`, at)
	return err
}
