CREATE INDEX idx_credit_entries_history ON credit_entries(account_id,operation_id,line_no,delta_sign) WHERE account_kind_snapshot='user' AND delta_sign<>0;
CREATE INDEX idx_credit_operations_history ON credit_operations(id,ledger_seq,created_at,kind);
DROP INDEX idx_request_logs_retention;
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id);
