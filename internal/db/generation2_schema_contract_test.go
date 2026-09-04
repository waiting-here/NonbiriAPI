package db

import (
	"database/sql"
	"strings"
	"testing"
)

func openGenerationTwoDDLForTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(generationTwoSchema); err != nil {
		probe, probeErr := sql.Open("sqlite", ":memory:")
		if probeErr == nil {
			for n, stmt := range splitGenerationTwoStatements(generationTwoSchema) {
				if stmt == "" {
					continue
				}
				if _, stmtErr := probe.Exec(stmt); stmtErr != nil {
					t.Logf("DDL statement %d failed: %v\\n%s", n, stmtErr, stmt)
					break
				}
			}
			_ = probe.Close()
		}
		for _, marker := range []string{"balance_after_sign", "terminal_return BLOB", "removed_bits BLOB", "route_kind TEXT"} {
			if i := strings.Index(generationTwoSchema, marker); i >= 0 {
				start, end := i-160, i+480
				if start < 0 {
					start = 0
				}
				if end > len(generationTwoSchema) {
					end = len(generationTwoSchema)
				}
				t.Logf("DDL marker %s: %s", marker, generationTwoSchema[start:end])
			}
		}
		_ = db.Close()
		t.Fatalf("generation-two DDL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func splitGenerationTwoStatements(sqlText string) []string {
	var out []string
	start, beginDepth := 0, 0
	quoted := false
	for i := 0; i < len(sqlText); i++ {
		switch sqlText[i] {
		case '\'':
			if quoted && i+1 < len(sqlText) && sqlText[i+1] == '\'' {
				i++
				continue
			}
			quoted = !quoted
		case ';':
			if !quoted && beginDepth == 0 {
				if stmt := strings.TrimSpace(sqlText[start:i]); stmt != "" {
					out = append(out, stmt)
				}
				start = i + 1
			}
		}
		if quoted {
			continue
		}
		if i == 0 || (sqlText[i] >= 'a' && sqlText[i] <= 'z') || (sqlText[i] >= 'A' && sqlText[i] <= 'Z') || sqlText[i] == '_' {
			continue
		}
	}
	// A tiny lexer below is intentionally kept separate from the execution
	// path; trigger bodies are the only DDL statements containing semicolons.
	// Re-scan the source with word boundaries to identify BEGIN/END depth.
	out = nil
	start, beginDepth = 0, 0
	wordStart := -1
	flushWord := func(end int) {
		if wordStart < 0 {
			return
		}
		switch strings.ToUpper(sqlText[wordStart:end]) {
		case "BEGIN":
			beginDepth++
		case "END":
			if beginDepth > 0 {
				beginDepth--
			}
		}
		wordStart = -1
	}
	quoted = false
	for i := 0; i < len(sqlText); i++ {
		if sqlText[i] == '\'' {
			if quoted && i+1 < len(sqlText) && sqlText[i+1] == '\'' {
				i++
				continue
			}
			flushWord(i)
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		c := sqlText[i]
		isWord := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if isWord {
			if wordStart < 0 {
				wordStart = i
			}
			continue
		}
		flushWord(i)
		if c == ';' && beginDepth == 0 {
			if stmt := strings.TrimSpace(sqlText[start:i]); stmt != "" {
				out = append(out, stmt)
			}
			start = i + 1
		}
	}
	flushWord(len(sqlText))
	if stmt := strings.TrimSpace(sqlText[start:]); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func TestGenerationTwoCanonicalDDLExecutes(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	t.Logf("generation-two schema sha256=%s", GenerationTwoSchemaHash())
	var objectCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objectCount); err != nil {
		t.Fatal(err)
	}
	if objectCount == 0 {
		t.Fatal("generation-two DDL created no schema objects")
	}
}

func TestGenerationTwoRequestAttemptModelUsesUnicodeScalarBoundary(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	userID := hostileInsertUser(t, database, "request-attempt-model", 0, 0)

	insertLog := func(requestID, route, upstreamModelID string) int64 {
		t.Helper()
		hostileInsertLogicalRequest(t, database, requestID, userID, route, 1)
		result, err := database.Exec(`
INSERT INTO request_logs(logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,started_at)
VALUES(?,?,'model',?,?,'https://upstream.example/v1',0)`, requestID, userID, upstreamModelID, route)
		if err != nil {
			t.Fatalf("insert %s request log: %v", route, err)
		}
		logID, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("read request log id: %v", err)
		}
		return logID
	}
	insertAttempt := func(claimID string, logID int64, upstreamModelID string) error {
		_, err := database.Exec(`
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,started_at,completed_at)
VALUES(?,?,1,'openai-compatible','https://upstream.example/v1',?,'synthetic',0,0)`,
			claimID, logID, upstreamModelID)
		return err
	}

	discoveryLogID := insertLog(hostileOIDVariant("req_", 'D', 'Q'), "model_discovery", "")
	if err := insertAttempt(hostileOIDVariant("clm_", 'D', 'Q'), discoveryLogID, ""); err != nil {
		t.Fatalf("discovery empty upstream model rejected: %v", err)
	}

	maxModel := strings.Repeat("界", 512)
	selfLogID := insertLog(hostileOIDVariant("req_", 'S', 'Q'), "openai_chat_completions", maxModel)
	if err := insertAttempt(hostileOIDVariant("clm_", 'S', 'Q'), selfLogID, maxModel); err != nil {
		t.Fatalf("512-scalar upstream model rejected: %v", err)
	}

	tooLong := strings.Repeat("界", 513)
	requestID := hostileOIDVariant("req_", 'T', 'Q')
	hostileInsertLogicalRequest(t, database, requestID, userID, "openai_chat_completions", 1)
	if _, err := database.Exec(`
INSERT INTO request_logs(logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,started_at)
VALUES(?,?,'model',?,'openai_chat_completions','https://upstream.example/v1',0)`, requestID, userID, tooLong); err == nil {
		t.Fatal("request log accepted a 513-scalar upstream model")
	}
	attemptLogID := insertLog(hostileOIDVariant("req_", 'U', 'Q'), "openai_chat_completions", maxModel)
	if err := insertAttempt(hostileOIDVariant("clm_", 'T', 'Q'), attemptLogID, tooLong); err == nil {
		t.Fatal("request attempt accepted a 513-scalar upstream model")
	}
}

func TestGenerationTwoOIDConstraintsRejectHostileValues(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	good := "op_" + strings.Repeat("A", 21) + "Q"
	cases := []struct {
		name string
		id   string
	}{
		{"short", "op_A"},
		{"wildcard", "op_AAAAAAAAAAAAAAAAAAAAA_"},
		{"bad-character", "op_AAAAAAAAAAAAAAAAAAAA!A"},
		{"bad-prefix", "req_AAAAAAAAAAAAAAAAAAAAAA"},
		{"bad-final-quantum", "op_AAAAAAAAAAAAAAAAAAAAAB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`INSERT INTO accepted_operations(id,kind,payload_hash,created_at) VALUES(?,?,?,1)`, tc.id, "model_discovery", []byte(strings.Repeat("x", 32)))
			if err == nil {
				t.Fatalf("hostile OID %q was accepted", tc.id)
			}
		})
	}
	if _, err := db.Exec(`INSERT INTO accepted_operations(id,kind,payload_hash,state,created_at) VALUES(?,?,?,'accepted',1)`, good, "model_discovery", []byte(strings.Repeat("x", 32))); err != nil {
		// The deliberate string above has 22 payload characters and a legal
		// terminal quantum; keep this assertion as a positive control.
		t.Fatalf("canonical OID %q rejected: %v", good, err)
	}
}

func TestGenerationTwoSM128ConstraintsRejectHostileValues(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	zero := []byte(strings.Repeat("\x00", 16))
	high := append([]byte{0x80}, []byte(strings.Repeat("\x00", 15))...)
	if _, err := db.Exec(`INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','hostile-zero',1,?,1,1)`, zero); err == nil {
		t.Fatal("positive zero balance accepted")
	}
	if _, err := db.Exec(`INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','hostile-high',1,?,1,1)`, high); err == nil {
		t.Fatal("SM128 high-bit magnitude accepted")
	}
	if _, err := db.Exec(`INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','platform',0,?,1,1)`, zero); err != nil {
		t.Fatalf("canonical zero rejected: %v", err)
	}
}
