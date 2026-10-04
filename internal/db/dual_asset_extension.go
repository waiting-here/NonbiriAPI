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

// seedGameAccounts creates fixed accounts during empty-database bootstrap.
func seedGameAccounts(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at) VALUES
 ('external','external','game',0,zeroblob(16),0,0),
 ('platform','platform','game',0,zeroblob(16),0,0),
 ('platform','game_fishing_reserve','game',0,zeroblob(16),0,0)`)
	return err
}
