package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const callerParameterErrorSchema = `CREATE TABLE dispatch_caller_parameter_errors (
 claim_id TEXT PRIMARY KEY REFERENCES dispatch_claims(id) ON DELETE CASCADE,
 http_status INTEGER NOT NULL CHECK(http_status IN (400,422)),
 error_code TEXT NOT NULL CHECK(error_code IN ('invalid_value','invalid_type','unsupported_parameter','unsupported_value'))
) STRICT;
CREATE TRIGGER dispatch_caller_parameter_errors_insert_guard BEFORE INSERT ON dispatch_caller_parameter_errors
WHEN NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.id=NEW.claim_id AND c.purpose='charity' AND c.state='dispatched')
 OR EXISTS(SELECT 1 FROM dispatch_response_starts r WHERE r.claim_id=NEW.claim_id)
 OR EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status=200)
 OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.claim_id AND sb.accepted_at IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'invalid caller parameter error'); END;
CREATE TRIGGER dispatch_caller_parameter_errors_update_guard BEFORE UPDATE ON dispatch_caller_parameter_errors
BEGIN SELECT RAISE(ABORT,'caller parameter error is immutable'); END;
`

// extendCallerParameterErrors adds a separate proof for neutral upstream
// outcomes without changing retry observations or the failure-origin enum.
func extendCallerParameterErrors(ctx context.Context, tx *sql.Tx) error {
	const marker = "NEW.failure_origin IN ('client_cancel','downstream','platform','legacy_unknown','recovery_unknown')"
	for _, table := range []string{"donation_usage_reservations", "dispatch_claims"} {
		proof := "EXISTS(SELECT 1 FROM dispatch_caller_parameter_errors ce WHERE ce.claim_id=NEW.claim_id)"
		if table == "dispatch_claims" {
			// The attempt projection is written before the claim terminal update.
			// Usage reservations are written first and cannot join it yet.
			proof = "NEW.purpose='charity' AND EXISTS(SELECT 1 FROM dispatch_caller_parameter_errors ce JOIN request_attempts a ON a.claim_id=ce.claim_id WHERE ce.claim_id=NEW.id AND a.result_kind='response' AND a.upstream_status=ce.http_status AND a.upstream_code=ce.error_code)"
		}
		for _, operation := range []string{"insert", "update"} {
			name := table + "_outcome_" + operation + "_guard"
			var source string
			if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`, name).Scan(&source); err != nil {
				return err
			}
			if strings.Count(source, marker) != 1 || strings.Contains(source, "dispatch_caller_parameter_errors") {
				return errors.New("caller parameter migration source trigger is missing")
			}
			target := strings.Replace(source, marker, marker+" OR (NEW.failure_origin='upstream_response' AND "+proof+")", 1)
			if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteSQLiteIdentifier(name)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, target); err != nil {
				return err
			}
		}
	}
	return nil
}
