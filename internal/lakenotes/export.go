package lakenotes

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type UserExport struct {
	RulesID         string            `json:"rules_id"`
	ProfileRevision string            `json:"profile_revision"`
	Profile         rules.Profile     `json:"profile"`
	Casts           []CastView        `json:"casts"`
	Entries         []EntryReceipt    `json:"entries"`
	Exchanges       []ExchangeReceipt `json:"exchanges"`
}

// ExportUserTx joins the lifecycle owner's authorized consistent snapshot.
func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user int64, limit int) (UserExport, error) {
	var out UserExport
	if user <= 0 || tx == nil || limit < 1 || limit > lifecycle.CollectionLimit {
		return out, ErrInvalid
	}
	var exists bool
	if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=?)", user).Scan(&exists); e != nil {
		return out, e
	}
	if !exists {
		return out, ErrNotFound
	}
	row, e := profileTx(ctx, tx, user, false, 0)
	if e != nil {
		return out, e
	}
	out = UserExport{RulesID: rules.RulesID, ProfileRevision: rev(row.revision), Profile: row.profile, Casts: []CastView{}, Entries: []EntryReceipt{}, Exchanges: []ExchangeReceipt{}}
	rows, e := tx.QueryContext(ctx, "SELECT "+castColumns+" FROM lake_notes_casts WHERE user_id=? ORDER BY created_at,id LIMIT ?", user, limit+1)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		c, e := scanCast(rows.Scan)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Casts = append(out.Casts, c.view(row.revision))
		if len(out.Casts) > limit {
			rows.Close()
			return UserExport{}, lifecycle.ErrTooLarge
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, "SELECT period_id,period_revision,fee_milli,coalesce(ledger_operation_id,''),coalesce((SELECT ledger_seq FROM credit_operations WHERE id=ledger_operation_id),0),created_at FROM lake_notes_entitlements WHERE user_id=? ORDER BY created_at,period_id LIMIT ?", user, limit+1)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r EntryReceipt
		var pr, fee, seq int64
		if e = rows.Scan(&r.PeriodID, &pr, &fee, &r.OperationID, &seq, &r.CreatedAt); e != nil {
			rows.Close()
			return out, e
		}
		r.PeriodRevision, r.FeeMilli = rev(pr), rev(fee)
		if seq > 0 {
			r.LedgerSeq = rev(seq)
		}
		out.Entries = append(out.Entries, r)
		if len(out.Entries) > limit {
			rows.Close()
			return UserExport{}, lifecycle.ErrTooLarge
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, "SELECT r.id,coalesce(r.period_id,''),coalesce(r.period_revision,0),coalesce(r.config_revision,0),r.direction,r.quantity_mag,r.source_lot,r.target_lot,r.source_amount_mag,r.target_amount_mag,r.ledger_operation_id,o.ledger_seq,r.created_at FROM lake_notes_exchange_receipts r JOIN credit_operations o ON o.id=r.ledger_operation_id WHERE r.user_id=? ORDER BY r.created_at,r.id LIMIT ?", user, limit+1)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r ExchangeReceipt
		var pr, cr, seq int64
		var direction string
		var quantity, a, b, sa, ta []byte
		if e = rows.Scan(&r.ID, &r.PeriodID, &pr, &cr, &direction, &quantity, &a, &b, &sa, &ta, &r.OperationID, &seq, &r.CreatedAt); e != nil {
			rows.Close()
			return out, e
		}
		r.LedgerSeq, r.Direction = rev(seq), directionFromStored(direction)
		if pr > 0 {
			r.PeriodRevision = rev(pr)
		}
		if cr > 0 {
			r.SettingsRevision = rev(cr)
		}
		for _, field := range []struct {
			raw    []byte
			target *string
		}{{quantity, &r.Quantity}, {a, &r.SourceLot}, {b, &r.TargetLot}, {sa, &r.SourceAmount}, {ta, &r.TargetAmount}} {
			n, e := db.DecodeU128(field.raw)
			if e != nil {
				rows.Close()
				return out, ErrInvariant
			}
			*field.target = n.Decimal()
		}
		// A receipt exports only historical amounts, not a misleading current wallet.
		out.Exchanges = append(out.Exchanges, r)
		if len(out.Exchanges) > limit {
			rows.Close()
			return UserExport{}, lifecycle.ErrTooLarge
		}
	}
	e = rows.Err()
	rows.Close()
	return out, e
}
