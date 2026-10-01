package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testNow int64 = 1700000000

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test secret vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbtest.EnsureOwnerOnlyParent(t, path)
	st, err := Open(path, vault)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func privateDBDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dbdir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create private db dir: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod private db dir: %v", err)
	}
	return dir
}

// copyPrivateSQLiteTestImage gives each case its own database file. Callers
// must finish writing and close the source before passing its immutable image.
func copyPrivateSQLiteTestImage(t *testing.T, image []byte) string {
	t.Helper()
	if len(image) < 100 {
		t.Fatal("SQLite test image is too short")
	}
	path := filepath.Join(privateDBDir(t), "fixture.sqlite")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create private SQLite test copy: %v", err)
	}
	if _, err := file.Write(image); err != nil {
		_ = file.Close()
		t.Fatalf("write private SQLite test copy: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close private SQLite test copy: %v", err)
	}
	return path
}

var generationTwoConstraintImage struct {
	once       sync.Once
	image      []byte
	schemaHash [32]byte
	imageHash  [32]byte
}

// Keep only immutable bytes after the first builder's temporary directory
// is cleaned up. Every consumer opens a private file, never a shared database.
func generationTwoConstraintImageForTest(t *testing.T) []byte {
	t.Helper()
	generationTwoConstraintImage.once.Do(func() {
		image := snapshotGenerationTwoDDLTestImage(t, openGenerationTwoDDLForTest(t))
		generationTwoConstraintImage.image = image
		generationTwoConstraintImage.schemaHash = sha256.Sum256([]byte(generationTwoSchema))
		generationTwoConstraintImage.imageHash = sha256.Sum256(image)
	})
	image := generationTwoConstraintImage.image
	if len(image) < 100 || generationTwoConstraintImage.schemaHash != sha256.Sum256([]byte(generationTwoSchema)) ||
		generationTwoConstraintImage.imageHash != sha256.Sum256(image) {
		t.Fatal("current DDL fixture image is missing or changed")
	}
	return image
}

func openGenerationTwoConstraintFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, _ := openGenerationTwoDDLTestImage(t, generationTwoConstraintImageForTest(t))
	return database
}

// snapshotGenerationTwoDDLTestImage closes a current DDL fixture before
// reading its standalone image. It is for constraint matrices whose inputs
// do not need to exercise fresh bootstrap, upgrade, recovery or file locking.
func snapshotGenerationTwoDDLTestImage(t *testing.T, database *sql.DB) []byte {
	t.Helper()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil || generationManifestDigest(manifest) != PinnedGenerationTwoManifestHash {
		t.Fatalf("DDL fixture manifest=%s, err=%v", generationManifestDigest(manifest), err)
	}
	assertGenerationTwoDDLTestConnection(t, database)
	path := filepath.Join(privateDBDir(t), "baseline.sqlite")
	if _, err := database.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatalf("snapshot DDL fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close DDL fixture: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("secure DDL fixture image: %v", err)
	}
	assertPrivateSQLiteTestImageFile(t, path)
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read DDL fixture image: %v", err)
	}
	if !bytes.HasPrefix(image, []byte("SQLite format 3\x00")) {
		t.Fatal("DDL fixture image lacks a SQLite header")
	}
	return image
}

