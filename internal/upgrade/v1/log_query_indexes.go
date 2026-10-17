package v1

const logQueryIndexes = `CREATE INDEX idx_request_attempts_key_log ON request_attempts(endpoint_key_id_snapshot,request_log_id);
CREATE INDEX idx_request_attempts_endpoint_log ON request_attempts(canonical_base_url,request_log_id);
CREATE INDEX idx_request_attempts_model_log ON request_attempts(upstream_model_id,request_log_id);
CREATE INDEX idx_request_logs_charity_model ON request_logs(route_kind,model,completed_at,id) WHERE route_kind IN ('charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions');
`
