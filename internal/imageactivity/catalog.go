package imageactivity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type CatalogQuery struct {
	Q               string
	Type            string
	Configured      string
	Enabled         string
	Page            int
	PageSize        int
	CatalogRevision string
}

type CatalogPage struct {
	Data     []AdminModel `json:"data"`
	Total    int          `json:"total"`
	Revision string       `json:"revision"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func (q *CatalogQuery) normalize() error {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Type == "" {
		q.Type = "image"
	}
	if q.Configured == "" {
		q.Configured = "all"
	}
	if q.Enabled == "" {
		q.Enabled = "all"
	}
	if q.Page < 1 || q.Page > 1000 || q.PageSize != 20 && q.PageSize != 50 && q.PageSize != 100 ||
		q.Type != "image" && q.Type != "unknown" && q.Type != "other" && q.Type != "all" ||
		q.Configured != "all" && q.Configured != "true" && q.Configured != "false" ||
		q.Enabled != "all" && q.Enabled != "true" && q.Enabled != "false" ||
		len(q.Q) > 128 || len(q.CatalogRevision) > 26 || strings.ContainsAny(q.Q, "\x00\r\n") {
		return ErrInvalid
	}
	return nil
}

// ListCatalog filters the complete bounded discovery set before slicing a
// numbered page. Cursor-based readers continue to use ListModels.
func (s *Service) ListCatalog(ctx context.Context, admin int64, query CatalogQuery) (CatalogPage, error) {
	out := CatalogPage{Data: []AdminModel{}}
	if err := query.normalize(); err != nil {
		return out, err
	}
	out.Page, out.PageSize = query.Page, query.PageSize
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	upstream, err := currentUpstreamTx(ctx, tx)
	if errors.Is(err, ErrUnavailable) {
		out.Revision = "0"
		return out, tx.Commit()
	}
	if err != nil {
		return out, err
	}
	out.Revision = "0"
	err = tx.QueryRowContext(ctx, `SELECT id FROM image_capability_snapshots WHERE control_id=? ORDER BY rowid DESC LIMIT 1`, upstream.controlID).Scan(&out.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if query.CatalogRevision != "" && query.CatalogRevision != out.Revision {
		return out, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.upstream_model_id,m.current_revision,
	 COALESCE(r.display_name,''),COALESCE(r.enabled,0),m.metadata_json,
	 COALESCE(json_extract(c.effective_json,'$.catalog_type'),json_extract(candidate.source_json,'$.catalog_type'),
	 CASE WHEN c.readiness='legacy' THEN 'image' ELSE 'unknown' END),
	 candidate.model_id IS NULL
	 FROM image_activity_models m
	 LEFT JOIN image_model_revisions r ON r.model_id=m.id AND r.revision=m.current_revision
	 LEFT JOIN image_model_revision_policies p ON p.model_id=m.id AND p.model_revision=m.current_revision
	 LEFT JOIN image_model_capability_revisions c ON c.model_id=p.model_id AND c.revision=p.capability_revision
	 LEFT JOIN image_capability_candidates candidate ON candidate.snapshot_id=? AND candidate.model_id=m.id
	 WHERE m.control_id=? ORDER BY lower(m.upstream_model_id),m.id`, out.Revision, upstream.controlID)
	if err != nil {
		return out, err
	}
	type entry struct {
		id, kind string
		missing  bool
	}
	matched := make([]entry, 0)
	search := strings.ToLower(query.Q)
	for rows.Next() {
		var id, upstreamID, display, kind, metadata string
		var current sql.NullInt64
		var enabled, missing bool
		if err = rows.Scan(&id, &upstreamID, &current, &display, &enabled, &metadata, &kind, &missing); err != nil {
			_ = rows.Close()
			return out, err
		}
		configured := current.Valid
		if !configured {
			display = fixedDisplayName(upstreamID, compileFixedCapability([]byte(metadata)).name)
		}
		if query.Type != "all" && kind != query.Type ||
			query.Configured == "true" && !configured || query.Configured == "false" && configured ||
			query.Enabled == "true" && !enabled || query.Enabled == "false" && enabled ||
			search != "" && !strings.Contains(strings.ToLower(upstreamID), search) && !strings.Contains(strings.ToLower(display), search) && !strings.Contains(strings.ToLower(id), search) {
			continue
		}
		matched = append(matched, entry{id, kind, missing && out.Revision != "0"})
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Total = len(matched)
	start := (query.Page - 1) * query.PageSize
	if start < out.Total {
		end := start + query.PageSize
		if end > out.Total {
			end = out.Total
		}
		for _, item := range matched[start:end] {
			model, modelErr := modelTx(ctx, tx, item.id, 0)
			if modelErr != nil {
				return out, modelErr
			}
			view := adminModelView(model)
			view.CatalogType = item.kind
			view.Missing = item.missing
			out.Data = append(out.Data, view)
		}
	}
	return out, tx.Commit()
}
