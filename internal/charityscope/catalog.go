package charityscope

// SupportSQL accepts repository-owned aliases only.
func SupportSQL(memberID, endpointKeyID, modelID string) string {
	return `(EXISTS(SELECT 1 FROM model_pair_catalog support WHERE support.endpoint_key_id=` + endpointKeyID + ` AND support.normalized_model_id=` + modelID + ` AND (support.automatic_supports>0 OR support.manual_supports>0)) OR EXISTS(SELECT 1 FROM donation_key_manual_models support WHERE support.donation_key_id=` + memberID + ` AND support.normalized_model_id=` + modelID + `))`
}
