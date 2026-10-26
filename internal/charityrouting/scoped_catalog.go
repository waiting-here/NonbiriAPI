package charityrouting

// The caller supplies a selected donation_keys row as dk and repository-owned
// model expressions. Source labels describe catalog evidence, not live routing
// availability, and retain the approved version's discovery affinity.
func automaticCatalogSupportSQL(model string) string {
	return `EXISTS(SELECT 1 FROM model_catalog_entries ac INDEXED BY idx_catalog_run_model
WHERE ac.discovery_run_id IN (` + candidateApprovedRuns + `)
AND ac.source_type='automatic' AND ac.normalized_model_id=` + model + `)`
}

func manualCatalogSupportSQL(model string) string {
	return `EXISTS(SELECT 1 FROM donation_key_manual_models dm
WHERE dm.donation_key_id=dk.id AND dm.normalized_model_id=` + model + `)`
}

func catalogSupportQuery() string {
	return `SELECT EXISTS(SELECT 1 FROM donation_keys dk WHERE dk.id=?1 AND (` +
		automaticCatalogSupportSQL("?2") + ` OR ` + manualCatalogSupportSQL("?2") + `))`
}

// Scope both union branches before deduplication. Filtering the aggregate
// catalog view outside its UNION/GROUP BY still reads unrelated members.
const manualCatalogNamesSQL = `SELECT ac.normalized_model_id
FROM donation_keys dk CROSS JOIN model_catalog_entries ac INDEXED BY idx_catalog_run_model
ON ac.discovery_run_id IN (` + candidateApprovedRuns + `) AND ac.source_type='automatic'
WHERE dk.id=?
UNION SELECT normalized_model_id FROM donation_key_manual_models WHERE donation_key_id=?`

func manualCatalogSelection() string {
	return `SELECT pc.normalized_model_id,COALESCE(dm.display_name,pc.normalized_model_id),` +
		automaticCatalogSupportSQL("pc.normalized_model_id") + `,dm.id IS NOT NULL,dm.id,dm.revision
FROM (` + manualCatalogNamesSQL + `) pc JOIN donation_keys dk ON dk.id=?
LEFT JOIN donation_key_manual_models dm ON dm.donation_key_id=dk.id AND dm.normalized_model_id=pc.normalized_model_id`
}

func adminBindingsQuery() string {
	return `SELECT b.id,b.ord,dk.id,d.id,dk.connector_type,dk.canonical_base_url,
dk.display_head,dk.display_tail,t.upstream_model_id,` + automaticCatalogSupportSQL("t.upstream_model_id") + `,` + manualCatalogSupportSQL("t.upstream_model_id") + `,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0),gm.policy_json,
t.id,t.revision,t.native_service_mask,k.endpoint_id
FROM charity_model_bindings b
JOIN charity_binding_targets t ON t.binding_id=b.id
JOIN donation_keys dk ON dk.id=t.donation_key_id
JOIN donations d ON d.id=dk.donation_id
LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id
LEFT JOIN gateway_model_capabilities gm ON dk.connector_type IN ('ai-sdk-gateway-v3','ai-sdk-gateway-v4') AND gm.base_url=rtrim(dk.canonical_base_url,'/') AND gm.model=t.upstream_model_id
WHERE b.charity_model_id=? ORDER BY b.ord,b.id,t.ord,t.id LIMIT ?`
}