func openGenerationTwoDDLTestImage(t *testing.T, image []byte) (*sql.DB, string) {
	t.Helper()
	path := copyPrivateSQLiteTestImage(t, image)
	assertPrivateSQLiteTestImageFile(t, path)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open DDL fixture copy: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close DDL fixture copy: %v", err)
		}
		assertPrivateSQLiteTestImageFile(t, path)
	})
	// Match the original in-memory fixture's connection settings. MEMORY
	// also keeps each copy free of an on-disk journal or WAL/SHM files.
	if _, err := database.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=MEMORY;`); err != nil {
		t.Fatalf("configure DDL fixture copy: %v", err)
	}
	assertGenerationTwoDDLTestConnection(t, database)
	return database, path
}

func assertGenerationTwoDDLTestConnection(t *testing.T, database *sql.DB) {
	t.Helper()
	var foreignKeys int
	var journalMode string
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("DDL fixture foreign_keys=%d, err=%v", foreignKeys, err)
	}
	if err := database.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil || journalMode != "memory" {
		t.Fatalf("DDL fixture journal_mode=%s, err=%v", journalMode, err)
	}
	if connections := database.Stats().MaxOpenConnections; connections != 1 {
		t.Fatalf("DDL fixture MaxOpenConnections=%d", connections)
	}
}

func assertPrivateSQLiteTestImageFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("private SQLite test image is not a regular file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private SQLite test image mode=%o", info.Mode().Perm())
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("private SQLite test image has sidecar %s: %v", suffix, err)
		}
	}
}

// seedUserRaw inserts the minimum valid Generation 2 identity row. It writes
// the fixed-width counters and revision explicitly so a fixture cannot rely
// on a retired schema default or silently bypass a NOT NULL contract.
func seedUserRaw(t *testing.T, st *Store, discordID string) int64 {
	t.Helper()
	zero := make([]byte, 16)
	res, err := st.DB().Exec(`
INSERT INTO users (
 discord_id, username, donation_credit_mag,
 total_requests, total_uncached_input_tokens, total_cache_write_input_tokens,
 total_cache_read_input_tokens, total_output_tokens,
 total_unknown_usage_requests, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		discordID, "tester", zero, zero, zero, zero, zero, zero, zero, zero, testNow, testNow)
	if err != nil {
		t.Fatalf("seed Generation 2 user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user last insert id: %v", err)
	}
	return id
}

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := st.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

const (
	hostileTimeMax  int64 = 253402300799
	hostileInt64Max int64 = 9223372036854775807
	hostileIDTail         = "Q"
)

func hostileOID(prefix string) string {
	return prefix + strings.Repeat("A", 21) + hostileIDTail
}

func hostileBadOID(prefix string) string {
	return prefix + strings.Repeat("A", 21) + "B"
}

func hostileOIDVariant(prefix string, marker byte, tail byte) string {
	body := []byte(strings.Repeat("A", 22))
	body[0] = marker
	body[len(body)-1] = tail
	return prefix + string(body)
}

func hostileBlob16(value byte) []byte {
	b := make([]byte, 16)
	b[15] = value
	return b
}

func hostileHigh128() []byte {
	b := make([]byte, 16)
	b[0] = 0x80
	return b
}

func hostileMaxU128() []byte {
	return []byte(strings.Repeat("\xff", 16))
}

func hostileMaxSM128() []byte {
	b := hostileMaxU128()
	b[0] = 0x7f
	return b
}

