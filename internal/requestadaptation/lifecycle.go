package requestadaptation

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// ExportRequestAdaptations returns only explicitly safe endpoint and personal model rules. It
// never projects fixed values, body JSON, ciphertext, or charity settings.
func (s *Store) ExportRequestAdaptations(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) ([]lifecycle.RequestAdaptationExport, error) {
	if s == nil || s.db == nil || ctx == nil || tx == nil || request.UserID <= 0 || request.Limit < 1 || request.Limit > lifecycle.CollectionLimit {
		return nil, lifecycle.ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT ra.scope,COALESCE(ra.endpoint_id,ra.personal_model_id) FROM request_adaptations ra
LEFT JOIN endpoints e ON e.id=ra.endpoint_id
LEFT JOIN models m ON m.id=ra.personal_model_id
WHERE (ra.scope='endpoint' AND e.user_id=?) OR (ra.scope='personal_model' AND m.user_id=?)
ORDER BY ra.scope,COALESCE(ra.endpoint_id,ra.personal_model_id) LIMIT ?`, request.UserID, request.UserID, request.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("request adaptation: list export: %w", err)
	}
	ids := make([]Ref, 0)
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.Scope, &ref.ID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("request adaptation: scan export: %w", err)
		}
		ids = append(ids, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("request adaptation: read export: %w", err)
	}
	rows.Close()
	if len(ids) > request.Limit {
		return nil, lifecycle.ErrTooLarge
	}
	out := make([]lifecycle.RequestAdaptationExport, 0, len(ids))
	for _, ref := range ids {
		snapshot, err := s.LoadTx(ctx, tx, ref)
		if err != nil || snapshot.Revision == "0" {
			snapshot.Clear()
			return nil, ErrUnavailable
		}
		projection := snapshot.Projection()
		item := lifecycle.RequestAdaptationExport{
			Revision:              projection.Revision,
			ExcludedRequestFields: projection.ExcludedRequestFields,
			Modes:                 map[string]string{"forward_headers": string(projection.ForwardHeaders.Mode), "fixed_headers": string(projection.FixedHeaders.Mode), "body_defaults": string(projection.BodyDefaults.Mode), "body_forced": string(projection.BodyForced.Mode), "native_extension_paths": string(projection.NativeExtensionPaths.Mode)},
			ForwardHeaders:        append([]string{}, projection.ForwardHeaders.Values...),
			FixedHeaders:          projectionValues(projection.FixedHeaders),
			BodyDefaults:          projectionValues(projection.BodyDefaults),
			BodyForced:            projectionValues(projection.BodyForced),
			NativeExtensionPaths:  append([]string{}, projection.NativeExtensionPaths.Values...),
		}
		if ref.Scope == ScopeEndpoint {
			item.EndpointID = strconv.FormatInt(ref.ID, 10)
		} else {
			item.ModelID = strconv.FormatInt(ref.ID, 10)
		}
		out = append(out, item)
		snapshot.Clear()
	}
	return out, nil
}

func projectionValues(section MapProjection) []lifecycle.AdaptationValueExport {
	keys := make([]string, 0, len(section.Values))
	for key := range section.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]lifecycle.AdaptationValueExport, 0, len(keys))
	for _, key := range keys {
		out = append(out, lifecycle.AdaptationValueExport{Path: key, HasValue: section.Values[key].HasValue, SensitivePaths: append([]string(nil), section.Values[key].SensitivePaths...)})
	}
	return out
}

// PrepareDelete removes audit ownership before resource deletion. Configuration
// rows follow their endpoint/model FKs in the same transaction.
func (s *Store) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if s == nil || s.db == nil || ctx == nil || tx == nil || request.UserID <= 0 || !request.Source.Valid() {
		return nil, lifecycle.ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_adaptation_audits WHERE (scope='endpoint'
AND resource_id IN (SELECT id FROM endpoints WHERE user_id=?)) OR (scope='personal_model' AND resource_id IN (SELECT id FROM models WHERE user_id=?))`, request.UserID, request.UserID); err != nil {
		return nil, fmt.Errorf("request adaptation: delete owner audits: %w", err)
	}
	return nil, nil
}

var _ lifecycle.RequestAdaptationLifecycle = (*Store)(nil)
