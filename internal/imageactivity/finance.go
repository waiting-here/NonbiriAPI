package imageactivity

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func codedSketch(ctx context.Context, tx *sql.Tx, code string) (ledger.SketchAccounts, error) {
	paper, err := ledger.CodedAssetAccount(ctx, tx, code, ledger.SketchPaper)
	if err != nil {
		return ledger.SketchAccounts{}, err
	}
	brush, err := ledger.CodedAssetAccount(ctx, tx, code, ledger.SketchBrush)
	if err != nil {
		return ledger.SketchAccounts{}, err
	}
	return ledger.SketchAccounts{Paper: paper.ID, Brush: brush.ID}, nil
}
func walletsTx(ctx context.Context, tx *sql.Tx, user int64) (ledger.SketchAccounts, error) {
	paper, err := ledger.UserAssetAccount(ctx, tx, user, ledger.SketchPaper)
	if err != nil {
		return ledger.SketchAccounts{}, err
	}
	brush, err := ledger.UserAssetAccount(ctx, tx, user, ledger.SketchBrush)
	if err != nil {
		return ledger.SketchAccounts{}, err
	}
	return ledger.SketchAccounts{Paper: paper.ID, Brush: brush.ID}, nil
}

// finishFinancialTx is the only terminal posting rail. The ledger reservation
// callback first wins the domain CAS; its operation link is added only after
// ConsumeReserved inserts the matching immutable ledger row.
func (s *Service) finishFinancialTx(ctx context.Context, tx *sql.Tx, row taskRow, state, code string, images int, now int64, deleting bool) (bool, error) {
	if row.finance != "reserved" {
		return false, nil
	}
	if row.user <= 0 {
		return false, ErrInvariant
	}
	operation, err := newID("op_")
	if err != nil {
		return false, err
	}
	escrow, err := codedSketch(ctx, tx, "image_activity_reserve")
	if err != nil {
		return false, err
	}
	meta := ledger.Meta{OperationID: operation, CreatedAt: now}
	var plan ledger.Plan
	finance := "refunded"
	if deleting {
		external, e := codedSketch(ctx, tx, "external")
		if e != nil {
			return false, e
		}
		plan, err = ledger.NewImageDeleteFinalize(meta, row.id, escrow, external, row.price)
		finance = "deleted"
	} else if state == "succeeded" {
		external, e := codedSketch(ctx, tx, "external")
		if e != nil {
			return false, e
		}
		plan, err = ledger.NewImageSettle(meta, row.id, escrow, external, row.price)
		finance = "settled"
	} else {
		wallets, e := walletsTx(ctx, tx, row.user)
		if e != nil {
			return false, e
		}
		plan, err = ledger.NewImageRefund(meta, row.id, escrow, wallets, row.price)
	}
	if err != nil {
		return false, err
	}
	ref, err := ledger.ImageTaskReservation(row.id)
	if err != nil {
		return false, err
	}
	slot := "none"
	if state == "unknown_refunded" {
		slot = "uncertain"
		if row.slot == "ignored" {
			slot = "ignored"
		}
	}
	if deleting && (state == "dispatching" || state == "running") {
		slot = row.slot
	}
	var expires any
	if state == "succeeded" {
		expires = now + resultLifetime
	}
	_, err = ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		return requireOne(tx.ExecContext(ctx, `UPDATE image_activity_tasks SET state=?,finance_state=?,slot_state=?,ledger_rows_remaining=?,completed_at=?,actual_images=?,result_expires_at=?,error_code=?,updated_at=? WHERE id=? AND finance_state='reserved' AND state=?`, state, finance, slot, db.EncodeU128(db.U128{}), now, images, expires, nullableString(code), now, row.id, row.state))
	})
	if err != nil {
		return false, err
	}
	if !deleting {
		if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_tasks SET terminal_operation_id=? WHERE id=? AND finance_state=?", operation, row.id, finance)); err != nil {
			return false, err
		}
	}
	if state != "unknown_refunded" && !(deleting && (state == "dispatching" || state == "running")) {
		_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET upstream_revision=NULL,control_id=NULL,upstream_task_id=NULL,next_poll_at=NULL,cleanup_deadline=NULL WHERE id=?", row.id)
	}
	return err == nil, err
}
func (s *Service) cancelQueuedTx(ctx context.Context, tx *sql.Tx, predicate string, args []any, code string, now int64) ([]string, error) {
	// predicate is a fixed internal SQL fragment, never request text.
	query := "SELECT " + taskColumns + " FROM image_activity_tasks WHERE state='queued'"
	if predicate != "" {
		query += " AND (" + predicate + ")"
	}
	query += " ORDER BY accepted_seq"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	tasks := []taskRow{}
	for rows.Next() {
		r, e := scanTask(rows)
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		tasks = append(tasks, r)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tasks))
	for _, r := range tasks {
		won, e := s.finishFinancialTx(ctx, tx, r, "cancelled", code, 0, now, false)
		if e != nil {
			return nil, e
		}
		if won {
			ids = append(ids, r.id)
		}
	}
	return ids, nil
}
