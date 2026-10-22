package charityscope

// SupportSQL accepts repository-owned aliases only.
func SupportSQL(memberID, endpointKeyID, modelID string) string {
	return `EXISTS(SELECT 1 FROM charity_model_catalog support WHERE support.donation_key_id=` + memberID + ` AND support.normalized_model_id=` + modelID + ` AND (support.automatic_supports>0 OR support.manual_supports>0))`
}
