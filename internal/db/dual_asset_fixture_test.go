package db

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Older structural fixtures are still checked against their independent
// deployed digests. This helper removes only the asset extension; populated
// upgrade acceptance additionally uses a database made by the prior binary.
func makePreAssetFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreDuelFixture(t, database)
	var present int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('credit_accounts') WHERE name='asset_type'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present == 0 {
		return
	}
	var nonzero int
	if err := database.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE asset_type='game' AND balance_sign<>0`).Scan(&nonzero); err != nil || nonzero != 0 {
		t.Fatalf("fixture has game money: %d, %v", nonzero, err)
	}
	hostileMustExec(t, database, `DELETE FROM credit_accounts WHERE asset_type='game'`)
	for _, line := range strings.Split(dualAssetGuardsSchema, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "CREATE" && fields[1] == "TRIGGER" {
			hostileMustExec(t, database, "DROP TRIGGER "+hostileQuoteIdent(fields[2]))
		}
	}
	for _, line := range strings.Split(dualAssetIndexesSchema, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "CREATE" {
			offset := 2
			if fields[1] == "UNIQUE" {
				offset = 3
			}
			hostileMustExec(t, database, "DROP INDEX "+hostileQuoteIdent(fields[offset]))
		}
	}
	hostileMustExec(t, database, `DROP TABLE game_onboarding_holds; DROP TABLE game_onboarding_completions; DROP TABLE game_checkins`)
	columns := strings.Split(strings.TrimSpace(dualAssetColumnsSchema), "\n")
	tables := map[string]bool{"credit_operations": true, "donation_reviews": true}
	for i := len(columns) - 1; i >= 0; i-- {
		fields := strings.Fields(columns[i])
		if len(fields) < 7 || fields[0] != "ALTER" || fields[3] != "ADD" {
			t.Fatal("invalid fixture column list")
		}
		table, column := fields[2], fields[5]
		hostileMustExec(t, database, "ALTER TABLE "+hostileQuoteIdent(table)+" DROP COLUMN "+hostileQuoteIdent(column))
		tables[table] = true
	}
	var version int
	if err := database.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `BEGIN; PRAGMA writable_schema=ON`)
	for table := range tables {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(generationTwoBaseSchema, start)
		if !ok {
			t.Fatalf("missing prior table %s", table)
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			t.Fatal("missing table terminator")
		}
		ddl := strings.ReplaceAll(start+body, ",'game_onboarding_reward'", "")
		ddl = strings.ReplaceAll(ddl, ",'failure_streak_reset'", "")
		hostileMustExec(t, database, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=?`, ddl, table)
	}
	hostileMustExec(t, database, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET; COMMIT`, version+1))
	for _, name := range []string{"idx_credit_accounts_user", "idx_credit_accounts_code", "idx_rps_queue_match"} {
		for _, line := range strings.Split(generationTwoBaseSchema, "\n") {
			if strings.Contains(line, "INDEX "+name+" ") {
				hostileMustExec(t, database, strings.TrimSpace(line))
			}
		}
	}
	for _, line := range strings.Split(dualAssetGuardsSchema, "\n") {
		if !strings.HasPrefix(line, "DROP TRIGGER ") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, "DROP TRIGGER "), ";")
		start := strings.Index(generationTwoBaseSchema, "CREATE TRIGGER "+name+" ")
		if start < 0 {
			t.Fatalf("missing prior trigger %s", name)
		}
		tail := generationTwoBaseSchema[start:]
		end := strings.Index(tail, "END;")
		hostileMustExec(t, database, tail[:end+4])
	}
	for key := range dualAssetConfigDefaults() {
		hostileMustExec(t, database, `DELETE FROM site_config WHERE key=?`, key)
	}
}

func hostileGameAwardEntry(t *testing.T, database *sql.DB, userID int64, operationID string) {
	t.Helper()
	wallet := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,user_id,asset_type,balance_sign,balance_mag,created_at,updated_at)
VALUES('user',?,'game',1,?,0,0)`, userID, hostileBlob16(1)))
	external := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
VALUES('external','external','game',-1,?,0,0)`, hostileBlob16(1)))
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag)
VALUES(?,0,?,'user','game',1,?)`, operationID, wallet, hostileBlob16(1))
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
VALUES(?,1,?,'external','game',-1,?,-1,?)`, operationID, external, hostileBlob16(1), hostileBlob16(1))
}
