package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The complete deployed structure immediately before dual assets.
const preBetaFourManifestHash = "8957732c1d2bbd892ab204de9b1b1c95a0bd7df06235554023b12e694f6cf6a5"

func dualAssetConfigDefaults() map[string]string {
	return map[string]string{
		"game_checkin_mode":             CheckinModeDisabled,
		"game_checkin_award_min_milli":  "40000000",
		"game_checkin_award_max_milli":  "60000000",
		"game_credits_cap_milli":        "250000000",
		"game_fishing_rake_platform_bp": "100",
		"game_fishing_rake_welfare_bp":  "100",
		"game_fishing_rake_thursday_bp": "100",
	}
}

func priorAssetOptionalConfig(key string) bool {
	switch key {
	case "game_checkin_mode", "game_checkin_award_min_milli", "game_checkin_award_max_milli", "game_credits_cap_milli",
		"game_fishing_rake_platform_bp", "game_fishing_rake_welfare_bp", "game_fishing_rake_thursday_bp",
		"game_fishing_rtp", "game_fishing_rtp_premium":
		return true
	default:
		return false
	}
}

func validateAssetSourceConfig(ctx context.Context, q generationTwoConfigQueryer, prior bool) error {
	values, err := readGenerationTwoSiteConfigSnapshot(ctx, q)
	if err != nil {
		return err
	}
	if prior {
		for key, value := range dualAssetConfigDefaults() {
			if _, exists := values[key]; !exists {
				values[key] = value
			}
		}
		for key, value := range map[string]string{"game_fishing_rtp": "90", "game_fishing_rtp_premium": "88"} {
			if _, exists := values[key]; !exists {
				values[key] = value
			}
		}
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

func extendDualAssetSchema(ctx context.Context, tx *sql.Tx) error {
	// These two closed CHECK sets need SQLite's schema-text procedure. All
	// columns, new tables, indexes and triggers below use ordinary DDL.
	type change struct{ table, previous, target string }
	changes := make([]change, 0, 2)
	for _, table := range []string{"credit_operations", "donation_reviews"} {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(generationTwoBaseSchema, start)
		if !ok {
			return errors.New("missing canonical asset schema")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			return errors.New("incomplete canonical asset schema")
		}
		target := start + body
		added, count := ",'game_onboarding_reward'", 2
		if table == "donation_reviews" {
			added, count = ",'failure_streak_reset'", 1
		}
		if strings.Count(target, added) != count {
			return errors.New("asset schema CHECK extension is missing")
		}
		previous := strings.ReplaceAll(target, added, "")
		var actual string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&actual); err != nil {
			return err
		}
		if actual != previous {
			return errors.New("unrecognized prior asset schema")
		}
		changes = append(changes, change{table, previous, target})
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("asset schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, change := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, change.target, change.table, change.previous)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("asset schema extension did not update one table")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, dualAssetSchema); err != nil {
		return err
	}
	if err := seedGameAccounts(ctx, tx); err != nil {
		return err
	}
	defaults := dualAssetConfigDefaults()
	// Older schemas that omitted RTP retain their historical fallback. An
	// existing value, including the former defaults, is never overwritten.
	defaults["game_fishing_rtp"] = "90"
	defaults["game_fishing_rtp_premium"] = "88"
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0) ON CONFLICT(key) DO NOTHING`, key, defaults[key]); err != nil {
			return err
		}
	}
	return nil
}
