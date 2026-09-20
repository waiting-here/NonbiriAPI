package activities

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const loanColumns = `id,operation_id,ledger_seq,created_at,config_revision,principal,coefficient_a,coefficient_b,nominal_milli,disbursed_milli,fee_milli,repayment_milli,interest_milli,general_before_sign,general_before_mag,general_after_sign,general_after_mag,game_before_sign,game_before_mag,game_after_sign,game_after_mag`

func loanRowsTx(ctx context.Context, tx *sql.Tx, user int64, limit int, offset int64) ([]LoanReceipt, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+loanColumns+` FROM activity_loans WHERE user_id=? ORDER BY created_at DESC,ledger_seq DESC LIMIT ? OFFSET ?`, user, limit, offset)
	if err != nil {
		return nil, classifyDatabaseError("read loan history", err)
	}
	defer rows.Close()
	items := []LoanReceipt{}
	for rows.Next() {
		var item LoanReceipt
		var seq, rev int64
		var t db.LoanTerms
		var signs [4]int
		var magnitudes [4][]byte
		if err := rows.Scan(&item.ID, &item.OperationID, &seq, &item.CreatedAt, &rev, &t.Principal, &t.A, &t.B, &t.Nominal, &t.Disbursed, &t.Fee, &t.Repayment, &t.Interest, &signs[0], &magnitudes[0], &signs[1], &magnitudes[1], &signs[2], &magnitudes[2], &signs[3], &magnitudes[3]); err != nil {
			return nil, classifyDatabaseError("scan loan history", err)
		}
		expected, err := db.CalculateLoanTerms(strconv.FormatInt(t.Principal, 10), strconv.FormatInt(t.A, 10), strconv.FormatInt(t.B, 10))
		if err != nil || expected != t {
			return nil, ErrInvariant
		}
		item.LoanTerms = projectLoanTerms(t)
		item.Sequence, item.ConfigRevision = strconv.FormatInt(seq, 10), strconv.FormatInt(rev, 10)
		for i, target := range []*string{&item.GeneralBefore, &item.GeneralAfter, &item.GameBefore, &item.GameAfter} {
			value, err := db.NewSM128(signs[i], magnitudes[i])
			if err != nil {
				return nil, ErrInvariant
			}
			*target = formatMilliPoints(value.Big())
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("iterate loan history", err)
	}
	return items, nil
}

// ReadLoansTx joins the caller's authorized management or owner snapshot. It
// performs no authorization itself and never opens a second transaction.
func ReadLoansTx(ctx context.Context, tx *sql.Tx, user int64, page pagination.Request) (Page[LoanReceipt], error) {
	var result Page[LoanReceipt]
	if ctx == nil || tx == nil || user <= 0 || !page.Valid() {
		return result, ErrInvalidRequest
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, user).Scan(&exists); err != nil {
		return result, classifyDatabaseError("read loan history owner", err)
	}
	if !exists {
		return result, ErrNotFound
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM activity_loans WHERE user_id=?`, user).Scan(&total); err != nil {
		return result, classifyDatabaseError("count loan history", err)
	}
	metadata, offset, err := page.Window(total)
	if err != nil {
		return result, ErrInvalidRequest
	}
	result.Data, err = loanRowsTx(ctx, tx, user, page.Size, offset)
	if err != nil {
		return result, err
	}
	result.Pagination = &metadata
	return result, nil
}

func (r *Repository) ListLoans(ctx context.Context, user int64, page pagination.Request) (Page[LoanReceipt], error) {
	var empty Page[LoanReceipt]
	if r == nil || ctx == nil || user <= 0 {
		return empty, ErrInvalidRequest
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, classifyDatabaseError("begin loan history", err)
	}
	defer tx.Rollback()
	if err := classifyAuthorizationError(r.userFinalAuth.AuthorizeUserMutation(ctx, tx, user)); err != nil {
		return empty, err
	}
	result, err := ReadLoansTx(ctx, tx, user, page)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, classifyDatabaseError("commit loan history", err)
	}
	return result, nil
}

// ExportLoansTx is bounded independently of ordinary activity collections.
// The lifecycle coordinator owns authorization and the transaction snapshot.
func (r *Repository) ExportLoansTx(ctx context.Context, tx *sql.Tx, user int64, limit int) ([]LoanReceipt, error) {
	if r == nil || ctx == nil || tx == nil || user <= 0 || limit < 1 || limit > maxActivityExportRows {
		return nil, ErrInvalidRequest
	}
	items, err := loanRowsTx(ctx, tx, user, limit+1, 0)
	if err != nil {
		return nil, err
	}
	if len(items) > limit {
		return nil, ErrResourceLimit
	}
	return items, nil
}
