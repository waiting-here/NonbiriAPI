package ranking

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var ErrExportTooLarge = errors.New("ranking export exceeds collection limit")

type TotalExport struct {
	Board      string `json:"board"`
	Window     string `json:"window"`
	Amount     string `json:"amount"`
	AchievedAt int64  `json:"achieved_at"`
}
type EventExport struct {
	Game           string  `json:"game"`
	SettledAt      int64   `json:"settled_at"`
	Loss           *string `json:"loss"`
	PositiveProfit *string `json:"positive_profit"`
}
type PersonalExport struct {
	StatisticsStart int64         `json:"statistics_start"`
	Totals          []TotalExport `json:"totals"`
	Events          []EventExport `json:"events"`
}

// ExportTx is bounded and owner-only. The lifecycle coordinator supplies its
// final authorization snapshot and maps catching-up to a retryable response.
func ExportTx(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (PersonalExport, error) {
	if ctx == nil || tx == nil || user <= 0 || now < 0 || limit < 1 || limit > 10000 {
		return PersonalExport{}, ErrInvalid
	}
	ready, err := ReadyTx(ctx, tx, now)
	if err != nil {
		return PersonalExport{}, err
	}
	if !ready {
		return PersonalExport{}, ErrCatchingUp
	}
	result := PersonalExport{Totals: []TotalExport{}, Events: []EventExport{}}
	if err := tx.QueryRowContext(ctx, `SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&result.StatisticsStart); err != nil {
		return PersonalExport{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT board,window,amount_sign,amount_mag,achieved_at FROM game_rank_totals WHERE user_id=? ORDER BY board,window LIMIT ?`, user, limit+1)
	if err != nil {
		return PersonalExport{}, err
	}
	for rows.Next() {
		var item TotalExport
		var sign int
		var mag []byte
		if err := rows.Scan(&item.Board, &item.Window, &sign, &mag, &item.AchievedAt); err != nil {
			rows.Close()
			return PersonalExport{}, err
		}
		value, err := db.NewSM128(sign, mag)
		if err != nil {
			rows.Close()
			return PersonalExport{}, err
		}
		item.Amount = credits(value.Big())
		result.Totals = append(result.Totals, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PersonalExport{}, err
	}
	if err := rows.Close(); err != nil {
		return PersonalExport{}, err
	}
	if len(result.Totals) > limit {
		return PersonalExport{}, ErrExportTooLarge
	}
	rows, err = tx.QueryContext(ctx, `SELECT game_key,settled_at,loss_sign,loss_mag,positive_profit,profit_30d_expires_at FROM game_rank_events WHERE user_id=? AND settled_at<=? AND (charity_expires_at>? OR profit_30d_expires_at>?) ORDER BY settled_at,seq LIMIT ?`, user, now, now, now, limit+1)
	if err != nil {
		return PersonalExport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item EventExport
		var sign, profitExpiry sql.NullInt64
		var loss, profit []byte
		if err := rows.Scan(&item.Game, &item.SettledAt, &sign, &loss, &profit, &profitExpiry); err != nil {
			return PersonalExport{}, err
		}
		if sign.Valid {
			value, err := db.NewSM128(int(sign.Int64), loss)
			if err != nil {
				return PersonalExport{}, err
			}
			item.Loss = new(credits(value.Big()))
		}
		if profitExpiry.Valid {
			value, err := db.DecodeU128(profit)
			if err != nil {
				return PersonalExport{}, err
			}
			item.PositiveProfit = new(credits(value.Big()))
		}
		result.Events = append(result.Events, item)
		if len(result.Events) > limit {
			return PersonalExport{}, ErrExportTooLarge
		}
	}
	return result, rows.Err()
}
