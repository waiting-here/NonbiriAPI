package logapi

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const logPageTimeout = 5 * time.Second

type logReadQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The route establishes the session identity; page reads repeat its current
// account check in the same snapshot as the authorized count and rows.
func (r *Repository) beginPageRead(ctx context.Context, role string, actorID, now int64) (*sql.Tx, error) {
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, translateReadError(ctx, err)
	}
	if err := r.authorizeRead(ctx, tx, role, actorID, now); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *Repository) authorizeRead(ctx context.Context, tx *sql.Tx, role string, actorID, now int64) error {
	if r.readAuth == nil {
		return authorizePageAccount(ctx, tx, role, actorID, now)
	}
	if err := r.readAuth.AuthorizeLogRead(ctx, tx, role, actorID); err != nil {
		if ctx.Err() == nil && (errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden)) {
			return ErrForbidden
		}
		return translateReadError(ctx, err)
	}
	return nil
}

func authorizePageAccount(ctx context.Context, tx *sql.Tx, role string, actorID, now int64) error {
	var allowed int
	var err error
	if role == "admin" {
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE is_admin=1 AND (is_banned=0 OR banned_until<=?)`, now).Scan(&allowed)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id=? AND is_admin=0 AND (is_banned=0 OR banned_until<=?)`, actorID, now).Scan(&allowed)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return translateReadError(ctx, err)
	}
	return nil
}

func logPageQuery(ctx context.Context, reader logReadQueryer, selection, countSelection, order string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	if requested == nil {
		return selection + order + ` LIMIT ?`, append(args, limit+1), nil, nil
	}
	var total int64
	if err := reader.QueryRowContext(ctx, countSelection, args...).Scan(&total); err != nil {
		return "", nil, nil, translateReadError(ctx, err)
	}
	metadata, offset, err := requested.Window(total)
	if err != nil {
		return "", nil, nil, ErrInvalid
	}
	return selection + order + ` LIMIT ? OFFSET ?`, append(args, requested.Size, offset), &metadata, nil
}

func logListPageQuery(ctx context.Context, reader logReadQueryer, columns, join, where string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	const order = ` ORDER BY l.started_at DESC,l.id DESC`
	selection, args, metadata, err := logPageQuery(ctx, reader,
		`SELECT l.id FROM request_logs l `+where,
		`SELECT COUNT(*) FROM request_logs l `+where, order, args, requested, limit)
	if err != nil {
		return "", nil, nil, err
	}
	// Apply the page window before projecting charges and caller history. A
	// selective attempt filter may require sorting its matches; that work must
	// not evaluate correlated display columns for every matching request.
	return `WITH log_page AS MATERIALIZED (` + selection + `) SELECT ` + columns +
		` FROM log_page p JOIN request_logs l ON l.id=p.id` + join + order, args, metadata, nil
}

func parseLogPage(values url.Values, prefix string) (*pagination.Request, error) {
	pageValues := url.Values{}
	for _, name := range []string{"page", "page_size", "cursor", "limit"} {
		if entries, exists := values[prefix+name]; exists {
			pageValues[name] = entries
		}
	}
	page, selected, err := pagination.Parse(pageValues)
	if err != nil {
		return nil, ErrInvalid
	}
	if !selected {
		return nil, nil
	}
	return &page, nil
}
