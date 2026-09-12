package db

import (
	"context"
	"testing"
)

func TestDualAssetSeeds(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationTwoSeedManifest(ctx, tx); err != nil {
		t.Fatal(err)
	}
	rows, err := readGenerationTwoFreshConfigSeedRows(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := generationTwoFreshConfigSeedDigest(rows)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh configuration SHA256=%s", digest)
	if err := validateGenerationTwoFreshSeedManifest(ctx, tx); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"external", "platform", "game_fishing_reserve"} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT asset_type) FROM credit_accounts WHERE code=? AND balance_sign=0`, code).Scan(&count); err != nil || count != 2 {
			t.Fatalf("two zero assets for %s: %d, %v", code, count, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
VALUES('platform','charity_reserve','game',0,zeroblob(16),0,0)`); err == nil {
		t.Fatal("game API reserve accepted")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET asset_type='general' WHERE code='platform' AND asset_type='game'`); err == nil {
		t.Fatal("account denomination changed")
	}
	if err := ValidateAssetLedger(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
