package db

import (
	"context"
	"database/sql"
)

func seedGovernanceState(ctx context.Context, tx *sql.Tx, at int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO observability_state(id,capture_started_at) VALUES(1,?);
 INSERT INTO risk_audit_config(id,threshold_percent,consecutive_minutes,shared_ip_hours,shared_ip_users,revision,updated_at) VALUES(1,80,5,24,3,1,?);
 INSERT INTO economy_audit_checkpoint(id,last_ledger_seq,opening_known,updated_at) VALUES(1,0,1,?)`, at, at, at); err != nil {
		return err
	}
	for _, asset := range []string{"sketch_paper", "sketch_brush"} {
		for _, account := range []struct{ kind, code string }{{"external", "external"}, {"platform", "image_activity_reserve"}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
    VALUES(?,?,?,0,X'00000000000000000000000000000000',?,?)`, account.kind, account.code, asset, at, at); err != nil {
				return err
			}
		}
	}
	if err := seedLimitedActivities(ctx, tx, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_rank_net_rebuild VALUES(1,0,NULL,zeroblob(16))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO image_activity_state(id,revision,upstream_revision,task_rows,updated_at) VALUES(1,1,NULL,0,?)`, at); err != nil {
		return err
	}
	return seedInactivity(ctx, tx, at)
}
