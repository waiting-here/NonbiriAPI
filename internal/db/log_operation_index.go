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

// RequestLogCallerErrorIndexSQL orders each caller error by the log list's tie
// breakers. The trailing fields cover retention and charity-model filtering
// when the planner searches by error before filtering by model.
const RequestLogCallerErrorIndexSQL = `CREATE INDEX idx_request_logs_caller_error_started ON request_logs(caller_error_code,started_at,id,completed_at,model,route_kind) WHERE caller_error_code IS NOT NULL;
`
