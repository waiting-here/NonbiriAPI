package db

import (
	"context"
	"database/sql"
)

func seedInactivity(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inactivity_policy VALUES(1,1,?,0,0,?,NULL)`,
		`{"enabled":false,"decay":{"enabled":false,"inactive_days":null,"interval_days":null,"assets":{"general":null,"game":null}},"protection":{"enabled":false,"inactive_days":null}}`, at)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_activity_state(user_id,observation_started_at) SELECT id,? FROM users`, at)
	return err
}
