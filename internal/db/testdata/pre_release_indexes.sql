-- Empty-schema fixture only; never executed by application migrations.
DROP INDEX "idx_credit_entries_history";
DROP INDEX "idx_credit_operations_history";
DROP INDEX "idx_request_logs_retention";
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id) WHERE completed_at IS NOT NULL;
