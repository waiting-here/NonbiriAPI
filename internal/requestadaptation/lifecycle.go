package requestadaptation

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// ExportRequestAdaptations returns only explicitly safe endpoint rules. It
// never projects fixed values, body JSON, ciphertext, or charity settings.
func (s *Store) ExportRequestAdaptations(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) ([]lifecycle.RequestAdaptationExport, error) {
	if s == nil || s.db == nil || ctx == nil || tx == nil || request.UserID <= 0 || request.Limit < 1 || request.Limit > lifecycle.CollectionLimit {
		return nil, lifecycle.ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT ra.endpoint_id FROM request_adaptations ra
JOIN endpoints e ON e.id=ra.endpoint_id
WHERE ra.scope='endpoint' AND e.user_id=? ORDER BY ra.endpoint_id LIMIT ?`, request.UserID, request.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("request adaptation: list export: %w", err)
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("request adaptation: scan export: %w", err)
		}
		ids = append(ids, id)
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
	for _, id := range ids {
		snapshot, err := s.LoadTx(ctx, tx, Ref{Scope: ScopeEndpoint, ID: id})
		if err != nil || snapshot.Revision == "0" {
			snapshot.Clear()
			return nil, ErrUnavailable
		}
		projection := snapshot.Projection()
		out = append(out, lifecycle.RequestAdaptationExport{
			EndpointID: strconv.FormatInt(id, 10), Revision: projection.Revision,
			ForwardHeaders:       append([]string{}, projection.ForwardHeaders.Values...),
			FixedHeaders:         projectionValues(projection.FixedHeaders),
			BodyDefaults:         projectionValues(projection.BodyDefaults),
			BodyForced:           projectionValues(projection.BodyForced),
			NativeExtensionPaths: append([]string{}, projection.NativeExtensionPaths.Values...),
		})
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
		out = append(out, lifecycle.AdaptationValueExport{Path: key, HasValue: section.Values[key].HasValue})
	}
	return out
}

// PrepareDelete runs before endpoint rows are removed by the resource owner.
// The adaptation row itself follows its endpoint FK in the shared transaction.
func (s *Store) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if s == nil || s.db == nil || ctx == nil || tx == nil || request.UserID <= 0 || !request.Source.Valid() {
		return nil, lifecycle.ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_adaptation_audits WHERE scope='endpoint'
AND resource_id IN (SELECT id FROM endpoints WHERE user_id=?)`, request.UserID); err != nil {
		return nil, fmt.Errorf("request adaptation: delete endpoint audits: %w", err)
	}
	return nil, nil
}

var _ lifecycle.RequestAdaptationLifecycle = (*Store)(nil)