func hostileTwoTo63U128() []byte {
	b := make([]byte, 16)
	b[8] = 0x80
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

func hostileMustFail(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("hostile SQL was accepted:\n%s", query)
	}
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

func hostileQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func hostileCompactSQL(sqlText string) string {
	return strings.Join(strings.Fields(strings.ToLower(sqlText)), "")
}

func hostileInlineIntegerDefense(compact, column string) bool {
	column = strings.ToLower(column)
	if strings.Contains(compact, "typeof("+column+")") && strings.Contains(compact, "integer") {
		return true
	}
	for _, prefix := range []string{"(", ",", "check("} {
		if strings.Contains(compact, prefix+column+"in(") {
			return true
		}
	}
	return false
}

func hostileIntegerColumnHasFK(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(` + hostileQuoteIdent(table) + `)`)
	if err != nil {
		t.Fatalf("foreign keys for %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int
		var parent, child, parentColumn, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &child, &parentColumn, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign key for %s: %v", table, err)
		}
		if child == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign keys for %s: %v", table, err)
	}
	return false
}

func hostileIntegerColumnHasInsertUpdateTypeGuards(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`
SELECT sql FROM sqlite_schema
WHERE type='trigger' AND tbl_name=? AND sql IS NOT NULL`, table)
	if err != nil {
		t.Fatalf("integer guards for %s: %v", table, err)
	}
	defer rows.Close()
	needle := "typeof(new." + strings.ToLower(column) + ")"
	insertGuard, updateGuard := false, false
	for rows.Next() {
		var triggerSQL string
		if err := rows.Scan(&triggerSQL); err != nil {
			t.Fatalf("scan integer guard for %s.%s: %v", table, column, err)
		}
		compact := hostileCompactSQL(triggerSQL)
		if !strings.Contains(compact, needle) || !strings.Contains(compact, "integer") {
			continue
		}
		if strings.Contains(compact, "beforeinsert") {
			insertGuard = true
		}
		if strings.Contains(compact, "beforeupdate") {
			updateGuard = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate integer guards for %s.%s: %v", table, column, err)
	}
	return insertGuard && updateGuard
}

func hostileHasIndexPrefix(t *testing.T, db *sql.DB, table string, want ...string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + hostileQuoteIdent(table) + `)`)
	if err != nil {
		t.Fatalf("indexes for %s: %v", table, err)
	}
	var indexes []string
	for rows.Next() {
		var seq, unique, partial int
		var origin string
		var name string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			t.Fatalf("scan indexes for %s: %v", table, err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate indexes for %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close indexes for %s: %v", table, err)
	}
	for _, index := range indexes {
		columns, err := db.Query(`PRAGMA index_info(` + hostileQuoteIdent(index) + `)`)
		if err != nil {
			t.Fatalf("index_info %s: %v", index, err)
		}
		var got []string
		for columns.Next() {
			var seq, cid int
			var name string
			if err := columns.Scan(&seq, &cid, &name); err != nil {
				columns.Close()
				t.Fatalf("scan index_info %s: %v", index, err)
			}
			got = append(got, name)
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			t.Fatalf("iterate index_info %s: %v", index, err)
		}
		if err := columns.Close(); err != nil {
			t.Fatalf("close index_info %s: %v", index, err)
		}
		if len(got) < len(want) {
			continue
		}
		matches := true
		for i := range want {
			if got[i] != want[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
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

func hostileInsertEndpoint(t *testing.T, db *sql.DB, userID int64, base string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,0,0)`, userID, base)
	return hostileMustLastID(t, result)
}

func hostileInsertSecret(t *testing.T, db *sql.DB, base string, at int64) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
	VALUES(?,?,'openai-compatible',?,?)`,
		hostileBlob16(byte(at)), base, "nbsec:v2:hostile", at)
	return hostileMustLastID(t, result)
}

func hostileInsertEndpointKey(t *testing.T, db *sql.DB, endpointID, secretID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "endpoint_keys")
	result := hostileMustExec(t, db, `
INSERT INTO endpoint_keys(
 id,endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
 enabled,force_store_false,revision,created_at,updated_at
) VALUES(?,?,?,?,'head','tail','',1,0,1,0,0)`,
		id, endpointID, secretID, hostileBlob32(1))
	return hostileMustLastID(t, result)
}

func hostileInsertLogicalRequest(t *testing.T, db *sql.DB, id string, userID any, route string, limit int64) {
	t.Helper()
	remaining := hostileBlob16(1)
	if route == "charity_chat_completions" {
		remaining = hostileBlob16(byte(limit + 1))
	} else if route == "model_discovery" {
		remaining = hostileBlob16(0)
	}
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,
 accounting_state,account_reserved_milli,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'model','accepted',?,'none',0,'user',?,0)`,
		id, userID, route, limit, remaining)
}

func hostileInsertTerminalRequest(t *testing.T, db *sql.DB, id string, userID any, route, resultClass string, status any, errorCode any) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,
 caller_status,caller_error_code,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at,terminal_at
) VALUES(?,?,?,'model','terminal',1,?,?,?,'none','user',?,0,1)`,
		id, userID, route, resultClass, status, errorCode, hostileBlob16(0))
}

func hostileInsertOperation(t *testing.T, db *sql.DB, id string, ledgerSeq int64, kind, sourceType, sourceID string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustExec(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,?,?,?,?,?,0,?,0)`, id, ledgerSeq, kind, sourceType, sourceID, zero, zero)
}

