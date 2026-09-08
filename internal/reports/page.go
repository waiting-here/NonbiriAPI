package reports

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const pageReadTimeout = 5 * time.Second

func (r *Repository) ListCases(ctx context.Context, actor authz.Actor, status, cursor string, limit int) (Page[CaseSummary], error) {
	return r.listCases(ctx, actor, status, cursor, limit, nil)
}
func (r *Repository) CaseDetail(ctx context.Context, actor authz.Actor, id, cursor string, limit int) (CaseDetail, error) {
	return r.caseDetail(ctx, actor, id, cursor, limit, nil)
}
func (r *Repository) Targets(ctx context.Context, actor authz.Actor, id, cursor string, limit int) (Page[Target], error) {
	return r.targets(ctx, actor, id, cursor, limit, nil)
}
func (r *Repository) TargetDonations(ctx context.Context, actor authz.Actor, id, target, cursor string, limit int) (Page[ReportDonationMatch], error) {
	return r.targetDonations(ctx, actor, id, target, cursor, limit, nil)
}

func validPageRequest(requested *pagination.Request, cursor string, limit int) bool {
	return validPageLimit(limit) && (requested == nil || requested.Valid() && cursor == "" && requested.Size == limit)
}

func (r *Repository) beginCasePageRead(ctx context.Context, actor authz.Actor, numbered bool) (*sql.Tx, *bool, error) {
	if !numbered {
		return r.beginAdminRead(ctx, actor)
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	if _, err := r.authorizeAdminTx(ctx, tx, actor); err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	return tx, new(bool), nil
}

func reportPageSQL(ctx context.Context, tx *sql.Tx, selection, order string, args []any, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	if requested == nil {
		return selection + order + ` LIMIT ?`, append(args, limit+1), nil, nil
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&total); err != nil {
		return "", nil, nil, fmt.Errorf("reports: count page: %w", err)
	}
	metadata, offset, err := requested.Window(total)
	if err != nil {
		return "", nil, nil, ErrInvariant
	}
	return selection + order + ` LIMIT ? OFFSET ?`, append(args, requested.Size, offset), &metadata, nil
}

func reportReadWindow(values url.Values, prefix string) (int, *pagination.Request, error) {
	adapted := url.Values{}
	for _, key := range []string{"page", "page_size", "cursor", "limit"} {
		if entries, ok := values[prefix+key]; ok {
			adapted[key] = entries
		}
	}
	requested, selected, err := pagination.Parse(adapted)
	if err != nil {
		return 0, nil, ErrInvalidRequest
	}
	if selected {
		return requested.Size, &requested, nil
	}
	limit, err := queryLimit(values, prefix+"limit")
	return limit, nil, err
}
