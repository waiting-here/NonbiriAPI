DROP TABLE admin_endpoint_tags;
UPDATE schema_state SET version=3;
DROP INDEX idx_request_errors_missing;
ALTER TABLE request_error_bodies DROP COLUMN failure_reason;
