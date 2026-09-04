package logapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	logUserOne = int64(101)
	logUserTwo = int64(202)
)

type logTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newLogTestClock(unix int64) *logTestClock { return &logTestClock{now: time.Unix(unix, 0).UTC()} }
func (clock *logTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}
func (clock *logTestClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

type logTestKeyDeriver struct {
	mu    sync.Mutex
	infos [][]byte
}

func (deriver *logTestKeyDeriver) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	deriver.mu.Lock()
	deriver.infos = append(deriver.infos, append([]byte(nil), info...))
	deriver.mu.Unlock()
	digest := sha256.Sum256(append([]byte("logapi-test-key\x00"), info...))
	return append([]byte(nil), digest[:]...), nil
}

func (deriver *logTestKeyDeriver) requireCanonicalInfo(t *testing.T) {
	t.Helper()
	deriver.mu.Lock()
	defer deriver.mu.Unlock()
	if len(deriver.infos) == 0 {
		t.Fatal("cursor key was never derived")
	}
	for _, info := range deriver.infos {
		if string(info) != "pagination-cursor/v1" {
			t.Fatalf("cursor key info = %q", info)
		}
	}
}

type logFixture struct {
	t           *testing.T
	db          *sql.DB
	clock       *logTestClock
	keys        *logTestKeyDeriver
	repo        *Repository
	selfID      string
	charityID   string
	discoveryID string
	otherID     string
	deletedID   string
	pendingID   string
}

type allowLogStewardRead struct{}

func (allowLogStewardRead) AuthorizeStewardRead(context.Context, *sql.Tx, int64) error { return nil }

func newLogFixture(t *testing.T) *logFixture {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	statements := []string{
		`CREATE TABLE request_logs(
 id INTEGER PRIMARY KEY, logical_request_id TEXT NOT NULL, user_id INTEGER, model TEXT NOT NULL,
 route_kind TEXT NOT NULL, caller_result_class TEXT, caller_status INTEGER, caller_error_code TEXT,
 started_at INTEGER NOT NULL, completed_at INTEGER, uncached_input_tokens INTEGER NOT NULL,
 cache_write_input_tokens INTEGER NOT NULL, cache_read_input_tokens INTEGER NOT NULL,
 output_tokens INTEGER NOT NULL, usage_unknown INTEGER NOT NULL, attempt_count INTEGER NOT NULL,
 raw_body TEXT, authorization TEXT, cookie TEXT, discord_id TEXT, private_note TEXT, ciphertext TEXT)`,
		`CREATE TABLE request_attempts(
 claim_id TEXT PRIMARY KEY, request_log_id INTEGER NOT NULL, attempt_seq INTEGER NOT NULL,
 endpoint_id_snapshot INTEGER, endpoint_key_id_snapshot INTEGER, canonical_base_url TEXT NOT NULL,
 connector_type TEXT NOT NULL, upstream_model_id TEXT NOT NULL, upstream_status INTEGER,
 upstream_code TEXT, diag TEXT, input_tokens INTEGER NOT NULL, cache_write_input_tokens INTEGER NOT NULL,
 cache_read_input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, usage_unknown INTEGER NOT NULL,
 started_at INTEGER NOT NULL, completed_at INTEGER NOT NULL, raw_upstream TEXT, response_body TEXT,
 set_cookie TEXT)`,
		`CREATE TABLE endpoints(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,note TEXT NOT NULL)`,
		`CREATE TABLE endpoint_keys(id INTEGER PRIMARY KEY,endpoint_id INTEGER NOT NULL,note TEXT NOT NULL)`,
		`CREATE TABLE credit_operations(id TEXT PRIMARY KEY,source_type TEXT NOT NULL,source_id TEXT NOT NULL,
 kind TEXT NOT NULL,ledger_seq INTEGER NOT NULL)`,
		`CREATE TABLE credit_entries(operation_id TEXT NOT NULL,delta_mag BLOB NOT NULL,
 account_kind_snapshot TEXT NOT NULL,delta_sign INTEGER NOT NULL,line_no INTEGER NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			_ = database.Close()
			t.Fatalf("create log fixture schema: %v", err)
		}
	}
	clock := newLogTestClock(1_000_000)
	keys := &logTestKeyDeriver{}
	repository, err := newRepository(database, keys, clock.Now)
	if err != nil {
		_ = database.Close()
		t.Fatalf("newRepository: %v", err)
	}
	fixture := &logFixture{
		t: t, db: database, clock: clock, keys: keys, repo: repository,
		selfID: logOpaqueID("req_", 1), charityID: logOpaqueID("req_", 2),
		discoveryID: logOpaqueID("req_", 3), otherID: logOpaqueID("req_", 4),
		deletedID: logOpaqueID("req_", 5), pendingID: logOpaqueID("req_", 6),
	}
	fixture.seed()
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database Close: %v", err)
		}
	})
	return fixture
}

func logOpaqueID(prefix string, sequence uint64) string {
	var raw [16]byte
	copy(raw[:8], []byte("log-wire"))
	binary.BigEndian.PutUint64(raw[8:], sequence)
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:])
}

func logMagnitude(value uint64) []byte {
	result := make([]byte, 16)
	binary.BigEndian.PutUint64(result[8:], value)
	return result
}

func (fixture *logFixture) mustExec(statement string, args ...any) {
	fixture.t.Helper()
	if _, err := fixture.db.Exec(statement, args...); err != nil {
		fixture.t.Fatalf("Exec %q: %v", statement, err)
	}
}

func (fixture *logFixture) insertLog(
	rowID int64,
	requestID string,
	userID any,
	model, route string,
	class any,
	status any,
	code any,
	started int64,
	completed any,
	usage [4]int64,
	unknown, attempts int64,
) {
	fixture.mustExec(`INSERT INTO request_logs(
id,logical_request_id,user_id,model,route_kind,caller_result_class,caller_status,caller_error_code,
started_at,completed_at,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,
output_tokens,usage_unknown,attempt_count,raw_body,authorization,cookie,discord_id,private_note,ciphertext)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rowID, requestID, userID, model, route, class, status, code, started, completed,
		usage[0], usage[1], usage[2], usage[3], unknown, attempts,
		"RAW-REQUEST-BODY-SENTINEL", "Bearer RAW-AUTH-SENTINEL", "RAW-COOKIE-SENTINEL",
		"RAW-DISCORD-SENTINEL", "RAW-PRIVATE-NOTE-SENTINEL", "RAW-CIPHERTEXT-SENTINEL")
}

