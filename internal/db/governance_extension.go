package db

import (
	"context"
	"database/sql"
	"errors"
)

func governanceConfigDefaults() map[string]string {
	return map[string]string{
		"level_display_name_6":              "",
		"request_error_body_budget_mib":     "1024",
		"game_fishing_blue_fish_chance_bps": "1000",
	}
}

func GovernanceStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN
 ('image_activity_tasks','request_source_facts','request_error_bodies','observability_state','audit_access_events',
 'anonymous_access_minutes','charity_request_outcomes','risk_audit_minutes','risk_audit_gaps','risk_audit_config',
 'risk_client_rules','economy_audit_checkpoint','economy_audit_buckets',
 'limited_activity_configs','limited_activity_revisions','activity_exchange_state','activity_exchange_receipts',
 'inactivity_policy','user_activity_state','inactivity_runs','inactivity_audits',
 'game_rank_net_rebuild','game_rank_net_rebuild_totals',
 'image_upstream_control','image_upstream_revisions','image_activity_state','image_activity_models',
 'image_model_revisions','image_model_refreshes','image_upstream_resume_audits','image_task_sources')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 31 {
		return false, errors.New("partial governance storage")
	}
	return count == 31, nil
}

func seedGovernanceState(ctx context.Context, tx *sql.Tx, at int64) error {
	present, err := GovernanceStoragePresent(ctx, tx)
	if err != nil || !present {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE donation_keys SET breakdown_started_at=?,unattributed_total_tokens=tokens_used WHERE breakdown_started_at=0`, at); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO observability_state(id,capture_started_at) VALUES(1,?);
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
