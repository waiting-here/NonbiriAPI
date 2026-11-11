package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

// Bind one bounded page, then resolve its projections in a single statement.
// Every correlated aggregate is scoped to one selected endpoint and its owner.
// Discovery/credential views must never be aggregated globally before paging.
const endpointBrowsePageSQL = `WITH page AS MATERIALIZED (
 SELECT e.id,e.connector_type,e.base_url FROM endpoints e
 WHERE e.user_id=? AND e.id IN (SELECT value FROM json_each(?))
)
SELECT page.id,
(SELECT COUNT(DISTINCT b.model_id)` + ownedBindingJoin + ownedBindingScope + ` AND e.id=page.id),
(SELECT COUNT(*) FROM endpoint_keys k JOIN self_grant_availability a ON a.endpoint_key_id=k.id AND a.native_service_mask>0
 WHERE k.endpoint_id=page.id AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)),
EXISTS(SELECT 1 FROM mainstream_channels c JOIN mainstream_channel_entries ce ON ce.channel_id=c.id
 WHERE c.state='active' AND c.enabled=1 AND ce.enabled=1 AND ce.connector_type=page.connector_type AND ce.canonical_base_url=page.base_url AND c.category='subscription'),
EXISTS(SELECT 1 FROM mainstream_channels c JOIN mainstream_channel_entries ce ON ce.channel_id=c.id
 WHERE c.state='active' AND c.enabled=1 AND ce.enabled=1 AND ce.connector_type=page.connector_type AND ce.canonical_base_url=page.base_url AND c.category='api_platform')
FROM page ORDER BY page.id`

func attachEndpointBrowseTx(ctx context.Context, tx *sql.Tx, userID int64, endpoints []Endpoint) error {
	if len(endpoints) == 0 {
		return nil
	}
	ids := make([]string, len(endpoints))
	byID := make(map[string]*Endpoint, len(endpoints))
	for i := range endpoints {
		ids[i] = endpoints[i].ID
		byID[ids[i]] = &endpoints[i]
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, endpointBrowsePageSQL, userID, string(encoded), userID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		var models, available int64
		var subscription, platform bool
		if err := rows.Scan(&id, &models, &available, &subscription, &platform); err != nil {
			return err
		}
		endpoint := byID[id]
		if endpoint == nil {
			return ErrUnavailable
		}
		state := "available"
		if !endpoint.Enabled {
			state, available = "endpoint_disabled", 0
		} else if endpoint.KeyCount == "0" {
			state = "no_keys"
		} else if available == 0 {
			state = "no_usable_key"
		}
		categories := make([]string, 0, 2)
		if subscription {
			categories = append(categories, mainstreamChannelCategorySubscription)
		}
		if platform {
			categories = append(categories, mainstreamChannelCategoryAPIPlatform)
		}
		endpoint.Browse = &EndpointBrowse{ModelCount: strconv.FormatInt(models, 10), AvailableKeyCount: strconv.FormatInt(available, 10), State: state, MainstreamCategories: categories}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(endpoints) {
		return ErrNotFound
	}
	return rows.Close()
}
