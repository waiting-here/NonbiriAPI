package db

import "github.com/waiting-here/NonbiriAPI/internal/requestkind"

// RequestLogOperationIndexSQL keeps self and charity operations in one ordered
// range. completed_at covers the retention predicate used by counts and pages.
var RequestLogOperationIndexSQL = "CREATE INDEX idx_request_logs_operation_started ON request_logs((" + requestkind.OperationIndexSQL() + "),started_at,id,completed_at);\n"
