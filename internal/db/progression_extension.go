package db

import (
	"context"
	"database/sql"

	"errors"
)

func ProgressionStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_statistics_epoch','game_rank_counters','game_rank_events','game_rank_totals','activity_loans','abuse_windows','abuse_window_events','abuse_cases','abuse_actions','abuse_evidence')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 10 {
		return false, errors.New("partial progression storage")
	}
	return count == 10, nil
}

func seedProgressionState(ctx context.Context, tx *sql.Tx, at int64) error {
	present, err := ProgressionStoragePresent(ctx, tx)
	if err != nil || !present {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_statistics_epoch(id,started_at,rules_version) VALUES(1,?,1);
INSERT INTO game_rank_counters(id,next_event_seq) VALUES(1,X'00000000000000000000000000000001')`, at)
	return err
}