func hostileInsertReportCase(t *testing.T, db *sql.DB, id string, fingerprint []byte, status, progress string, at int64) {
	t.Helper()
	var terminalAt any
	if status == "approved" || status == "rejected" {
		terminalAt = at + 1
	}
	hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,NULL,0,0,0,0,0,0,0,?,?)`,
		id, fingerprint, status, progress, at, terminalAt)
}

func hostileInsertDonation(t *testing.T, db *sql.DB, userID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donations")
	result := hostileMustExec(t, db, `
INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,?,'pending',1,'','',0,0)`, id, userID)
	return hostileMustLastID(t, result)
}

func hostileInsertDonationKey(t *testing.T, db *sql.DB, donationID int64, endpointKeyID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donation_keys")
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	sourceKeyID := int64(0)
	if raw, ok := endpointKeyID.(int64); ok && raw > 0 {
		sourceKeyID = raw
	} else {
		// Tombstone rows keep the original physical key identity without an FK;
		// any positive value satisfies the NOT NULL boundary in this fixture.
		sourceKeyID = hostileNextPK64(t, db, "endpoint_keys")
	}
	result := hostileMustExec(t, db, `
INSERT INTO donation_keys(
 id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_limit_mag,call_limit_mag,token_limit_mag,price_used_mag,price_reserved_mag,
 calls_used,calls_reserved,tokens_used,tokens_reserved,token_reserve,enabled,failure_disabled,
 failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
) VALUES(?,?,?,'head','tail','https://upstream.example/v1','openai-compatible',NULL,NULL,NULL,?,?,?,?,?, ?,0,1,0,?,?,?,?,'',0,0,NULL,NULL,?,?)`,
		id, donationID, endpointKeyID, zero, zero, zero, zero, zero, zero, zero, one, one, zero, sourceKeyID, hostileBlob32(7))
	return hostileMustLastID(t, result)
}

func hostileInsertRequestLog(t *testing.T, db *sql.DB, reqID string, userID any, route string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO request_logs(
 logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,
 started_at
) VALUES(?,?, 'model','upstream',?,'https://upstream.example/v1',0)`, reqID, userID, route)
	return hostileMustLastID(t, result)
}

func hostileInsertAttempt(t *testing.T, db *sql.DB, claimID string, logID int64, seq int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at
) VALUES(?,?,?,'openai-compatible','https://upstream.example/v1','upstream',
 'response',200,0,0,0,0,0,0,1)`, claimID, logID, seq)
}

func hostileInsertMaintenanceEvent(t *testing.T, db *sql.DB, id string, adminID int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,NULL,'admin','enable','hostile',0)`, id, adminID)
}

func hostileInsertAnnouncementAudit(t *testing.T, db *sql.DB, adminID int64, at int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "announcement_audits")
	result := hostileMustExec(t, db, `
INSERT INTO announcement_audits(
 id,announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(?,?,?,'create',0,1,'hostile',?,?)`, id, hostileOID("ann_"), adminID, at, at+7776000)
	return hostileMustLastID(t, result)
}

func hostileInsertReportMaterial(t *testing.T, db *sql.DB, caseID string) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "report_materials")
	result := hostileMustExec(t, db, `
INSERT INTO report_materials(id,case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?, '', ?,0)`, id, caseID, hostileBlob32(2), make([]byte, 45))
	return hostileMustLastID(t, result)
}

func hostileInsertReportTarget(t *testing.T, db *sql.DB, caseID, id string) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,connector_type,canonical_base_url,
 key_display_head,key_display_tail,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,NULL,?,?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`,
		id, caseID, hostileNextPK64(t, db, "endpoint_keys"), hostileBlob32(3))
}

func hostileInsertReportDecision(t *testing.T, db *sql.DB, caseID string, adminID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "report_decisions")
	result := hostileMustExec(t, db, `
INSERT INTO report_decisions(id,case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,1,1,?,'reject','hostile',0)`, id, caseID, adminID)
	return hostileMustLastID(t, result)
}

func hostileInsertDonationReview(t *testing.T, db *sql.DB, donationID int64, adminID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donation_reviews")
	result := hostileMustExec(t, db, `
