package logapi

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"time"

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
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, translateSQLError(err)
	}
	if err := authorizePageAccount(ctx, tx, role, actorID, now); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
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
		return translateSQLError(err)
	}
	return nil
}

func logPageQuery(ctx context.Context, reader logReadQueryer, selection, order string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	if requested == nil {
		return selection + order + ` LIMIT ?`, append(args, limit+1), nil, nil
	}
	var total int64
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&total); err != nil {
		return "", nil, nil, translateSQLError(err)
	}
	metadata, offset, err := requested.Window(total)
	if err != nil {
		return "", nil, nil, ErrInvalid
	}
	return selection + order + ` LIMIT ? OFFSET ?`, append(args, requested.Size, offset), &metadata, nil
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
