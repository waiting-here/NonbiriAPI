package inactivity

import (
	"context"
	"database/sql"
	"strings"
)

const accountStateQuery = `SELECT u.id,u.is_admin,COALESCE(u.level,u.auto_level),u.is_banned,u.banned_until,u.ban_kind,u.revision,s.observation_started_at,s.last_active_at,s.activity_seq,s.activity_epoch,s.schedule_revision,s.next_due_at,s.last_decay_at FROM users u JOIN user_activity_state s ON s.user_id=u.id`

func scanAccountState(row interface{ Scan(...any) error }) (accountState, error) {
	var a accountState
	err := row.Scan(&a.id, &a.admin, &a.level, &a.banned, &a.until, &a.banKind, &a.revision, &a.state.ObservationStartedAt, &a.state.LastActiveAt, &a.state.ActivitySeq, &a.state.ActivityEpoch, &a.state.ScheduleRevision, &a.state.NextDueAt, &a.state.LastDecayAt)
	return a, err
}

func readAccount(ctx context.Context, tx *sql.Tx, userID int64) (accountState, error) {
	return scanAccountState(tx.QueryRowContext(ctx, accountStateQuery+` WHERE u.id=?`, userID))
}

// readAccountStates reads the selected preview page in its existing transaction.
func readAccountStates(ctx context.Context, tx *sql.Tx, userIDs []int64) (map[int64]accountState, error) {
	states := make(map[int64]accountState, len(userIDs))
	if len(userIDs) == 0 {
		return states, nil
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, accountStateQuery+` WHERE u.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAccountState(rows)
		if err != nil {
			return nil, err
		}
		states[a.id] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(states) != len(userIDs) {
		return nil, sql.ErrNoRows
	}
	return states, rows.Close()
}
