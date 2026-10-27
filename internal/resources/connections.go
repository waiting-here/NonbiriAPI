package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const (
	routeConnections           = "/api/connections"
	routeConnection            = "/api/connections/{id}"
	routeConnectionEntries     = "/api/connections/{id}/entries"
	routeConnectionCredentials = "/api/connections/{id}/credentials"
	routeCredential            = "/api/credentials/{id}"
	routeCredentialVersions    = "/api/credentials/{id}/versions"
	routeCredentialVersion     = "/api/credentials/{id}/versions/{version}"
	routeCredentialImpact      = "/api/credentials/{id}/impact"
	routeConnectionSources     = "/api/connections/{id}/discovery-sources"
	routeDiscoverySource       = "/api/discovery-sources/{id}"
	routeSourceRefresh         = "/api/discovery-sources/{id}/refresh"
	routeSourceModels          = "/api/discovery-sources/{id}/models"
)

// Services are the ownership boundary for reusable upstream keys.
const connectionCredentialMembership = `cr.connection_id=sc.id`

const connectionSelect = `SELECT sc.id,sc.name,sc.note,sc.revision,
(SELECT count(*) FROM endpoints e WHERE e.connection_id=sc.id AND e.user_id=sc.user_id),
(SELECT count(*) FROM credentials cr WHERE cr.user_id=sc.user_id AND cr.deleted_at IS NULL AND ` + connectionCredentialMembership + `),
sc.created_at,sc.updated_at,pe.id,pe.base_url,pe.note,pe.created_at
FROM service_connections sc LEFT JOIN endpoints pe ON pe.id=(
 SELECT e.id FROM endpoints e WHERE e.connection_id=sc.id AND e.user_id=sc.user_id ORDER BY e.id LIMIT 1)
WHERE sc.user_id=?`

func scanConnection(scanner pageScanner) (Connection, error) {
	var item Connection
	var id, revision, entryCount, credentialCount int64
	var entryID, entryCreatedAt sql.NullInt64
	var entryBaseURL, entryNote sql.NullString
	if err := scanner.Scan(&id, &item.Name, &item.Note, &revision, &entryCount, &credentialCount, &item.CreatedAt, &item.UpdatedAt,
		&entryID, &entryBaseURL, &entryNote, &entryCreatedAt); err != nil {
		return Connection{}, err
	}
	if id <= 0 || revision <= 0 || entryCount < 0 || credentialCount < 0 ||
		!validateExactText(item.Name, 1, 128) || !validateNote(item.Note) {
		return Connection{}, ErrUnavailable
	}
	item.ID, item.Revision = strconv.FormatInt(id, 10), strconv.FormatInt(revision, 10)
	item.EntryCount, item.CredentialCount = strconv.FormatInt(entryCount, 10), strconv.FormatInt(credentialCount, 10)
	item.DisplayName = item.Name
	if entryID.Valid {
		primaryID, err := decimalID(entryID.Int64)
		if err != nil {
			return Connection{}, ErrUnavailable
		}
		item.PrimaryEntry = &ConnectionPrimaryEntry{ID: primaryID, BaseURL: entryBaseURL.String, Note: entryNote.String}
		if revision == 1 && item.Note == "" && item.Name == "Connection "+item.ID && entryID.Int64 == id && entryCreatedAt.Int64 == item.CreatedAt {
			if note := strings.TrimSpace(entryNote.String); note != "" {
				item.DisplayName = note
			} else if parsed, err := url.Parse(entryBaseURL.String); err == nil && parsed.Hostname() != "" {
				item.DisplayName = parsed.Hostname()
			}
		}
	}
	return item, nil
}

func getConnectionTx(ctx context.Context, tx *sql.Tx, userID, connectionID int64) (Connection, error) {
	item, err := scanConnection(tx.QueryRowContext(ctx, connectionSelect+` AND sc.id=?`, userID, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, ErrNotFound
	}
	return item, err
}

func (r *Repository) GetConnection(ctx context.Context, userID, connectionID int64) (Connection, error) {
	if connectionID <= 0 {
		return Connection{}, ErrNotFound
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Connection, error) {
		return getConnectionTx(ctx, tx, userID, connectionID)
	})
}

func (r *Repository) listConnections(ctx context.Context, userID int64, search string, window connectionListWindow) (Page[Connection], error) {
	if !validateFreeText(search, 0, 128) {
		return Page[Connection]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[Connection], error) {
		query, args := connectionListQuery(userID, search)
		return readConnectionList(ctx, tx, r, window, "user-connections", query, "sc.id", args, scanConnection, func(item Connection) string { return item.ID })
	})
}

func connectionListQuery(userID int64, search string) (string, []any) {
	query, args := connectionSelect, []any{userID}
	if search = strings.TrimSpace(search); search != "" {
		query += ` AND (instr(lower(sc.name),lower(?))>0 OR instr(lower(sc.note),lower(?))>0
 OR EXISTS(SELECT 1 FROM endpoints e WHERE e.connection_id=sc.id AND e.user_id=sc.user_id
 AND (instr(lower(e.base_url),lower(?))>0 OR instr(lower(e.note),lower(?))>0)))`
		args = append(args, search, search, search, search)
	}
	return query, args
}

