package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Only this complete published structure can enter the governance migration.
const preGovernanceManifestHash = "cbab638c0f8c97efd0037f47cdcff58575de714dd47390c9e9e9039a9886f517"

var governanceChangedTables = []string{
	"users", "charity_model_access", "donation_handling", "donations",
	"donation_reviews", "policy_audits", "credit_accounts", "credit_entries", "credit_operations",
	"game_rank_totals",
}

func governanceReplace(source, before, after string) (string, error) {
	if strings.Count(source, before) != 1 {
		return "", errors.New("unrecognized governance source constraint")
	}
	return strings.Replace(source, before, after, 1), nil
}

func governanceTableSQL(table, previous string) (string, error) {
	var changes [][2]string
	switch table {
	case "users":
		changes = append(changes, [2]string{"level BETWEEN 1 AND 5", "level BETWEEN 1 AND 6"})
	case "charity_model_access":
		changes = append(changes, [2]string{"DEFAULT 31", "DEFAULT 63"}, [2]string{"allowed_level_mask BETWEEN 0 AND 31", "allowed_level_mask BETWEEN 0 AND 63"})
	case "game_rank_totals":
		changes = append(changes,
			[2]string{"board IN ('game_charity','bidding','blackjack')", "board IN ('game_charity','bidding','blackjack','game_net_profit','fishing_net_profit','blackjack_net_profit')"},
			[2]string{"board<>'game_charity' OR window='7d'", "board NOT IN ('game_charity','game_net_profit','fishing_net_profit','blackjack_net_profit') OR window='7d'"},
			[2]string{"board='game_charity' OR amount_sign>=0", "board IN ('game_charity','game_net_profit','fishing_net_profit','blackjack_net_profit') OR amount_sign>=0"},
		)
	case "donation_handling", "donations", "donation_reviews", "policy_audits":
		changes = append(changes, [2]string{"'level5'", "'level5','level6','trainee5'"})
	case "credit_accounts":
		changes = append(changes, [2]string{"'platform','forward_reserve','charity_reserve','game_fishing_reserve'", "'platform','forward_reserve','charity_reserve','game_fishing_reserve','image_activity_reserve'"})
		if strings.Contains(previous, "asset_type IN") {
			changes = append(changes, [2]string{"asset_type IN ('general','game')", "asset_type IN ('general','game','sketch_paper','sketch_brush')"})
		}
	case "credit_entries":
		if strings.Contains(previous, "asset_type IN") {
			changes = append(changes, [2]string{"asset_type IN ('general','game')", "asset_type IN ('general','game','sketch_paper','sketch_brush')"})
		}
	case "credit_operations":
		if strings.Count(previous, "'activity_loan'") != 2 {
			return "", errors.New("unrecognized activity operation source")
		}
		previous = strings.ReplaceAll(previous, "'activity_loan'", "'activity_loan','activity_exchange','inactivity_decay'")
		previous = strings.Replace(previous, "'activity_loan'", "'activity_loan','image_reserve','image_settle','image_refund','image_delete_finalize'", 1)
		changes = append(changes,
			[2]string{"source_type IN ('operation','logical_request'", "source_type IN ('image_task','operation','logical_request'"},
			[2]string{" OR (source_type='logical_request'", " OR (source_type='image_task' AND length(source_id)=26 AND substr(source_id,1,4)='img_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='logical_request'"},
			[2]string{" OR (kind='donor_reward'", " OR (kind IN ('image_reserve','image_settle','image_refund','image_delete_finalize') AND source_type='image_task' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='donor_reward'"},
		)
	default:
		return "", errors.New("unknown governance table")
	}
	for _, change := range changes {
		var err error
		previous, err = governanceReplace(previous, change[0], change[1])
		if err != nil {
			return "", fmt.Errorf("%s: %w", table, err)
		}
	}
	return previous, nil
}

func governanceBootstrapSchema(previous string) string {
	for _, table := range governanceChangedTables {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing canonical governance table: " + table)
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			panic("incomplete canonical governance table")
		}
		old := start + body
		target, err := governanceTableSQL(table, old)
		if err != nil || strings.Count(previous, old) != 1 {
			panic("invalid canonical governance table: " + table)
		}
		previous = strings.Replace(previous, old, target, 1)
	}
	for _, table := range []string{"credit_accounts", "credit_entries"} {
		before := "ALTER TABLE " + table + " ADD COLUMN asset_type TEXT NOT NULL DEFAULT 'general' CHECK(asset_type IN ('general','game'));"
		after := strings.Replace(before, "'general','game'", "'general','game','sketch_paper','sketch_brush'", 1)
		var err error
		previous, err = governanceReplace(previous, before, after)
		if err != nil {
			panic(err)
		}
	}
	return previous + governanceAdditiveSchema()
}

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
 'game_rank_net_rebuild','game_rank_net_rebuild_totals')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 23 {
		return false, errors.New("partial governance storage")
	}
	return count == 23, nil
}