INSERT INTO donation_reviews(id,donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,1,?,'admin','note_update','hostile',0)`, id, donationID, adminID)
	return hostileMustLastID(t, result)
}

func hostileInsertRPSAccount(t *testing.T, db *sql.DB, sessionID string) int64 {
	t.Helper()
	return hostileInsertAccount(t, db, "platform", nil, "rps-session:"+sessionID, 0, hostileBlob16(0), 0)
}

func hostileInsertRPSSession(t *testing.T, db *sql.DB, id string, phase, state string, accountID int64) {
	t.Helper()
	hostileInsertRPSSessionWithReason(t, db, id, phase, state, accountID, "quick_resolved")
}

func hostileInsertRPSSessionWithReason(t *testing.T, db *sql.DB, id string, phase, state string, accountID int64, reason string) {
	t.Helper()
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	terminal := state == "terminal_processing"
	var poolBase, plan, dealerRaise, deadline, terminalOperationID, terminalReason any
	if terminal {
		terminalOperationID = hostileOID("op_")
		terminalReason = reason
	} else {
		deadline = int64(120)
		if phase == "paid_pool_gesture" || phase == "free_pool_gesture" {
			poolBase = one
		} else {
			plan = one
		}
		if phase == "followers" {
			dealerRaise = one
		}
	}
	hostileMustExec(t, db, `
INSERT INTO game_rps_sessions(
 id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
 ledger_rows_remaining,dealer_seat,base_milli,platform_bp,welfare_bp,thursday_bp,
 gesture_seconds,dealer_seconds,follower_seconds,player_pool,permanent_multiplier,
 pool_base_multiplier,current_plan_multiplier,dealer_raise,base_round_count,paid_tie_count,
 free_tie_count,paid_pool_streak,free_pool_streak,platform_cut_total,welfare_cut_total,
 thursday_cut_total,welfare_carry_total,reminder_state,phase_deadline,health_epoch,
 recent_events_blob,recent_first_seq,recent_last_seq,recent_event_count,terminal_operation_id,
 terminal_retry_attempt_count,terminal_next_retry_at,terminal_last_error_class,started_at,terminal_reason
) VALUES(?,?,'quick',1,?,?,?,?,?, ?,?,0,5,0,0,0,20,15,15,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, ?,0,X'',?,?,0,?, ?,NULL,NULL,100,?)`,
		id, accountID, state, phase, one, one, one, zero, one,
		zero, one, poolBase, plan, dealerRaise, zero, zero, zero, zero, zero, zero, zero, zero, zero,
		"none", deadline, zero, zero, terminalOperationID, zero, terminalReason)
}

const hostileRPSSeatInsertSQL = `
INSERT INTO game_rps_seats(
 session_id,seat_no,user_id,deletion_state,starting_balance,current_balance,current_round_input,
 current_all_in,current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq,total_input,
 total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,
 paper_count,timeout_count,stats_applied
) VALUES(?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`

func hostileInsertRPSSeat(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, lastPhase any, terminalReturn any, netSign any, netMag any, deletionState string) {
	t.Helper()
	var gesturePhase any
	if envelope != nil {
		gesturePhase = lastPhase
	}
	hostileInsertRPSSeatWithActions(t, db, sessionID, seatNo, userID, envelope, gesturePhase, nil, lastPhase, terminalReturn, netSign, netMag, deletionState)
}

func hostileInsertRPSSeatWithActions(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, gesturePhase, followerAction, lastPhase any, terminalReturn any, netSign any, netMag any, deletionState string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustExec(t, db, hostileRPSSeatInsertSQL,
		sessionID, seatNo, userID, deletionState, zero, zero, zero, envelope, gesturePhase, followerAction, lastPhase,
		hostileBlob32(0), hostileBlob32(0), terminalReturn, netSign, netMag, zero, zero, zero, zero)
}

func hostileMustFailRPSSeatWithActions(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, gesturePhase, followerAction, lastPhase any, deletionState string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustFail(t, db, hostileRPSSeatInsertSQL,
		sessionID, seatNo, userID, deletionState, zero, zero, zero, envelope, gesturePhase, followerAction, lastPhase,
		hostileBlob32(0), hostileBlob32(0), nil, nil, nil, zero, zero, zero, zero)
}

func hostileInsertRPSQueue(t *testing.T, db *sql.DB, marker byte, mode string, reserved, remaining []byte) (string, int64) {
	t.Helper()
	queueID := hostileOIDVariant("rpsq_", marker, 'Q')
	accountID := hostileInsertAccount(t, db, "platform", nil, "rps-queue:"+queueID, 0, hostileBlob16(0), 0)
	hostileMustExec(t, db, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at
) VALUES(?,?,? ,?, ?,? ,?, ?, ?, ?,0,0)`, queueID,
		hostileInsertUser(t, db, "queue-"+string(marker), 0, 0), accountID, mode,
		hostileBlob16(1), hostileOIDVariant("op_", marker, 'Q'), reserved, remaining,
		hostileBlob32(marker), hostileBlob32(marker+1))
	return queueID, accountID
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