func (fixture *logFixture) insertAttempt(
	requestLogID, sequence int64,
	endpointID, keyID any,
	baseURL, connector, upstreamModel, resultKind string,
	status any,
	code, diag any,
	usage [4]int64,
	unknown, started, completed int64,
) {
	fixture.mustExec(`INSERT INTO request_attempts(
claim_id,request_log_id,attempt_seq,endpoint_id_snapshot,endpoint_key_id_snapshot,canonical_base_url,
connector_type,upstream_model_id,upstream_status,upstream_code,diag,input_tokens,
cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at,
raw_upstream,response_body,set_cookie)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		logOpaqueID("clm_", uint64(requestLogID*10+sequence)), requestLogID, sequence, endpointID, keyID,
		baseURL, connector, upstreamModel, status, code, diag, usage[0], usage[1], usage[2], usage[3],
		unknown, started, completed, "RAW-UPSTREAM-SENTINEL", "RAW-RESPONSE-BODY-SENTINEL", "RAW-SET-COOKIE-SENTINEL")
	fixture.mustExec(`UPDATE request_attempts SET result_kind=? WHERE claim_id=?`, resultKind,
		logOpaqueID("clm_", uint64(requestLogID*10+sequence)))
}

func (fixture *logFixture) seed() {
	// Add result_kind after table creation so tests can also construct hostile
	// nullable/status rows without schema constraints.
	fixture.mustExec(`ALTER TABLE request_attempts ADD COLUMN result_kind TEXT NOT NULL DEFAULT 'response'`)
	fixture.mustExec(`INSERT INTO endpoints(id,user_id,note) VALUES(?,?,?),(?,?,?)`,
		201, logUserOne, "OWNER-ENDPOINT-NOTE-SENTINEL", 202, logUserTwo, "OTHER-ENDPOINT-NOTE-SENTINEL")
	fixture.mustExec(`INSERT INTO endpoint_keys(id,endpoint_id,note) VALUES(?,?,?),(?,?,?)`,
		301, 201, "OWNER-KEY-NOTE-SENTINEL", 302, 202, "OTHER-KEY-NOTE-SENTINEL")

	fixture.insertLog(1, fixture.selfID, logUserOne, "SELF-LOGICAL-MODEL-SENTINEL", string(RouteOpenAIChat),
		string(ResultFailed), 422, "debug_live_result_captured", 100, 110, [4]int64{1, 2, 3, 4}, 0, 2)
	fixture.insertAttempt(1, 1, 201, 301, "https://self-one.example/v1", "openai-compatible", "up-self-1",
		string(ResultResponse), 429, "rate_limit", "safe rate diagnostic", [4]int64{1, 0, 0, 2}, 0, 101, 102)
	// This snapshot points at a deleted resource, so owner notes must project as
	// empty while immutable safe snapshots remain available.
	fixture.insertAttempt(1, 2, 999, 999, "https://deleted.example/v1", "anthropic-compatible", "up-self-2",
		string(ResultSynthetic), nil, "timeout", "safe timeout diagnostic", [4]int64{0, 2, 1, 2}, 1, 103, 104)

	fixture.insertLog(2, fixture.charityID, logUserOne, "CHARITY-LOGICAL-MODEL-SENTINEL", string(RouteCharityChat),
		string(ResultSuccess), 200, nil, 200, 220, [4]int64{5, 0, 0, 5}, 0, 2)
	fixture.insertAttempt(2, 1, 202, 302, "https://charity-one.example/v1", "openai-compatible", "charity-up-1",
		string(ResultResponse), 200, nil, nil, [4]int64{2, 0, 0, 3}, 0, 201, 205)
	fixture.insertAttempt(2, 2, 202, 302, "https://charity-two.example/v1", "openai-compatible", "charity-up-2",
		string(ResultSynthetic), 502, "transport", "safe transport diagnostic", [4]int64{3, 0, 0, 2}, 0, 206, 210)

	fixture.insertLog(3, fixture.discoveryID, logUserOne, "DISCOVERY-LOGICAL-MODEL-SENTINEL", string(RouteDiscovery),
		string(ResultCancelled), nil, nil, 300, 305, [4]int64{}, 1, 0)
	fixture.insertLog(4, fixture.otherID, logUserTwo, "OTHER-LOGICAL-MODEL-SENTINEL", string(RouteOpenAIChat),
		string(ResultFailed), 502, "upstream", 400, 410, [4]int64{7, 0, 0, 1}, 0, 1)
	fixture.insertAttempt(4, 1, 202, 302, "https://other.example/v1", "openai-compatible", "other-upstream",
		string(ResultSynthetic), 502, "transport", "safe other diagnostic", [4]int64{7, 0, 0, 1}, 0, 401, 405)
	fixture.insertLog(5, fixture.deletedID, nil, "DELETED-LOGICAL-MODEL-SENTINEL", string(RouteOpenAIChat),
		string(ResultSuccess), 200, nil, 500, 510, [4]int64{1, 1, 1, 1}, 0, 0)
	fixture.insertLog(6, fixture.pendingID, logUserOne, "PENDING-LOGICAL-MODEL-SENTINEL", string(RouteOpenAIChat),
		nil, nil, nil, 600, nil, [4]int64{}, 0, 0)

	fixture.mustExec(`INSERT INTO credit_operations(id,source_type,source_id,kind,ledger_seq) VALUES(?,?,?,?,?)`,
		"op-self", "logical_request", fixture.selfID, "forward_settle", 1)
	fixture.mustExec(`INSERT INTO credit_entries(operation_id,delta_mag,account_kind_snapshot,delta_sign,line_no)
VALUES(?,?,?,?,?)`, "op-self", logMagnitude(1234), "platform", 1, 1)
}

func marshalLogGolden(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	return append(encoded, '\n')
}

func assertLogGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- got ---\n%s", name, err, got)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func noLogSentinel(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, sentinel := range forbidden {
		if strings.Contains(lower, strings.ToLower(sentinel)) {
			t.Fatalf("forbidden sentinel %q in %s", sentinel, encoded)
		}
	}
}

func collectJSONKeys(value any, result map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			result[key] = struct{}{}
			collectJSONKeys(child, result)
		}
	case []any:
		for _, child := range typed {
			collectJSONKeys(child, result)
		}
	}
}

func requireNoJSONKeys(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]struct{})
	collectJSONKeys(decoded, keys)
	for _, key := range forbidden {
		if _, exists := keys[key]; exists {
			t.Fatalf("forbidden JSON key %q in %s", key, encoded)
		}
	}
}
