package charityscope

import (
	"context"
	"database/sql"
)

// RequireBinding applies the existing key maintenance scope to every target.
func (scope Scope) RequireBinding(ctx context.Context, tx *sql.Tx, bindingID, now int64) error {
	if scope.ModelID <= 0 || bindingID <= 0 {
		return ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT dk.donation_id,dk.id FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id JOIN donation_keys dk ON dk.id=t.donation_key_id WHERE b.id=? AND b.charity_model_id=? ORDER BY t.ord,t.id LIMIT 257`, bindingID, scope.ModelID)
	if err != nil {
		return err
	}
	type key struct{ donationID, keyID int64 }
	keys := []key{}
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.donationID, &item.keyID); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(keys) == 0 || len(keys) > 256 {
		return ErrNotFound
	}
	for _, item := range keys {
		if err := scope.RequireKey(ctx, tx, item.donationID, item.keyID, now, false); err != nil {
			return err
		}
	}
	return nil
}
