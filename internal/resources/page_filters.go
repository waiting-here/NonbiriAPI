package resources

import (
	"net/url"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type EndpointPageFilters struct {
	Query, ConnectorType, Source, State string
}

type EndpointKeyPageFilters struct {
	Query, Enabled, Donated, SuspensionState string
}

type ModelPageFilters struct {
	Query, Provider, RouteStrategy, ConnectionState string
}

func optionalChoice(value string, choices ...string) bool {
	if value == "" {
		return true
	}
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

// An omitted filter means all values; an explicitly empty enum is malformed.
func nonemptyPageFilters(values url.Values, names ...string) bool {
	for _, name := range names {
		if values.Has(name) && values.Get(name) == "" {
			return false
		}
	}
	return true
}

func (r *Repository) validEndpointPageFilters(f EndpointPageFilters) bool {
	if r == nil || !validateFreeText(f.Query, 0, 128) ||
		!optionalChoice(f.Source, "mainstream", "custom") ||
		!optionalChoice(f.State, "available", "endpoint_disabled", "no_keys", "no_usable_key") {
		return false
	}
	if f.ConnectorType != "" {
		if r.connectors == nil {
			return false
		}
		if _, err := r.connectors.MustValidate(connectorcontract.Type(f.ConnectorType)); err != nil {
			return false
		}
	}
	return true
}

const endpointPageState = `CASE WHEN e.enabled=0 THEN 'endpoint_disabled'
WHEN NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.endpoint_id=e.id) THEN 'no_keys'
WHEN NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.endpoint_id=e.id AND k.enabled=1
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)) THEN 'no_usable_key'
ELSE 'available' END`

func endpointPageQuery(userID int64, f EndpointPageFilters) (string, []any) {
	query, args := endpointPageSelect, []any{userID}
	if q := strings.TrimSpace(f.Query); q != "" {
		query += ` AND (instr(lower(e.base_url),lower(?))>0 OR instr(lower(e.note),lower(?))>0
OR instr(lower(COALESCE(e.mainstream_channel_name,'')),lower(?))>0)`
		args = append(args, q, q, q)
	}
	if f.ConnectorType != "" {
		query += ` AND e.connector_type=?`
		args = append(args, f.ConnectorType)
	}
	if f.Source == "mainstream" {
		query += ` AND e.mainstream_channel_id IS NOT NULL`
	} else if f.Source == "custom" {
		query += ` AND e.mainstream_channel_id IS NULL`
	}
	if f.State != "" {
		query += ` AND (` + endpointPageState + `)=?`
		args = append(args, f.State)
	}
	return query, args
}

func endpointKeyPageQuery(userID, endpointID int64, f EndpointKeyPageFilters) (string, []any) {
	query := endpointKeySelect + ` WHERE e.user_id=? AND e.id=?`
	args := []any{userID, endpointID}
	if q := strings.TrimSpace(f.Query); q != "" {
		query += ` AND (instr(lower(k.note),lower(?))>0 OR instr(lower(k.display_head),lower(?))>0
OR instr(lower(k.display_tail),lower(?))>0)`
		args = append(args, q, q, q)
	}
	if f.Enabled != "" {
		query += ` AND k.enabled=?`
		args = append(args, f.Enabled == "true")
	}
	if f.Donated != "" {
		query += ` AND EXISTS(SELECT 1 FROM donation_key_memberships dm WHERE dm.endpoint_key_id=k.id)=?`
		args = append(args, f.Donated == "true")
	}
	if f.SuspensionState != "" {
		query += ` AND EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)=?`
		args = append(args, f.SuspensionState == "security_processing")
	}
	return query, args
}

func modelPageQuery(userID int64, f ModelPageFilters) (string, []any) {
	query, args := modelSelect+` WHERE m.user_id=?`, []any{userID}
	if q := strings.TrimSpace(f.Query); q != "" {
		query += ` AND (instr(lower(m.full_name),lower(?))>0 OR EXISTS(SELECT 1
FROM model_bindings b JOIN endpoint_keys k ON k.id=b.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
WHERE b.model_id=m.id AND e.user_id=m.user_id AND instr(lower(b.upstream_model_id),lower(?))>0))`
		args = append(args, q, q)
	}
	if f.Provider != "" {
		query += ` AND m.provider=?`
		args = append(args, f.Provider)
	}
	if f.RouteStrategy != "" {
		query += ` AND m.route_strategy=?`
		args = append(args, f.RouteStrategy)
	}
	const anyBinding = `EXISTS(SELECT 1 FROM model_bindings b WHERE b.model_id=m.id)`
	// Correlate the outer model while reusing the browse eligibility expression.
	available := `EXISTS(SELECT 1 FROM model_bindings b JOIN endpoint_keys k ON k.id=b.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id WHERE b.model_id=m.id AND e.user_id=m.user_id AND (` + ownedBindingState + `)='available')`
	switch f.ConnectionState {
	case "available":
		query += ` AND ` + available
	case "unavailable":
		query += ` AND ` + anyBinding + ` AND NOT ` + available
	case "unconfigured":
		query += ` AND NOT ` + anyBinding
	}
	return query, args
}
