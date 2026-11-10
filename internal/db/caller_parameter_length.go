package db

// CallerParameterLengthSQL preserves prior proof rows while extending their
// exact error-code constraint. The dispatch identity guards remain unchanged.
const CallerParameterLengthSQL = `CREATE TABLE caller_parameter_errors_rebuild AS SELECT claim_id,http_status,error_code FROM dispatch_caller_parameter_errors;
DROP TABLE dispatch_caller_parameter_errors;
CREATE TABLE dispatch_caller_parameter_errors (
 claim_id TEXT PRIMARY KEY REFERENCES dispatch_claims(id) ON DELETE CASCADE,
 http_status INTEGER NOT NULL CHECK(http_status IN (400,422)),
 error_code TEXT NOT NULL CHECK(error_code IN ('invalid_value','invalid_type','unsupported_parameter','unsupported_value','context_length_exceeded','input_too_long','prompt_too_long','string_above_max_length') OR (http_status=400 AND error_code IN ('InvalidParameter','AI_APICallError')))
) STRICT;
INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) SELECT claim_id,http_status,error_code FROM caller_parameter_errors_rebuild;
DROP TABLE caller_parameter_errors_rebuild;
CREATE TRIGGER dispatch_caller_parameter_errors_insert_guard BEFORE INSERT ON dispatch_caller_parameter_errors
WHEN NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.id=NEW.claim_id AND c.purpose='charity' AND c.state='dispatched')
 OR EXISTS(SELECT 1 FROM dispatch_response_starts r WHERE r.claim_id=NEW.claim_id)
 OR EXISTS(SELECT 1 FROM dispatch_retry_after_observations r WHERE r.claim_id=NEW.claim_id AND r.http_status=200)
 OR EXISTS(SELECT 1 FROM dispatch_smart_backoff_facts sb WHERE sb.claim_id=NEW.claim_id AND sb.accepted_at IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'invalid caller parameter error'); END;
CREATE TRIGGER dispatch_caller_parameter_errors_update_guard BEFORE UPDATE ON dispatch_caller_parameter_errors
BEGIN SELECT RAISE(ABORT,'caller parameter error is immutable'); END;
`
