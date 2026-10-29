package db

// RequestDiagnosticsSQL adds bounded, safe observations tied to the existing
// attempt lifecycle. Deleting an attempt also deletes its diagnostic snapshot.
const RequestDiagnosticsSQL = `
CREATE TABLE request_attempt_diagnostics (
 claim_id TEXT PRIMARY KEY REFERENCES request_attempts(claim_id) ON DELETE CASCADE,
 request_log_id INTEGER NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
 attempt_seq INTEGER NOT NULL CHECK(typeof(attempt_seq)='integer' AND attempt_seq BETWEEN 1 AND 100),
 category TEXT NOT NULL CHECK(length(category) BETWEEN 1 AND 64),
 phase TEXT NOT NULL CHECK(length(phase) BETWEEN 1 AND 64),
 details TEXT NOT NULL CHECK(json_valid(details) AND json_type(details)='object' AND length(CAST(details AS BLOB))<=16384),
 completed_at INTEGER NOT NULL CHECK(typeof(completed_at)='integer' AND completed_at BETWEEN 0 AND 253402300799),
 UNIQUE(request_log_id,attempt_seq)
);
CREATE INDEX idx_request_diagnostics_category ON request_attempt_diagnostics(category,request_log_id);
CREATE INDEX idx_request_diagnostics_phase ON request_attempt_diagnostics(phase,request_log_id);
CREATE TABLE request_delivery_facts (
 request_log_id INTEGER PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,
 details TEXT NOT NULL CHECK(json_valid(details) AND json_type(details)='object' AND length(CAST(details AS BLOB))<=16384)
);
CREATE TABLE agent_request_diagnostics (
 request_id INTEGER PRIMARY KEY REFERENCES agent_requests(id) ON DELETE CASCADE,
 details TEXT NOT NULL CHECK(json_valid(details) AND json_type(details)='object' AND length(CAST(details AS BLOB))<=16384)
);
CREATE TABLE request_error_diagnostics (
 error_id INTEGER PRIMARY KEY REFERENCES request_error_bodies(id) ON DELETE CASCADE,
 details TEXT NOT NULL CHECK(json_valid(details) AND json_type(details)='object' AND length(CAST(details AS BLOB))<=16384)
);
`
