package db

import "github.com/waiting-here/NonbiriAPI/internal/requestkind"

// RequestLogOperationIndexSQL keeps self and charity operations in one ordered
// range. completed_at covers the retention predicate used by counts and pages.
var RequestLogOperationIndexSQL = "CREATE INDEX idx_request_logs_operation_started ON request_logs((" + requestkind.OperationIndexSQL() + "),started_at,id,completed_at);\n"

// RequestLogOwnerIndexSQL covers retention and caller outcome filters without
// loading wide diagnostic rows. It replaces the existing owner/time index.
const RequestLogOwnerIndexSQL = `DROP INDEX idx_request_logs_user_started;
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id,completed_at,caller_status,caller_error_code);
`
