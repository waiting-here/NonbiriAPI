package resources

import (
	"context"
	"database/sql"
	"strconv"
)

func baselineEndpointBrowseTx(ctx context.Context, tx *sql.Tx, userID int64, endpoint Endpoint) (*EndpointBrowse, error) {
	var modelCount, availableKeys int64
	var subscription, apiPlatform bool
	err := tx.QueryRowContext(ctx, `SELECT
(SELECT COUNT(DISTINCT b.model_id)`+ownedBindingJoin+ownedBindingScope+` AND e.id=?),
(SELECT COUNT(*) FROM endpoint_keys k JOIN self_grant_availability a ON a.endpoint_key_id=k.id AND a.native_service_mask>0 WHERE k.endpoint_id=? AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)),
EXISTS(SELECT 1 FROM mainstream_channels c JOIN mainstream_channel_entries ce ON ce.channel_id=c.id WHERE c.state='active' AND c.enabled=1 AND ce.enabled=1
 AND ce.connector_type=? AND ce.canonical_base_url=? AND c.category='subscription'),
EXISTS(SELECT 1 FROM mainstream_channels c JOIN mainstream_channel_entries ce ON ce.channel_id=c.id WHERE c.state='active' AND c.enabled=1 AND ce.enabled=1
 AND ce.connector_type=? AND ce.canonical_base_url=? AND c.category='api_platform')`,
		userID, userID, endpoint.ID, endpoint.ID,
		endpoint.ConnectorType, endpoint.BaseURL, endpoint.ConnectorType, endpoint.BaseURL).Scan(&modelCount, &availableKeys, &subscription, &apiPlatform)
	if err != nil {
		return nil, err
	}
	state := "available"
	if !endpoint.Enabled {
		state = "endpoint_disabled"
		availableKeys = 0
	} else if endpoint.KeyCount == "0" {
		state = "no_keys"
	} else if availableKeys == 0 {
		state = "no_usable_key"
	}
	// Current channel matches are independent of the endpoint's creation snapshot.
	categories := make([]string, 0, 2)
	if subscription {
		categories = append(categories, mainstreamChannelCategorySubscription)
	}
	if apiPlatform {
		categories = append(categories, mainstreamChannelCategoryAPIPlatform)
	}
	return &EndpointBrowse{ModelCount: strconv.FormatInt(modelCount, 10), AvailableKeyCount: strconv.FormatInt(availableKeys, 10), State: state, MainstreamCategories: categories}, nil
}
