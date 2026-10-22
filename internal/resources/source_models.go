package resources

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func catalogServicesAggregate() string {
	terms := make([]string, 0, len(contract.AllNativeServices()))
	for _, service := range contract.AllNativeServices() {
		set, _ := contract.ParseNativeServices([]contract.NativeService{service})
		terms = append(terms, "MAX(c.native_service_mask & "+strconv.FormatInt(set.Mask(), 10)+")")
	}
	return strings.Join(terms, "+")
}

func (r *Repository) listSourceModels(ctx context.Context, userID, sourceID int64, search, provider, service, state string, window connectionListWindow) (Page[DiscoverySourceModel], error) {
	if sourceID <= 0 || !validateFreeText(search, 0, 128) || !validateFreeText(provider, 0, 128) || !optionalChoice(state, "current", "stale") {
		return Page[DiscoverySourceModel]{}, ErrInvalidRequest
	}
	var serviceMask int64
	if service != "" {
		services, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeService(service)})
		if err != nil {
			return Page[DiscoverySourceModel]{}, ErrInvalidRequest
		}
		serviceMask = services.Mask()
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[DiscoverySourceModel], error) {
		if _, err := getDiscoverySourceTx(ctx, tx, userID, sourceID); err != nil {
			return Page[DiscoverySourceModel]{}, err
		}
		query := `SELECT catalog.id,catalog.normalized_model_id,catalog.provider,catalog.native_service_mask,catalog.evidence_source FROM (
 SELECT MIN(c.id) AS id,c.normalized_model_id,MIN(c.provider) AS provider,` + catalogServicesAggregate() + ` AS native_service_mask,
 CASE WHEN COUNT(DISTINCT c.evidence_source)=1 THEN MIN(c.evidence_source) ELSE 'unknown' END AS evidence_source,
 CASE WHEN st.last_successful_run_id=st.current_run_id THEN 'current' ELSE 'stale' END AS state
 FROM discovery_sources ds JOIN discovery_source_states st ON st.source_revision_id=ds.current_revision_id
 JOIN model_catalog_entries c ON c.discovery_run_id=st.last_successful_run_id AND c.source_type='automatic'
 WHERE ds.id=? AND ds.user_id=? GROUP BY c.normalized_model_id
 ) catalog WHERE 1=1`
		args := []any{sourceID, userID}
		if search = strings.TrimSpace(search); search != "" {
			query += ` AND instr(lower(catalog.normalized_model_id),lower(?))>0`
			args = append(args, search)
		}
		if provider != "" {
			query += ` AND catalog.provider=?`
			args = append(args, provider)
		}
		if serviceMask != 0 {
			query += ` AND (catalog.native_service_mask&?)<>0`
			args = append(args, serviceMask)
		}
		if state != "" {
			query += ` AND catalog.state=?`
			args = append(args, state)
		}
		return readConnectionList(ctx, tx, r, window, "source-models", query, "catalog.id", args, scanSourceModel, func(item DiscoverySourceModel) string { return item.ID })
	})
}

func scanSourceModel(scanner pageScanner) (DiscoverySourceModel, error) {
	var item DiscoverySourceModel
	var id, mask int64
	var origin string
	if err := scanner.Scan(&id, &item.UpstreamModelID, &item.Provider, &mask, &origin); err != nil {
		return item, err
	}
	services, err := contract.NativeServicesFromMask(mask)
	if id <= 0 || err != nil || !validateCatalogText(item.UpstreamModelID, 1, 512) || !validateCatalogText(item.Provider, 0, 128) {
		return DiscoverySourceModel{}, ErrUnavailable
	}
	item.ID = strconv.FormatInt(id, 10)
	item.NativeServices = services.Services()
	item.NeedsServiceSelection = services.Empty()
	switch origin {
	case "metadata":
		item.ServiceEvidence = "provider"
	case "user":
		item.ServiceEvidence = "selected"
	case "legacy", "preset", "unknown":
		item.ServiceEvidence = origin
	default:
		return DiscoverySourceModel{}, ErrUnavailable
	}
	return item, nil
}
