package lakenotes

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
		p.Exchanges[d] = ExchangeSetting{false, "", ""}
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
func currentPeriodTx(ctx context.Context, tx *sql.Tx, now int64) (Period, error) {
	var id string
	e := tx.QueryRowContext(ctx, "SELECT id FROM lake_notes_periods WHERE status='published' AND starts_at<=? AND ends_at>? ORDER BY starts_at,id LIMIT 1", now, now).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return Period{}, ErrNotFound
	}
	if e != nil {
		return Period{}, e
	}
	return periodTx(ctx, tx, id)
}
func validatePeriod(in PeriodInput) (int64, *int64, error) {
	r, e := revision(in.ExpectedRevision, true)
	if e != nil {
		return 0, nil, e
	}
	if strings.TrimSpace(in.Name) == "" || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 80 || strings.ContainsAny(in.Name, "\r\n") || in.StartsAt < 0 || in.EndsAt > maxUnix || in.StartsAt >= in.EndsAt {
		return 0, nil, ErrInvalid
	}
	if in.Status != "draft" && in.Status != "published" && in.Status != "cancelled" {
		return 0, nil, ErrInvalid
	}
	var fee *int64
	if in.EntryFeeMilli != nil {
		n, e := strconv.ParseInt(*in.EntryFeeMilli, 10, 64)
		if e != nil || n < 0 || n > db.MaxMoneyMilli || rev(n) != *in.EntryFeeMilli {
			return 0, nil, ErrInvalid
		}
		fee = &n
	}
	if in.Status == "published" && fee == nil || len(in.Exchanges) > 4 {
		return 0, nil, ErrInvalid
	}
	for d, v := range in.Exchanges {
		if d.stored() == "" {
			return 0, nil, ErrInvalid
		}
		if !v.Enabled && v.SourceAmount == "" && v.TargetAmount == "" {
			continue
		}
		for _, a := range []string{v.SourceAmount, v.TargetAmount} {
			n, e := db.ParseU128Decimal(a)
			if e != nil || n.Decimal() != a || n.Big().Sign() <= 0 {
				return 0, nil, ErrInvalid
			}
		}
	}
	return r, fee, nil
}
func (s *Service) SavePeriod(ctx context.Context, admin int64, id, key string, in PeriodInput) (MutationResult[Period], error) {
	expected, fee, e := validatePeriod(in)
	if e != nil {
		return MutationResult[Period]{}, e
	}
	if id == "" && expected != 0 || id != "" && (!db.ValidateOpaqueID(id, "lnp_") || expected < 1) {
		return MutationResult[Period]{}, ErrInvalid
	}
	route := adminBaseRoute + "/periods"
	method := "POST"
	if id != "" {
		route += "/" + id
		method = "PUT"
	}
	return mutate(ctx, s, admin, true, key, method, route, in, func(tx *sql.Tx, now time.Time) (Period, error) {
		target := id
		if target == "" {
			var e error
			target, e = db.GenerateOpaqueID("lnp_")
			if e != nil {
				return Period{}, e
			}
		}
		if in.Status == "published" {
			var overlap bool
			e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lake_notes_periods WHERE id<>? AND status='published' AND starts_at<? AND ends_at>?)", target, in.EndsAt, in.StartsAt).Scan(&overlap)
			if e != nil {
				return Period{}, e
			}
			if overlap {
				return Period{}, ErrConflict
			}
		}
		if id == "" {
			_, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,entry_fee_milli,created_at,updated_at) VALUES(?,?,1,?,?,?,?,?,?)", target, in.Name, in.Status, in.StartsAt, in.EndsAt, fee, now.Unix(), now.Unix())
		} else {
			var changed sql.Result
			changed, e = tx.ExecContext(ctx, "UPDATE lake_notes_periods SET name=?,status=?,starts_at=?,ends_at=?,entry_fee_milli=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", in.Name, in.Status, in.StartsAt, in.EndsAt, fee, now.Unix(), target, expected)
			if e == nil {
				var n int64
				n, e = changed.RowsAffected()
				if e == nil && n != 1 {
					e = ErrConflict
				}
			}
		}
		if e != nil {
			return Period{}, e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM lake_notes_exchange_settings WHERE period_id=?", target); e != nil {
			return Period{}, e
		}
		for _, d := range directions {
			v, ok := in.Exchanges[d]
			if !ok || !v.Enabled && v.SourceAmount == "" && v.TargetAmount == "" {
				continue
			}
			a, _ := db.ParseU128Decimal(v.SourceAmount)
			b, _ := db.ParseU128Decimal(v.TargetAmount)
			if _, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_exchange_settings(period_id,direction,enabled,source_lot,target_lot) VALUES(?,?,?,?,?)", target, d.stored(), v.Enabled, db.EncodeU128(a), db.EncodeU128(b)); e != nil {
				return Period{}, e
			}
		}
		current, e := currentPeriodTx(ctx, tx, now.Unix())
		if errors.Is(e, ErrNotFound) {
			if e = s.pauseTx(ctx, tx, now.UnixNano(), "", nil, false); e != nil {
				return Period{}, e
			}
		} else if e != nil {
			return Period{}, e
		} else if e = s.ClampLeaseDeadlineTx(ctx, tx, current.EndsAt); e != nil {
			return Period{}, e
		}
		return periodTx(ctx, tx, target)
	})
}

type PeriodPage struct {
	Items    []Period `json:"items"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	HasMore  bool     `json:"has_more"`
}

func (s *Service) Periods(ctx context.Context, admin int64, page, size int) (PeriodPage, error) {
	out := PeriodPage{Items: []Period{}, Page: page, PageSize: size}
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return out, ErrInvalid
	}
	tx, e := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = s.authorize(ctx, tx, admin, true); e != nil {
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
