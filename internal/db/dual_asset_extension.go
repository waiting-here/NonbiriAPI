package db

import (
	"context"
	"database/sql"
)

func validateSourceConfig(ctx context.Context, q generationTwoConfigQueryer) error {
	values, err := readGenerationTwoSiteConfigSnapshot(ctx, q)
	if err != nil {
		return err
	}
	return validateGenerationTwoSiteConfigSnapshot(values)
}

// seedGameAccounts preserves all existing identities. Historical dynamic RPS
// accounts need no counterpart; only active queues and sessions get one.
func seedGameAccounts(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO credit_accounts(kind,user_id,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
SELECT a.kind,a.user_id,a.code,'game',0,X'00000000000000000000000000000000',a.created_at,a.updated_at
FROM credit_accounts a
WHERE a.asset_type='general' AND (
 a.kind='user' OR a.code IN ('external','platform','game_fishing_reserve') OR
 a.code IN (SELECT 'rps-queue:'||id FROM game_rps_queue) OR
 a.code IN (SELECT 'rps-session:'||id FROM game_rps_sessions)
)`)
	return err
}
