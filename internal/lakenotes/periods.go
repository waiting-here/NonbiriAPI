package lakenotes

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func periodTx(ctx context.Context, tx *sql.Tx, id string) (Period, error) {
	var p Period
	var r int64
	var fee sql.NullInt64
	e := tx.QueryRowContext(ctx, "SELECT id,name,revision,status,starts_at,ends_at,entry_fee_milli FROM lake_notes_periods WHERE id=?", id).Scan(&p.ID, &p.Name, &r, &p.Status, &p.StartsAt, &p.EndsAt, &fee)
	if errors.Is(e, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if e != nil {
		return p, e
	}
	p.Revision = rev(r)
	if fee.Valid {
		v := rev(fee.Int64)
		p.EntryFeeMilli = &v
	}
	p.Exchanges = map[Direction]ExchangeSetting{}
	for _, d := range directions {
		p.Exchanges[d] = ExchangeSetting{}
	}
	rows, e := tx.QueryContext(ctx, "SELECT direction,enabled,source_lot,target_lot FROM lake_notes_exchange_settings WHERE period_id=?", id)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var v ExchangeSetting
		var a, b []byte
		if e = rows.Scan(&d, &v.Enabled, &a, &b); e != nil {
			return p, e
		}
		source, e := db.DecodeU128(a)
		if e != nil {
			return p, ErrInvariant
		}
		target, e := db.DecodeU128(b)
		if e != nil {
			return p, ErrInvariant
		}
		v.SourceAmount, v.TargetAmount = source.Decimal(), target.Decimal()
		direction := directionFromStored(d)
		if direction == "" {
			return p, ErrInvariant
		}
		p.Exchanges[direction] = v
	}
	return p, rows.Err()
}

type PeriodPage struct {
	Items    []Period `json:"items"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	HasMore  bool     `json:"has_more"`
}

func (s *Service) Periods(ctx context.Context, page, size int) (PeriodPage, error) {
	out := PeriodPage{Items: []Period{}, Page: page, PageSize: size}
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return out, ErrInvalid
	}
	tx, e := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = s.admins.AuthorizeAdminMutation(ctx, tx); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, "SELECT id FROM lake_notes_periods ORDER BY starts_at DESC,id LIMIT ? OFFSET ?", size+1, (page-1)*size)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > size {
		out.HasMore = true
		ids = ids[:size]
	}
	for _, id := range ids {
		p, e := periodTx(ctx, tx, id)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, p)
	}
	return out, tx.Commit()
}
