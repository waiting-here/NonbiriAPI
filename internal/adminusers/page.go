package adminusers

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/queryread"
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

func (s *Service) beginListRead(ctx context.Context, adminID int64) (*sql.Tx, error) {
	return s.beginManagement(ctx, adminID, roleAdmin, true)
}

func listPageQuery(ctx context.Context, tx *sql.Tx, selection, order, pageOrder string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	statement, params, meta, err := queryread.Page(ctx, tx, selection, order, pageOrder, args, requested, limit)
	if errors.Is(err, pagination.ErrInvalid) {
		return "", nil, nil, ErrInvariant
	}
	if err != nil {
		return "", nil, nil, classifyDatabaseError("count page", err)
	}
	return statement, params, meta, nil
}
