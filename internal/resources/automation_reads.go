package resources

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// AutomationPage deliberately exposes only page-number metadata.
type AutomationPage[T any] struct {
	Data       []T                  `json:"data"`
	Pagination *pagination.Metadata `json:"pagination"`
}

func automationPage[T any](page Page[T]) AutomationPage[T] {
	return AutomationPage[T]{Data: page.Data, Pagination: page.Pagination}
}

func (r *Repository) AutomationEndpoints(ctx context.Context, userID int64, q string, page pagination.Request) (AutomationPage[Endpoint], error) {
	result, err := userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[Endpoint], error) {
		query, args := endpointPageQuery(userID, EndpointPageFilters{Query: q})
		return readNumberedPage(ctx, tx, page, query, "e.updated_at DESC,e.id DESC", args, func(s pageScanner) (Endpoint, error) { return scanEndpoint(s) })
	})
	return automationPage(result), err
}
func (r *Repository) AutomationEndpoint(ctx context.Context, userID, id int64) (Endpoint, error) {
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Endpoint, error) { return getEndpointTx(ctx, tx, userID, id) })
}
func (r *Repository) AutomationKeys(ctx context.Context, userID, endpointID int64, q string, page pagination.Request) (AutomationPage[EndpointKey], error) {
	result, err := userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[EndpointKey], error) {
		if _, err := getEndpointTx(ctx, tx, userID, endpointID); err != nil {
			return Page[EndpointKey]{}, err
		}
		query, args := endpointKeyPageQuery(userID, endpointID, EndpointKeyPageFilters{Query: q})
		return readNumberedPage(ctx, tx, page, query, "k.updated_at DESC,k.id DESC", args, func(s pageScanner) (EndpointKey, error) { return scanEndpointKey(s) })
	})
	return automationPage(result), err
}
func (r *Repository) AutomationKey(ctx context.Context, userID, endpointID, keyID int64) (EndpointKey, error) {
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (EndpointKey, error) {
		return getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	})
}
func (r *Repository) AutomationModels(ctx context.Context, userID int64, q string, page pagination.Request) (AutomationPage[Model], error) {
	result, err := r.FilterModelsPage(ctx, userID, ModelPageFilters{Query: q}, page)
	return automationPage(result), err
}
func (r *Repository) AutomationModel(ctx context.Context, userID, id int64) (Model, error) {
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Model, error) { return getModelTx(ctx, tx, userID, id) })
}

type AutomationBinding struct {
	ID              string `json:"id"`
	EndpointKeyID   string `json:"endpoint_key_id"`
	EndpointID      string `json:"endpoint_id"`
	BaseURL         string `json:"base_url"`
	ConnectorType   string `json:"connector_type"`
	EndpointNote    string `json:"endpoint_note"`
	KeyNote         string `json:"key_note"`
	DisplayHead     string `json:"display_head"`
	DisplayTail     string `json:"display_tail"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             int    `json:"ord"`
	EndpointEnabled bool   `json:"endpoint_enabled"`
	KeyEnabled      bool   `json:"key_enabled"`
	SuspensionState string `json:"suspension_state"`
	CatalogEligible bool   `json:"catalog_eligible"`
}

func (r *Repository) AutomationBindings(ctx context.Context, userID, modelID int64, q string, page pagination.Request) (AutomationPage[AutomationBinding], error) {
	result, err := userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[AutomationBinding], error) {
		if _, err := getModelTx(ctx, tx, userID, modelID); err != nil {
			return Page[AutomationBinding]{}, err
		}
		query := `SELECT b.id,k.id,e.id,e.base_url,e.connector_type,e.note,k.note,k.display_head,k.display_tail,b.upstream_model_id,b.ord,e.enabled,k.enabled,
  CASE WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id) THEN 'security_processing' ELSE 'none' END,
  COALESCE((p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision)),0)
  FROM model_bindings b JOIN models m ON m.id=b.model_id JOIN endpoint_keys k ON k.id=b.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
  LEFT JOIN model_pair_catalog p ON p.endpoint_key_id=k.id AND p.normalized_model_id=b.upstream_model_id LEFT JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
  WHERE m.id=? AND m.user_id=? AND e.user_id=? AND (?='' OR instr(b.upstream_model_id,?)>0 OR instr(e.base_url,?)>0 OR instr(e.note,?)>0 OR instr(k.note,?)>0)`
		return readNumberedPage(ctx, tx, page, query, "b.ord,b.id", []any{modelID, userID, userID, q, q, q, q, q}, func(s pageScanner) (AutomationBinding, error) {
			var item AutomationBinding
			var id, keyID, endpointID int64
			err := s.Scan(&id, &keyID, &endpointID, &item.BaseURL, &item.ConnectorType, &item.EndpointNote, &item.KeyNote, &item.DisplayHead, &item.DisplayTail, &item.UpstreamModelID, &item.Ord, &item.EndpointEnabled, &item.KeyEnabled, &item.SuspensionState, &item.CatalogEligible)
			item.ID, _ = decimalID(id)
			item.EndpointKeyID, _ = decimalID(keyID)
			item.EndpointID, _ = decimalID(endpointID)
			return item, err
		})
	})
	return automationPage(result), err
}