func seedGovernanceState(ctx context.Context, tx *sql.Tx, at int64) error {
	present, err := GovernanceStoragePresent(ctx, tx)
	if err != nil || !present {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO observability_state(id,capture_started_at) VALUES(1,?);
 INSERT INTO risk_audit_config VALUES(1,80,5,24,3,1,?);
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
	return seedInactivity(ctx, tx, at)
}

func applyGovernanceExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preGovernanceManifestHash {
		return errors.New("unrecognized governance source manifest")
	}
	// Opening inventory is known only after the retained ledger replays exactly.
	if err := ValidateAssetLedger(ctx, tx); err != nil {
		return err
	}
	type change struct{ table, before, after string }
	var changes []change
	for _, table := range governanceChangedTables {
		c := change{table: table}
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&c.before); err != nil {
			return err
		}
		if c.after, err = governanceTableSQL(table, c.before); err != nil {
			return err
		}
		changes = append(changes, c)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("governance schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, c := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, c.after, c.table, c.before)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("governance schema update count mismatch")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, governanceAdditiveSchema()); err != nil {
		return err
	}
	// Keep historical role snapshots unchanged; migrate only current authority.
	if _, err := tx.ExecContext(ctx, `UPDATE users SET level=6 WHERE level=5;
 UPDATE charity_model_access SET allowed_level_mask=(allowed_level_mask & 15) | ((allowed_level_mask & 16)<<1) | ((allowed_level_mask & 8)<<1);
 INSERT INTO site_config(key,value,updated_at) SELECT 'level_display_name_6',value,updated_at FROM site_config WHERE key='level_display_name_5';
 UPDATE site_config SET value='见习协管' WHERE key='level_display_name_5';
 INSERT INTO site_config(key,value,updated_at) VALUES('request_error_body_budget_mib','1024',0),('game_fishing_blue_fish_chance_bps','1000',0);`); err != nil {
		return err
	}
	return seedGovernanceState(ctx, tx, time.Now().Unix())
}

func governanceAdditiveSchema() string {
	return governanceTablesSchema + riskAuditSchema + economyAuditSchema + governanceGuardsSchema() + charityControlSchema + limitedActivitySchema + inactivitySchema + gameplayGovernanceSchema
}

// The modulo expression operates directly on all 128 bits; SQLite integer
// casts would truncate wide asset amounts.
func governanceWholeAsset(column string) string {
	terms := make([]string, 32)
	coefficient := 1
	for i := 31; i >= 0; i-- {
		terms[i] = fmt.Sprintf("(instr('0123456789ABCDEF',substr(hex(%s),%d,1))-1)*%d", column, i+1, coefficient)
		coefficient = coefficient * 16 % 1000
	}
	return "((" + strings.Join(terms, "+") + ")%1000=0)"
}

func governanceGuardsSchema() string {
	var b strings.Builder
	for _, name := range []string{"legal_hold_steward_read_insert_guard", "legal_hold_steward_read_update_guard"} {
		start := strings.Index(stewardHoldReadSchema, "CREATE TRIGGER "+name)
		if start < 0 {
			panic("missing steward audit guard")
		}
		tail := stewardHoldReadSchema[start:]
		end := strings.Index(tail, "END;")
		if end < 0 {
			panic("incomplete steward audit guard")
		}
		b.WriteString("DROP TRIGGER " + name + ";\n")
		b.WriteString(strings.ReplaceAll(tail[:end+4], "u.level=5", "u.level=6") + "\n")
	}
	b.WriteString(`
DROP TRIGGER credit_account_asset_insert;
CREATE TRIGGER credit_account_asset_insert BEFORE INSERT ON credit_accounts
WHEN (NEW.asset_type='game' AND (NEW.kind='pool' OR NEW.code IN ('forward_reserve','charity_reserve')))
 OR (NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT
  (NEW.kind='user' OR (NEW.kind='external' AND NEW.code='external') OR (NEW.kind='platform' AND NEW.code='image_activity_reserve')))
 OR (NEW.code='image_activity_reserve' AND NEW.asset_type NOT IN ('sketch_paper','sketch_brush'))
BEGIN SELECT RAISE(ABORT,'account code does not support this asset'); END;
`)
	for _, event := range []string{"INSERT", "UPDATE"} {
		b.WriteString("CREATE TRIGGER activity_account_integer_" + strings.ToLower(event) + " BEFORE " + event + " ON credit_accounts\n")
		b.WriteString("WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND NOT " + governanceWholeAsset("NEW.balance_mag") + "\n")
		b.WriteString("BEGIN SELECT RAISE(ABORT,'activity balance is not an integer'); END;\n")
		b.WriteString("CREATE TRIGGER activity_entry_integer_" + strings.ToLower(event) + " BEFORE " + event + " ON credit_entries\n")
		b.WriteString("WHEN NEW.asset_type IN ('sketch_paper','sketch_brush') AND (NOT " + governanceWholeAsset("NEW.delta_mag") + " OR (NEW.balance_after_mag IS NOT NULL AND NOT " + governanceWholeAsset("NEW.balance_after_mag") + "))\n")
		b.WriteString("BEGIN SELECT RAISE(ABORT,'activity entry is not an integer'); END;\n")
	}
	return b.String()
}
