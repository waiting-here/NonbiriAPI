package db

// AttemptHealthSQL retains operator-facing decisions alongside the request.
// A retry header may take effect before the attempt completes, so its receipt
// belongs to the log and can precede the request_attempts row.
const AttemptHealthSQL = `
ALTER TABLE request_attempts ADD COLUMN streak_effect TEXT CHECK(streak_effect IN ('incremented','reset','unchanged','pending'));
ALTER TABLE request_attempts ADD COLUMN streak_before TEXT CHECK(length(streak_before) BETWEEN 1 AND 39 AND streak_before NOT GLOB '*[^0-9]*');
ALTER TABLE request_attempts ADD COLUMN streak_after TEXT CHECK(length(streak_after) BETWEEN 1 AND 39 AND streak_after NOT GLOB '*[^0-9]*');
CREATE TABLE request_attempt_backoffs (
 claim_id TEXT PRIMARY KEY NOT NULL,
 request_log_id INTEGER NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
 reason TEXT NOT NULL CHECK(reason IN ('retry_header','google_model_overloaded','google_daily_requests','google_minute_requests','google_minute_input_tokens','google_daily_input_tokens','google_model_quota','smart_quota','smart_trial','recorded_wait')),
 scope TEXT NOT NULL CHECK(scope IN ('key','service','model','unknown')),
 retry_at INTEGER NOT NULL CHECK(retry_at BETWEEN 1 AND 253402300799),
 period TEXT CHECK(period IN ('unknown','five_hour','day','week','month')),
 observed_only INTEGER NOT NULL DEFAULT 0 CHECK(observed_only IN (0,1))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_attempt_backoffs_log ON request_attempt_backoffs(request_log_id);
`
