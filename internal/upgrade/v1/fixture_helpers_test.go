package v1

import (
	"database/sql"
	"strings"
	"testing"
)

const hostileIDTail = "A"

func hostileOID(prefix string) string {
	return prefix + strings.Repeat("A", 21) + hostileIDTail
}

func hostileBlob16(value byte) []byte {
	b := make([]byte, 16)
	b[15] = value
	return b
}

func hostileBlob32(value byte) []byte {
	b := make([]byte, 32)
	b[31] = value
	return b
}

func hostileMustExec(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("SQL failed: %v\n%s", err, query)
	}
	return result
}

func hostileMustLastID(t *testing.T, result sql.Result) int64 {
	t.Helper()
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// hostileNextPK64 reserves a deterministic positive row id for AUTOINCREMENT
// tables whose hostile INSERT guards intentionally reject implicit/zero ids.
// Each hostile database is private to one test, so MAX(id)+1 is sufficient and
// keeps the fixture independent of production ID generators.
func hostileNextPK64(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id),0)+1 FROM ` + table).Scan(&id); err != nil {
		t.Fatalf("next %s id: %v", table, err)
	}
	if id <= 0 {
		t.Fatalf("next %s id is not positive: %d", table, id)
	}
	return id
}

func hostileInsertUser(t *testing.T, db *sql.DB, label string, isAdmin int, at int64) int64 {
	t.Helper()
	zero := hostileBlob16(0)
	result := hostileMustExec(t, db, `
INSERT INTO users (
 discord_id, username, is_admin, donation_credit_mag,
 total_requests, total_uncached_input_tokens, total_cache_write_input_tokens,
 total_cache_read_input_tokens, total_output_tokens,
 total_unknown_usage_requests, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hostile-"+label, "hostile", isAdmin, zero, zero, zero, zero, zero, zero, zero, zero, at, at)
	return hostileMustLastID(t, result)
}

func hostileInsertAccount(t *testing.T, db *sql.DB, kind string, userID any, code any, sign int, mag []byte, at int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "credit_accounts")
	result := hostileMustExec(t, db, `
INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?)`, id, kind, userID, code, sign, mag, at, at)
	return hostileMustLastID(t, result)
}

func hostileInsertFishingBatch(t *testing.T, db *sql.DB, batchID string, userID int64, operationID string) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at
) VALUES(?,?, 'worm',1,1,1,1,?,?, 'reserved',?,0,0,0,0)`,
		batchID, userID, operationID, hostileBlob32(1), hostileBlob16(1))
}

func assertForeignKeyEnforcement(t *testing.T, d *sql.DB) {
	t.Helper()
	var enabled int
	if err := d.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal(enabled, err)
	}
}
