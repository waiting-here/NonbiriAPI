package adminusers

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const pageReadTimeout = 5 * time.Second

func normalizePageLimit(page *pagination.Request, cursor string, limit int) int {
	if page == nil {
		return normalizeLimit(limit)
	}
	if !page.Valid() || cursor != "" || limit != 0 {
		return 0
	}
	return page.Size
}

func (s *Service) beginListRead(ctx context.Context, adminID int64, numbered bool) (*sql.Tx, error) {
	if !numbered {
		return s.beginAuthorized(ctx, adminID)
	}
	if s == nil || s.database == nil || ctx == nil || adminID <= 0 {
		return nil, ErrUnauthorized
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classifyDatabaseError("begin page read", err)
	}
	if err := s.finalAuth.AuthorizeAdmin(ctx, tx, adminID); err != nil {
		tx.Rollback()
		return nil, classifyAuthorizationError(err)
	}
	return tx, nil
}

func listPageQuery(ctx context.Context, tx *sql.Tx, selection, order string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	if requested == nil {
		return selection + order + ` LIMIT ?`, append(args, limit+1), nil, nil
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&total); err != nil {
		return "", nil, nil, classifyDatabaseError("count page", err)
	}
	metadata, offset, err := requested.Window(total)
	if err != nil {
		return "", nil, nil, ErrInvariant
	}
	return selection + order + ` LIMIT ? OFFSET ?`, append(args, requested.Size, offset), &metadata, nil
}
