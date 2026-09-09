package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const legalHoldPageSelection = `SELECT id,object_kind,object_ref,
CASE WHEN state='active' AND expires_at<=?1 THEN 'expired' ELSE state END AS state,
CASE WHEN state='active' AND expires_at<=?1 THEN revision+1 ELSE revision END AS revision,
basis,created_at,expires_at,
CASE WHEN state='active' AND expires_at<=?1 THEN expires_at ELSE ended_at END AS ended_at,
CASE WHEN state='active' AND expires_at<=?1 THEN 'expired' ELSE end_reason END AS end_reason,
CASE WHEN state='active' AND expires_at<=?1 THEN expires_at+?2 ELSE retain_until END AS retain_until
FROM legal_holds`

const legalHoldPageFiltered = `SELECT * FROM (` + legalHoldPageSelection + `)
WHERE (?3='' OR state=?3) AND (?4='' OR object_kind=?4) AND (state='active' OR retain_until>?1)`

const legalHoldPageOrder = ` ORDER BY created_at DESC,id DESC LIMIT ?5 OFFSET ?6`

func (c *Coordinator) legalHoldPageTx(ctx context.Context, adminID int64) (*sql.Tx, error) {
	tx, err := c.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("lifecycle: begin legal hold page: %w", err)
	}
	if err := c.adminAuth.AuthorizeAdmin(ctx, tx, adminID); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (c *Coordinator) listLegalHoldsPage(ctx context.Context, filter LegalHoldListFilter) (LegalHoldPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Authorize before the existing expiry maintenance, then reauthorize in
	// the snapshot that owns both count and rows.
	preflight, err := c.legalHoldPageTx(ctx, filter.AdminID)
	if err != nil {
		return LegalHoldPage{}, err
	}
	if err = preflight.Commit(); err != nil {
		return LegalHoldPage{}, err
	}
	// Bound maintenance work independently of the number of due records. The
	// read projection below includes any remaining logical expirations.
	if _, err = c.expireDueHolds(ctx, filter.DecisionNow, WorkerBatchLimit, time.Now().Add(WorkerBudget)); err != nil {
		return LegalHoldPage{}, err
	}
	tx, err := c.legalHoldPageTx(ctx, filter.AdminID)
	if err != nil {
		return LegalHoldPage{}, err
	}
	defer tx.Rollback()
	// Include both later commits and due holds beyond the maintenance batch
	// using their exact expiry state in the same count-and-row snapshot.
	args := []any{filter.DecisionNow, int64(LegalHoldMetadataLife / time.Second), filter.State, string(filter.Kind)}
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+legalHoldPageFiltered+`)`, args...).Scan(&total); err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: count legal holds: %w", err)
	}
	metadata, offset, err := filter.Page.Window(total)
	if err != nil {
		return LegalHoldPage{}, ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, legalHoldPageFiltered+legalHoldPageOrder, append(args, filter.Page.Size, offset)...)
	if err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: query legal hold page: %w", err)
	}
	defer rows.Close()
	items := []LegalHoldSummary{}
	for rows.Next() {
		row, err := scanLegalHold(rows)
		if err != nil {
			return LegalHoldPage{}, err
		}
		summary, err := row.summary()
		if err != nil {
			return LegalHoldPage{}, err
		}
		items = append(items, summary)
	}
	if err = rows.Err(); err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: iterate legal hold page: %w", err)
	}
	if err = rows.Close(); err != nil {
		return LegalHoldPage{}, err
	}
	if err = tx.Commit(); err != nil {
		return LegalHoldPage{}, fmt.Errorf("lifecycle: commit legal hold page: %w", err)
	}
	return LegalHoldPage{Data: items, Pagination: &metadata}, nil
}
