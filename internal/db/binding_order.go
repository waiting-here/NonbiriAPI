package db

import (
	"context"
	"database/sql"
	"errors"
)

func bindingOrderNames(charity bool) (table, parent, column string) {
	if charity {
		return "charity_model_bindings", "charity_models", "charity_model_id"
	}
	return "model_bindings", "models", "model_id"
}

// CompactModelBindingsTx keeps surviving identities and their dependent rows.
// The caller owns authorization, the transaction and the model revision.
func CompactModelBindingsTx(ctx context.Context, tx *sql.Tx, modelID int64) error {
	return compactBindingsTx(ctx, tx, false, modelID)
}

// CompactCharityBindingsTx is the charity counterpart of CompactModelBindingsTx.
func CompactCharityBindingsTx(ctx context.Context, tx *sql.Tx, modelID int64) error {
	return compactBindingsTx(ctx, tx, true, modelID)
}

func compactBindingsTx(ctx context.Context, tx *sql.Tx, charity bool, modelID int64) error {
	if tx == nil || modelID <= 0 {
		return errors.New("invalid binding order target")
	}
	table, _, column := bindingOrderNames(charity)
	rows, err := tx.QueryContext(ctx, `SELECT id,ord FROM `+table+` WHERE `+column+`=? ORDER BY ord,id`, modelID)
	if err != nil {
		return err
	}
	type binding struct {
		id  int64
		ord int
	}
	var bindings []binding
	for rows.Next() {
		var item binding
		if err := rows.Scan(&item.id, &item.ord); err != nil {
			rows.Close()
			return err
		}
		if item.ord < 0 || item.ord > 255 || len(bindings) >= 256 {
			rows.Close()
			return errors.New("invalid committed binding order")
		}
		bindings = append(bindings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Ascending compaction only writes an empty lower slot, even with UNIQUE.
	for ord, item := range bindings {
		if ord == item.ord {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET ord=? WHERE id=? AND `+column+`=? AND ord=?`, ord, item.id, modelID, item.ord)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("binding compaction changed concurrently")
		}
	}
	return nil
}

// ReorderBindingsTx uses transaction-private ordinals to preserve child rows.
// The caller validates ownership, expected revision and the complete ID set.
func ReorderBindingsTx(ctx context.Context, tx *sql.Tx, charity bool, modelID int64, order []int64, now int64) error {
	if tx == nil || modelID <= 0 || len(order) > 256 || now < 0 || now > 253402300799 {
		return errors.New("invalid binding reorder")
	}
	table, _, column := bindingOrderNames(charity)
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE `+column+`=? AND ord BETWEEN 0 AND 255`, modelID).Scan(&count); err != nil {
		return err
	}
	if count != len(order) {
		return errors.New("binding reorder is incomplete")
	}
	seen := make(map[int64]bool, len(order))
	for _, id := range order {
		if id <= 0 || seen[id] {
			return errors.New("invalid binding reorder identity")
		}
		seen[id] = true
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET ord=ord+256 WHERE `+column+`=?`, modelID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != int64(len(order)) {
		return errors.New("binding reorder count mismatch")
	}
	for ord, id := range order {
		result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET ord=?,updated_at=? WHERE id=? AND `+column+`=? AND ord BETWEEN 256 AND 511`, ord, now, id, modelID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("binding reorder identity mismatch")
		}
	}
	return nil
}
