package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

const keyBrowseFactsSQL = `SELECT e.id,k.id,e.connector_type,e.base_url,
 d.state,d.revision,d.operation_hash,d.safe_class,d.safe_diag,d.started_at,d.completed_at,d.fetched_count,
 success.completed_at,changes.added_count,changes.removed_count,
 COALESCE((SELECT a.native_service_mask FROM self_grant_availability a
 WHERE a.endpoint_key_id=k.id AND a.owner_user_id=e.user_id
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)),0),
 CASE WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=k.id) THEN 'security_processing'
 WHEN EXISTS(SELECT 1 FROM donation_key_memberships WHERE endpoint_key_id=k.id) THEN 'already_donated'
 ELSE 'eligible' END
 FROM endpoints e JOIN endpoint_keys k ON k.endpoint_id=e.id
 JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
 LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=k.id
 LEFT JOIN discovery_source_states st ON st.source_revision_id=ks.source_revision_id
 LEFT JOIN discovery_runs success ON success.id=st.last_successful_run_id
 LEFT JOIN discovery_run_changes changes ON changes.run_id=st.current_run_id
 WHERE e.user_id=? AND e.id=? AND k.id IN (SELECT value FROM json_each(?))`

// Keep the page keys as the driving set. With freely reorderable joins SQLite
// starts at every key of the endpoint and filters the page only after its targets.
const keyBrowseCountsSQL = `WITH selected AS MATERIALIZED (
 SELECT json_extract(value,'$.id') AS id,json_extract(value,'$.mask') AS mask FROM json_each(?)
)
SELECT selected.id,COUNT(DISTINCT m.id),COUNT(DISTINCT b.id),COUNT(DISTINCT CASE WHEN (selected.mask&t.native_service_mask)>0 THEN b.id ELSE NULL END)
FROM selected
CROSS JOIN model_binding_targets t ON t.endpoint_key_id=selected.id
CROSS JOIN model_bindings b ON b.id=t.binding_id
CROSS JOIN models m ON m.id=b.model_id
CROSS JOIN endpoint_key_current_credentials k ON k.id=t.endpoint_key_id
CROSS JOIN endpoints e ON e.id=k.endpoint_id
WHERE m.user_id=? AND e.user_id=? AND e.id=? GROUP BY selected.id`

// Pick three target IDs per selected key before reading labels/eligibility.
// A window over every target would scan all of a key's bindings for its preview.
const keyBrowsePreviewsSQL = `WITH keys AS MATERIALIZED (
 SELECT k.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
 WHERE e.user_id=? AND e.id=? AND k.id IN (SELECT value FROM json_each(?))
), previews AS MATERIALIZED (
 SELECT value AS id FROM keys,json_each((SELECT json_group_array(id) FROM (
 SELECT t.id FROM model_binding_targets t JOIN model_bindings b ON b.id=t.binding_id
 JOIN models m ON m.id=b.model_id WHERE t.endpoint_key_id=keys.id AND m.user_id=?
 ORDER BY t.upstream_model_id,b.id,t.id LIMIT 3
 )))
) ` + ownedBindingSelect + ` JOIN previews ON previews.id=t.id` + ownedBindingScope + `
 ORDER BY k.id,t.upstream_model_id,b.id,t.id`

func attachEndpointKeyBrowseTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, keys []EndpointKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]string, len(keys))
	byID := make(map[string]*EndpointKey, len(keys))
	for i := range keys {
		ids[i] = keys[i].ID
		byID[ids[i]] = &keys[i]
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, keyBrowseFactsSQL, userID, endpointID, string(encoded))
	if err != nil {
		return err
	}
	defer rows.Close()
	type authority struct {
		ID   string `json:"id"`
		Mask int64  `json:"mask"`
	}
	authorities := make([]authority, 0, len(keys))
	for rows.Next() {
		var row discoveryRow
		var mask int64
		var eligibility string
		if err := rows.Scan(&row.endpointID, &row.keyID, &row.connectorType, &row.baseURL,
			&row.state, &row.revision, &row.operationHash, &row.safeClass, &row.safeDiag,
			&row.startedAt, &row.completedAt, &row.fetchedCount, &row.lastSuccessAt, &row.addedCount, &row.removedCount,
			&mask, &eligibility); err != nil {
			return err
		}
		id := strconv.FormatInt(row.keyID, 10)
		key := byID[id]
		if key == nil {
			return ErrUnavailable
		}
		evidence, err := row.evidence()
		if err != nil {
			return err
		}
		if !key.CredentialAvailable {
			eligibility = "unavailable"
		}
		key.Browse = &EndpointKeyBrowse{DonationEligibility: eligibility, ModelCount: "0", BindingCount: "0", AvailableBindingCount: "0", Discovery: evidence, Preview: make([]KeyBindingView, 0, 3)}
		authorities = append(authorities, authority{id, mask})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(authorities) != len(keys) {
		return ErrNotFound
	}
	// Keep these grants in this authorized transaction, never across requests.
	masks, err := json.Marshal(authorities)
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, keyBrowseCountsSQL, string(masks), userID, userID, endpointID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var models, bindings, available int64
		if err := rows.Scan(&id, &models, &bindings, &available); err != nil {
			return err
		}
		key := byID[id]
		if key == nil || key.Browse == nil {
			return ErrUnavailable
		}
		key.Browse.ModelCount, key.Browse.BindingCount, key.Browse.AvailableBindingCount = strconv.FormatInt(models, 10), strconv.FormatInt(bindings, 10), strconv.FormatInt(available, 10)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, keyBrowsePreviewsSQL, userID, endpointID, string(encoded), userID, userID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanKeyBinding(rows)
		if err != nil {
			return err
		}
		key := byID[item.EndpointKeyID]
		if key == nil || key.Browse == nil {
			return ErrUnavailable
		}
		key.Browse.Preview = append(key.Browse.Preview, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}
