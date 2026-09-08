package resources

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type pageScanner interface{ Scan(...any) error }

// The query is an internal SELECT with its complete owner and filter scope.
// Counting that same query prevents count and row predicates from diverging.
func readNumberedPage[T any](ctx context.Context, tx *sql.Tx, request pagination.Request, query, order string, args []any, scan func(pageScanner) (T, error)) (Page[T], error) {
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+`)`, args...).Scan(&total); err != nil {
		return Page[T]{}, err
	}
	meta, offset, err := request.Window(total)
	if err != nil {
		return Page[T]{}, ErrInvalidRequest
	}
	params := append(append([]any(nil), args...), request.Size, offset)
	rows, err := tx.QueryContext(ctx, query+` ORDER BY `+order+` LIMIT ? OFFSET ?`, params...)
	if err != nil {
		return Page[T]{}, err
	}
	defer rows.Close()
	page := Page[T]{Data: make([]T, 0, request.Size), Pagination: &meta}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return Page[T]{}, err
		}
		page.Data = append(page.Data, item)
	}
	if err := rows.Err(); err != nil {
		return Page[T]{}, err
	}
	return page, nil
}

func userPageRead[T any](ctx context.Context, r *Repository, userID int64, page pagination.Request, read func(context.Context, *sql.Tx) (T, error)) (T, error) {
	var zero T
	if ctx == nil || r == nil || r.db == nil || userID <= 0 || !page.Valid() {
		return zero, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	// This existing boundary checks the current session, role and ban without
	// consuming elevation or changing state for the ordinary-user requirement.
	if err = r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return zero, err
	}
	result, err := read(ctx, tx)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return result, nil
}

const endpointPageSelect = `SELECT e.id,e.connector_type,e.base_url,e.note,e.enabled,e.revision,
(SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id),e.created_at,e.updated_at,
e.mainstream_channel_id,e.mainstream_channel_revision,e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoints e WHERE e.user_id=?`

func (r *Repository) ListEndpointsPage(ctx context.Context, userID int64, page pagination.Request) (Page[Endpoint], error) {
	return r.SearchEndpointsPage(ctx, userID, "", page)
}

func (r *Repository) SearchEndpointsPage(ctx context.Context, userID int64, search string, page pagination.Request) (Page[Endpoint], error) {
	if !validateFreeText(search, 0, 128) {
		return Page[Endpoint]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[Endpoint], error) {
		query := endpointPageSelect
		args := []any{userID}
		if search != "" {
			query += ` AND (instr(lower(e.base_url),lower(?))>0 OR instr(lower(e.note),lower(?))>0
OR instr(lower(COALESCE(e.mainstream_channel_name,'')),lower(?))>0)`
			args = append(args, search, search, search)
		}
		result, err := readNumberedPage(ctx, tx, page, query, "e.updated_at DESC,e.id DESC", args, func(s pageScanner) (Endpoint, error) { return scanEndpoint(s) })
		if err != nil {
			return result, err
		}
		for i := range result.Data {
			result.Data[i].Browse, err = endpointBrowseTx(ctx, tx, userID, result.Data[i])
			if err != nil {
				return Page[Endpoint]{}, err
			}
		}
		return result, nil
	})
}

func (r *Repository) ListEndpointKeysPage(ctx context.Context, userID, endpointID int64, page pagination.Request) (Page[EndpointKey], error) {
	return r.SearchEndpointKeysPage(ctx, userID, endpointID, "", page)
}