func (r *Repository) listConnectionEntries(ctx context.Context, userID, connectionID int64, filters EndpointPageFilters, window connectionListWindow) (Page[Endpoint], error) {
	if !r.validEndpointPageFilters(filters) || connectionID <= 0 {
		return Page[Endpoint]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[Endpoint], error) {
		if err := requireConnectionTx(ctx, tx, userID, connectionID); err != nil {
			return Page[Endpoint]{}, err
		}
		query, args := endpointPageQuery(userID, filters)
		query += ` AND e.connection_id=?`
		args = append(args, connectionID)
		page, err := readConnectionList(ctx, tx, r, window, "connection-entries", query, "e.id", args,
			func(s pageScanner) (Endpoint, error) { return scanEndpoint(s) }, func(item Endpoint) string { return item.ID })
		if err != nil {
			return Page[Endpoint]{}, err
		}
		for i := range page.Data {
			page.Data[i].Browse, err = endpointBrowseTx(ctx, tx, userID, page.Data[i])
			if err != nil {
				return Page[Endpoint]{}, err
			}
		}
		return page, nil
	})
}

func effectiveEndpointLimitTx(ctx context.Context, tx *sql.Tx, userID int64) (int64, error) {
	var override sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT endpoint_limit FROM users WHERE id=? AND is_admin=0`, userID).Scan(&override); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	limit, err := readSiteLimitTx(ctx, tx, "default_endpoint_limit", 0, 10000)
	if err != nil {
		return 0, err
	}
	if override.Valid {
		limit = override.Int64
	}
	if limit < 0 || limit > 10000 {
		return 0, ErrUnavailable
	}
	return limit, nil
}

func createConnectionTx(ctx context.Context, tx *sql.Tx, userID int64, input ConnectionInput, now int64) (int64, error) {
	if !validateExactText(input.Name, 1, 128) || !validateNote(input.Note) {
		return 0, ErrInvalidRequest
	}
	limit, err := effectiveEndpointLimitTx(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	count, err := countTx(ctx, tx, `SELECT count(*) FROM service_connections WHERE user_id=?`, userID)
	if err != nil {
		return 0, err
	}
	if count >= limit {
		return 0, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO service_connections(user_id,name,note,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, userID, input.Name, input.Note, now, now)
	if err != nil {
		return 0, fmt.Errorf("resources: create connection: %w", err)
	}
	return result.LastInsertId()
}

func (r *Repository) CreateConnection(ctx context.Context, userID int64, mutation ControlMutation, input ConnectionInput) (MutationResult[Connection], error) {
	if r == nil || userID <= 0 || !validResourceMutation(mutation, http.MethodPost, routeConnections) ||
		!validateExactText(input.Name, 1, 128) || !validateNote(input.Note) {
		return MutationResult[Connection]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusCreated, "connection", func(ctx context.Context, tx *sql.Tx, now int64) (Connection, ResourceOperationResult, error) {
		id, err := createConnectionTx(ctx, tx, userID, input, now)
		if err != nil {
			return Connection{}, ResourceOperationResult{}, err
		}
		item, err := getConnectionTx(ctx, tx, userID, id)
		return item, ResourceOperationResult{ConnectionID: item.ID}, err
	})
}

func (r *Repository) PatchConnection(ctx context.Context, userID, connectionID int64, mutation ControlMutation, input PatchConnectionInput) (MutationResult[Connection], error) {
	if r == nil || userID <= 0 || connectionID <= 0 || !validResourceMutation(mutation, http.MethodPatch, routeConnection, connectionID) ||
		input.ExpectedRevision < 1 || input.ExpectedRevision == math.MaxInt64 || (input.Name == nil && input.Note == nil) ||
		(input.Name != nil && !validateExactText(*input.Name, 1, 128)) || (input.Note != nil && !validateNote(*input.Note)) {
		return MutationResult[Connection]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusOK, "", func(ctx context.Context, tx *sql.Tx, now int64) (Connection, ResourceOperationResult, error) {
		item, err := getConnectionTx(ctx, tx, userID, connectionID)
		if err != nil {
			return Connection{}, ResourceOperationResult{}, err
		}
		if item.Revision != strconv.FormatInt(input.ExpectedRevision, 10) {
			return Connection{}, ResourceOperationResult{}, ErrConflict
		}
		if input.Name != nil {
			item.Name = *input.Name
		}
		if input.Note != nil {
			item.Note = *input.Note
		}
		result, err := tx.ExecContext(ctx, `UPDATE service_connections SET name=?,note=?,revision=revision+1,updated_at=? WHERE id=? AND user_id=? AND revision=?`, item.Name, item.Note, now, connectionID, userID, input.ExpectedRevision)
		if err != nil {
			return Connection{}, ResourceOperationResult{}, err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return Connection{}, ResourceOperationResult{}, ErrConflict
		}
		item.Revision, item.UpdatedAt = strconv.FormatInt(input.ExpectedRevision+1, 10), now
		item.DisplayName = item.Name
		return item, ResourceOperationResult{}, nil
	})
}