func hostileInsertSharedPool(t *testing.T, db *sql.DB, id, poolType, periodID string, accountID int64) {
	t.Helper()
	var period any
	if periodID != "" {
		period = periodID
	}
	hostileMustExec(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?,?,?,?,'open',1,0)`, id, poolType, period, accountID)
}

func hostileInsertThursdayPeriod(t *testing.T, db *sql.DB, id, currentPool, nextPool string, state string) {
	t.Helper()
	one := hostileBlob16(1)
	zero := hostileBlob16(0)
	var started, terminal, remaining any
	remaining = one
	if state == "settled" {
		started, terminal, remaining = int64(1), int64(2), zero
	}
	hostileMustExec(t, db, `
INSERT INTO thursday_periods(
 id,period_key,state,revision,opens_at,closes_at,literature,entry_milli,per_user_limit,
 platform_bp,welfare_bp,next_pool_bp,current_pool_id,next_pool_id,settlement_cursor,
 ledger_rows_remaining,frozen_pool_mag,frozen_contribution_count,eligible_contribution_count,
 platform_cut_mag,welfare_cut_mag,next_cut_mag,payout_total_mag,rollover_mag,created_at,
 started_settlement_at,terminal_at
) VALUES(?,?,?,1,0,86400,'',1,1,0,0,0,?,?,NULL,?,?,?,?,?,?,?, ?,?,0,?,?)`,
		id, "1970-01-01", state, currentPool, nextPool, remaining, zero, zero, zero, zero, zero, zero, zero, zero, started, terminal)
}

func hostileInsertLegalHold(t *testing.T, db *sql.DB, id, objectKind, objectRef string, adminID int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,?,?,'active',1,'hostile',?,100,200)`, id, objectKind, objectRef, adminID)
}

func hostileInsertThursdayFixture(t *testing.T, db *sql.DB) (string, string, string) {
	t.Helper()
	periodID := hostileOIDVariant("thu_", 'T', 'Q')
	currentPoolID := hostileOIDVariant("pol_", 'C', 'Q')
	nextPoolID := hostileOIDVariant("pol_", 'N', 'Q')
	hostileMustExec(t, db, `PRAGMA defer_foreign_keys=ON`)
	hostileMustExec(t, db, `BEGIN`)
	currentAccount := hostileInsertAccount(t, db, "pool", nil, "pool:"+currentPoolID, 0, hostileBlob16(0), 0)
	nextAccount := hostileInsertAccount(t, db, "pool", nil, "pool:"+nextPoolID, 0, hostileBlob16(0), 0)
	hostileInsertSharedPool(t, db, currentPoolID, "thursday", periodID, currentAccount)
	// A Thursday period owns exactly one current pool; its next pool is the
	// single unbound Thursday pool that will be attached at rollover.
	hostileInsertSharedPool(t, db, nextPoolID, "thursday", "", nextAccount)
	hostileInsertThursdayPeriod(t, db, periodID, currentPoolID, nextPoolID, "open")
	hostileMustExec(t, db, `COMMIT`)
	return periodID, currentPoolID, nextPoolID
}

func hostileEnvelope(length int, version byte) []byte {
	b := make([]byte, length)
	b[0] = version
	return b
}