func (r *Repository) SearchEndpointKeysPage(ctx context.Context, userID, endpointID int64, search string, page pagination.Request) (Page[EndpointKey], error) {
	if endpointID <= 0 || !validateFreeText(search, 0, 128) {
		return Page[EndpointKey]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[EndpointKey], error) {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoints WHERE id=? AND user_id=?)`, endpointID, userID).Scan(&exists); err != nil {
			return Page[EndpointKey]{}, err
		}
		if !exists {
			return Page[EndpointKey]{}, ErrNotFound
		}
		query := endpointKeySelect + ` WHERE e.user_id=? AND e.id=?`
		args := []any{userID, endpointID}
		if search != "" {
			query += ` AND (instr(lower(k.note),lower(?))>0 OR instr(lower(k.display_head),lower(?))>0
OR instr(lower(k.display_tail),lower(?))>0)`
			args = append(args, search, search, search)
		}
		result, err := readNumberedPage(ctx, tx, page, query, "k.updated_at DESC,k.id DESC", args, func(s pageScanner) (EndpointKey, error) { return scanEndpointKey(s) })
		if err != nil {
			return result, err
		}
		for i := range result.Data {
			result.Data[i].Browse, err = endpointKeyBrowseTx(ctx, tx, userID, endpointID, result.Data[i])
			if err != nil {
				return Page[EndpointKey]{}, err
			}
		}
		return result, nil
	})
}

func (r *Repository) ListModelsPage(ctx context.Context, userID int64, page pagination.Request) (Page[Model], error) {
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[Model], error) {
		result, err := readNumberedPage(ctx, tx, page, modelSelect+` WHERE m.user_id=?`, "m.updated_at DESC,m.id DESC", []any{userID}, func(s pageScanner) (Model, error) { return scanModel(s) })
		if err != nil {
			return result, err
		}
		for i := range result.Data {
			result.Data[i].Browse, err = modelBrowseTx(ctx, tx, userID, result.Data[i])
			if err != nil {
				return Page[Model]{}, err
			}
		}
		return result, nil
	})
}

const candidatePageSelect = `SELECT e.id,k.id,p.rowid,e.base_url,e.connector_type,e.note,k.display_head,k.display_tail,k.note,p.normalized_model_id,
CASE WHEN p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision THEN 1 ELSE 0 END,
CASE WHEN p.manual_supports>0 THEN 1 ELSE 0 END
FROM model_pair_catalog p JOIN endpoint_keys k ON k.id=p.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
WHERE e.user_id=? AND e.enabled=1 AND k.enabled=1
AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))
AND (?=0 OR e.id=?) AND (?=0 OR k.id=?)
AND (?='' OR instr(p.normalized_model_id,?)>0)
AND (?='' OR (?='automatic' AND p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision) OR (?='manual' AND p.manual_supports>0))`

func (r *Repository) BindingCandidatesPage(ctx context.Context, userID, modelID int64, query CandidateQuery, page pagination.Request) (Page[BindingCandidate], error) {
	if modelID <= 0 || query.EndpointID < 0 || query.KeyID < 0 || query.Cursor != "" || query.Limit != 0 ||
		(query.Source != "" && query.Source != "automatic" && query.Source != "manual") || !validateSearchText(query.Query) {
		return Page[BindingCandidate]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[BindingCandidate], error) {
		if _, err := getModelTx(ctx, tx, userID, modelID); err != nil {
			return Page[BindingCandidate]{}, err
		}
		if query.EndpointID != 0 || query.KeyID != 0 {
			var exists bool
			q := `SELECT EXISTS(SELECT 1 FROM endpoints e WHERE e.user_id=? AND e.id=?)`
			args := []any{userID, query.EndpointID}
			if query.KeyID != 0 {
				q = `SELECT EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=? AND k.id=? AND (?=0 OR e.id=?))`
				args = []any{userID, query.KeyID, query.EndpointID, query.EndpointID}
			}
			if err := tx.QueryRowContext(ctx, q, args...).Scan(&exists); err != nil {
				return Page[BindingCandidate]{}, err
			}
			if !exists {
				return Page[BindingCandidate]{}, ErrNotFound
			}
		}
		args := []any{userID, query.EndpointID, query.EndpointID, query.KeyID, query.KeyID, query.Query, query.Query, query.Source, query.Source, query.Source}
		return readNumberedPage(ctx, tx, page, candidatePageSelect, "e.id,k.id,p.rowid", args, func(s pageScanner) (BindingCandidate, error) {
			var c BindingCandidate
			var endpointID, keyID, pairID int64
			var automatic, manual int
			err := s.Scan(&endpointID, &keyID, &pairID, &c.EndpointBaseURL, &c.ConnectorType, &c.EndpointNote, &c.EndpointKeyDisplayHead, &c.EndpointKeyDisplayTail, &c.EndpointKeyNote, &c.UpstreamModelID, &automatic, &manual)
			if err != nil {
				return c, err
			}
			c.EndpointKeyID, err = decimalID(keyID)
			if err != nil {
				return c, err
			}
			c.SourceTypes = []string{}
			if automatic == 1 {
				c.SourceTypes = append(c.SourceTypes, "automatic")
			}
			if manual == 1 {
				c.SourceTypes = append(c.SourceTypes, "manual")
			}
			return c, nil
		})
	})
}

func (r *Repository) GetCatalogPage(ctx context.Context, userID, endpointID, keyID int64, source string, page pagination.Request) (CatalogView, error) {
	if endpointID <= 0 || keyID <= 0 || (source != "" && source != "automatic" && source != "manual") {
		return CatalogView{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (CatalogView, error) {
		owner, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
		if err != nil {
			return CatalogView{}, err
		}
		evidence, err := owner.evidence()
		if err != nil {
			return CatalogView{}, err
		}
		query := `SELECT c.id,c.source_type,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision,c.created_at,c.updated_at
FROM model_catalog_entries c JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE c.endpoint_key_id=? AND (?='' OR c.source_type=?)`
		entries, err := readNumberedPage(ctx, tx, page, query, "c.id", []any{keyID, source, source}, func(s pageScanner) (CatalogEntry, error) { return scanCatalogEntry(s) })
		if err != nil {
			return CatalogView{}, err
		}
		view := CatalogView{Evidence: evidence, AutomaticEntries: []CatalogEntry{}, ManualEntries: []CatalogEntry{}, Pagination: entries.Pagination}
		for _, entry := range entries.Data {
			if entry.SourceType == "automatic" {
				view.AutomaticEntries = append(view.AutomaticEntries, entry)
			} else {
				view.ManualEntries = append(view.ManualEntries, entry)
			}
		}
		return view, nil
	})
}

func (r *Repository) ListMainstreamChannelsPage(ctx context.Context, adminID int64, state string, page pagination.Request) (Page[MainstreamChannel], error) {
	if state == "" {
		state = mainstreamChannelStateActive
	}
	if ctx == nil || r == nil || r.db == nil || isNilInterface(r.adminFinalAuth) || adminID <= 0 || !page.Valid() || !validMainstreamChannelState(state) {
		return Page[MainstreamChannel]{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page[MainstreamChannel]{}, err
	}
	defer tx.Rollback()
	if err = r.adminFinalAuth.AuthorizeAdminFinalTx(ctx, tx, adminID); err != nil {
		return Page[MainstreamChannel]{}, err
	}
	query := mainstreamChannelSelect
	var args []any
	if state != "all" {
		query += ` WHERE state=?`
		args = []any{state}
	}
	result, err := readNumberedPage(ctx, tx, page, query, "updated_at DESC,id DESC", args, func(s pageScanner) (MainstreamChannel, error) {
		channel, err := scanMainstreamChannel(s)
		if err != nil {
			return channel, err
		}
		return channel, r.validateMainstreamChannelDTO(channel)
	})
	if err != nil {
		return Page[MainstreamChannel]{}, err
	}
	if err = tx.Commit(); err != nil {
		return Page[MainstreamChannel]{}, err
	}
	return result, nil
}
