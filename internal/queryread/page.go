// Package queryread owns the mechanics of bounded database reads. Domains own
// authorization, predicates, ordering, projections, and public error semantics.
package queryread

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const DefaultTimeout = 5 * time.Second

// MaterializePage is for a bounded selection, never an unlimited export. The
// result order must refer to output columns, including a unique tie breaker.
// All SQL fragments are trusted code; user values belong in parameters.
func MaterializePage(selection, order string) string {
	return `WITH query_page AS MATERIALIZED (` + selection + `) SELECT * FROM query_page ` + order
}

// Window counts the same authorized/filtered selection used for the page. The
// caller must use this transaction for page rows and any derived projections.
// SQLite prunes unused projection columns from this count where safe to do so.
func Window(ctx context.Context, tx *sql.Tx, selection string, args []any, request pagination.Request) (pagination.Metadata, int64, error) {
	if !request.Valid() {
		return pagination.Metadata{}, 0, pagination.ErrInvalid
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&total); err != nil {
		return pagination.Metadata{}, 0, err
	}
	return request.Window(total)
}

// Page supports exact numbered pages and legacy lookahead cursors. The caller
// places the cursor predicate in selection. Empty resultOrder is reserved for
// a narrow ID selection embedded by the caller in its own materialized CTE,
// followed by page-only detail lookup and an explicit final ORDER BY.
func Page(ctx context.Context, tx *sql.Tx, selection, order, resultOrder string, args []any, request *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	params := append([]any(nil), args...)
	statement := selection + order + ` LIMIT ?`
	var metadata *pagination.Metadata
	if request == nil {
		if limit < 1 || limit > 1000 {
			return "", nil, nil, pagination.ErrInvalid
		}
		params = append(params, limit+1)
	} else {
		meta, offset, err := Window(ctx, tx, selection, args, *request)
		if err != nil {
			return "", nil, nil, err
		}
		statement += ` OFFSET ?`
		params = append(params, request.Size, offset)
		metadata = &meta
	}
	if resultOrder != "" {
		statement = MaterializePage(statement, resultOrder)
	}
	return statement, params, metadata, nil
}

type Scanner interface{ Scan(...any) error }

// Collect closes the cursor before callers run other queries on its connection.
// On errors it returns no partial result that could be mistaken for a full page.
func Collect[T any](ctx context.Context, tx *sql.Tx, statement string, args []any, scan func(Scanner) (T, error)) ([]T, error) {
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, nil
}
