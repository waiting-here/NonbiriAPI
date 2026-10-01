package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type lakeConfiguration struct{ emptyConfiguration }

func lakePublicTx(ctx context.Context, tx *sql.Tx, now int64) (json.RawMessage, error) {
	type setting struct {
		Enabled      bool   `json:"enabled"`
		SourceAmount string `json:"source_amount"`
		TargetAmount string `json:"target_amount"`
	}
	type period struct {
		ID            string             `json:"id"`
		Name          string             `json:"name"`
		Revision      string             `json:"revision"`
		StartsAt      int64              `json:"starts_at"`
		EndsAt        int64              `json:"ends_at"`
		EntryFeeMilli *string            `json:"entry_fee_milli"`
		Exchanges     map[string]setting `json:"exchanges"`
	}
	var out struct {
		Periods []period `json:"periods"`
	}
	out.Periods = []period{}
	rows, e := tx.QueryContext(ctx, "SELECT id,name,revision,starts_at,ends_at,entry_fee_milli FROM lake_notes_periods WHERE status='published' AND ends_at>? ORDER BY starts_at,id LIMIT 2", now)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var p period
		var revision int64
		var fee sql.NullInt64
		if e = rows.Scan(&p.ID, &p.Name, &revision, &p.StartsAt, &p.EndsAt, &fee); e != nil {
			rows.Close()
			return nil, e
		}
		p.Revision = strconv.FormatInt(revision, 10)
		if fee.Valid {
			v := strconv.FormatInt(fee.Int64, 10)
			p.EntryFeeMilli = &v
		}
		p.Exchanges = map[string]setting{}
		out.Periods = append(out.Periods, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i := range out.Periods {
		rows, e = tx.QueryContext(ctx, "SELECT direction,enabled,source_lot,target_lot FROM lake_notes_exchange_settings WHERE period_id=? AND enabled=1", out.Periods[i].ID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var d string
			var v setting
			var a, b []byte
			if e = rows.Scan(&d, &v.Enabled, &a, &b); e != nil {
				rows.Close()
				return nil, e
			}
			source, e := db.DecodeU128(a)
			if e != nil {
				rows.Close()
				return nil, ErrInvariant
			}
			target, e := db.DecodeU128(b)
			if e != nil {
				rows.Close()
				return nil, ErrInvariant
			}
			v.SourceAmount, v.TargetAmount = source.Decimal(), target.Decimal()
			name := map[string]string{"coin_to_general": "coins_to_general", "general_to_coin": "general_to_coins", "coin_to_game": "coins_to_game", "game_to_coin": "game_to_coins"}[d]
			if name == "" {
				rows.Close()
				return nil, ErrInvariant
			}
			out.Periods[i].Exchanges[name] = v
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return json.Marshal(out)
}
