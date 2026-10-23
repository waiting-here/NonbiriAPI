package charity

import "strings"

func quoteFixtureName(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

// The released fixture has one chat use. Project that use's physical state
// into the old key scope before validating the real released schema upgrade.
func releasedBackoffFrom(table, prefix string, serviceHealth bool) string {
	name := prefix + quoteFixtureName(table)
	if table != "donation_key_backoffs" || !serviceHealth {
		return name + " src"
	}
	return `(SELECT * FROM ` + name + ` UNION ALL
SELECT u.donation_key_id,h.backoff_generation,h.backoff_episode,h.backoff_period,h.backoff_source,h.backoff_stage,h.backoff_retry_at,h.trial_claim_id,h.updated_at
FROM ` + prefix + `donation_service_health h JOIN ` + prefix + `donation_uses u ON u.id=h.use_id
WHERE h.native_service_bit=1 AND NOT EXISTS(SELECT 1 FROM ` + name + ` old WHERE old.donation_key_id=u.donation_key_id)) src`
}

func releasedBackoffColumn(table, column, prefix string) string {
	switch table {
	case "mainstream_channels":
		if column == "connector_type" || column == "canonical_base_url" {
			return `(SELECT e.` + quoteFixtureName(column) + ` FROM ` + prefix + `mainstream_channel_entries e WHERE e.channel_id=src.id ORDER BY e.ord LIMIT 1)`
		}
	case "endpoint_keys":
		switch column {
		case "secret_ref_id", "display_head", "display_tail":
			return `(SELECT v.` + quoteFixtureName(column) + ` FROM ` + prefix + `endpoint_key_grant_revisions g JOIN ` + prefix + `credential_versions v ON v.id=g.credential_version_id WHERE g.id=src.current_grant_revision_id)`
		case "secret_fingerprint":
			return `(SELECT g.report_fingerprint FROM ` + prefix + `endpoint_key_grant_revisions g WHERE g.id=src.current_grant_revision_id)`
		}
	case "model_bindings":
		if column == "endpoint_key_id" || column == "upstream_model_id" {
			return `(SELECT p.` + quoteFixtureName(column) + ` FROM ` + prefix + `model_binding_primary_targets p WHERE p.binding_id=src.id)`
		}
	case "charity_model_bindings":
		if column == "endpoint_key_id" {
			return `(SELECT dk.endpoint_key_id FROM ` + prefix + `charity_binding_primary_targets p JOIN ` + prefix + `donation_keys dk ON dk.id=p.donation_key_id WHERE p.binding_id=src.id)`
		}
		if column == "donation_key_id" || column == "upstream_model_id" {
			return `(SELECT p.` + quoteFixtureName(column) + ` FROM ` + prefix + `charity_binding_primary_targets p WHERE p.binding_id=src.id)`
		}
	case "model_catalog_entries":
		if column == "endpoint_key_id" {
			return `(SELECT a.endpoint_key_id FROM ` + prefix + `endpoint_key_discovery_sources a JOIN ` + prefix + `discovery_runs r ON r.source_revision_id=a.source_revision_id WHERE r.id=src.discovery_run_id)`
		}
	}
	return "src." + quoteFixtureName(column)
}
