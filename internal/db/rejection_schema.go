package db

import "strings"

func rejectionStorageSchema() string {
	var ddl strings.Builder
	for _, table := range []string{"logical_requests", "request_logs"} {
		ddl.WriteString("ALTER TABLE " + table + ` ADD COLUMN rejection_stage TEXT CHECK(rejection_stage IS NULL OR rejection_stage IN ('authorization','flow','preflight'));
ALTER TABLE ` + table + ` ADD COLUMN rejection_reason TEXT CHECK(rejection_reason IS NULL OR rejection_reason IN ('unauthorized','forbidden','charity_suspended','feature_disabled','maintenance','invalid_request','not_found','unbound_model','insufficient_credits','user_rpm','global_rpm','shared_rpm','concurrency','content_too_short','payload_too_large','resource_limit_exceeded','service_unavailable'));
ALTER TABLE ` + table + ` ADD COLUMN request_method TEXT CHECK(request_method IS NULL OR request_method IN ('GET','POST'));
ALTER TABLE ` + table + ` ADD COLUMN request_path TEXT CHECK(request_path IS NULL OR request_path IN ('/v1/models','/v1/chat/completions','/v1/embeddings'));
`)
		for _, operation := range []string{"INSERT", "UPDATE"} {
			ddl.WriteString("CREATE TRIGGER " + table + "_rejection_" + strings.ToLower(operation) + " BEFORE " + operation + " ON " + table + `
WHEN (NEW.rejection_stage IS NULL AND (NEW.rejection_reason IS NOT NULL OR NEW.request_method IS NOT NULL OR NEW.request_path IS NOT NULL))
 OR (NEW.rejection_stage IS NOT NULL AND (NEW.rejection_reason IS NULL OR NEW.request_method IS NULL OR NEW.request_path IS NULL
 OR NOT ((NEW.request_method='GET' AND NEW.request_path='/v1/models' AND NEW.route_kind='model_discovery')
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/chat/completions' AND NEW.route_kind IN ('openai_chat_completions','charity_chat_completions'))
 OR (NEW.request_method='POST' AND NEW.request_path='/v1/embeddings' AND NEW.route_kind IN ('openai_embeddings','charity_embeddings')))
 OR NEW.caller_result_class IS NOT 'failed'`)
			if table == "logical_requests" {
				ddl.WriteString(` OR NEW.state<>'terminal' OR NEW.accounting_state<>'none' OR NEW.account_reserved_milli<>0 OR NEW.ledger_rows_remaining<>X'00000000000000000000000000000000'`)
			} else {
				ddl.WriteString(` OR NEW.attempt_count<>0 OR NEW.uncached_input_tokens<>0 OR NEW.cache_write_input_tokens<>0 OR NEW.cache_read_input_tokens<>0 OR NEW.output_tokens<>0 OR NEW.usage_unknown<>0
 OR NOT EXISTS(SELECT 1 FROM logical_requests r WHERE r.id=NEW.logical_request_id AND r.rejection_stage IS NEW.rejection_stage AND r.rejection_reason IS NEW.rejection_reason AND r.request_method IS NEW.request_method AND r.request_path IS NEW.request_path)`)
			}
			ddl.WriteString(`)) BEGIN SELECT RAISE(ABORT,'invalid pre-handler rejection'); END;
`)
		}
	}
	ddl.WriteString(`CREATE INDEX idx_request_logs_phase ON request_logs(user_id,rejection_stage,started_at,id);
CREATE TRIGGER rejected_request_no_dispatch BEFORE INSERT ON dispatch_claims
WHEN EXISTS(SELECT 1 FROM logical_requests WHERE id=NEW.logical_request_id AND rejection_stage IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'rejected request cannot dispatch'); END;
`)
	return ddl.String()
}
