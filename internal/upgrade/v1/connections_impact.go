package v1

// Only fields listed in connections_schema.json are copied or introduced.
// Unchanged large tables are not rebuilt for relationship sidecars.
var connectionsMigrationImpact = &migrationImpact{
	addedTables:      []string{"dispatch_caller_parameter_errors", "dispatch_service_health", "donation_service_health", "endpoint_backoff_policies", "global_pressure_samples", "global_pressure_state", "mainstream_channel_discovery_presets", "mainstream_channel_model_mappings", "personal_header_sessions", "personal_header_uses", "request_attempt_service_facts"},
	rebuiltTables:    []string{"charity_header_sessions", "charity_header_uses", "charity_model_bindings", "charity_models", "charity_reservations", "donation_keys", "endpoint_key_secrets", "endpoint_keys", "endpoints", "logical_requests", "mainstream_channels", "model_bindings", "model_catalog_entries", "models", "report_cases", "report_targets", "request_attempts", "request_logs", "resource_operation_status", "model_discovery_evidence"},
	relationTables:   []string{"charity_binding_targets", "credential_versions", "credentials", "discovery_runs", "discovery_source_revisions", "discovery_source_states", "discovery_sources", "dispatch_claim_targets", "donation_use_revisions", "donation_uses", "endpoint_key_discovery_sources", "endpoint_key_grant_revisions", "mainstream_channel_entries", "model_binding_targets", "service_connections"},
	controlTables:    []string{"site_config"},
	constraintTables: []string{"users", "risk_audit_minutes", "donation_usage_reservations", "dispatch_smart_backoff_facts"},
	credentials:      credentialsRebuilt,
}
